package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrNotLoggedIn = errors.New("not logged in — run: vc login")

func tokenDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".void-code"), nil
}
func tokenPath() (string, error) {
	dir, err := tokenDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token"), nil
}
func legacyTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claudev", "token"), nil
}

func LegacyTokenExists() bool {
	path, err := legacyTokenPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

type credentialOps struct {
	write    func(*os.File, string) error
	fileSync func(*os.File) error
	rename   func(string, string) error
	dirSync  func(string) error
}

func defaultCredentialOps() credentialOps {
	return credentialOps{
		write:    func(file *os.File, value string) error { _, err := file.WriteString(value); return err },
		fileSync: func(file *os.File) error { return file.Sync() },
		rename:   os.Rename,
		dirSync:  syncDirectory,
	}
}

func Save(token string) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	return saveWithOps(path, token, defaultCredentialOps())
}

// saveAt retains the narrow rename seam used by existing callers and tests.
func saveAt(path, token string, rename func(string, string) error) error {
	ops := defaultCredentialOps()
	ops.rename = rename
	return saveWithOps(path, token, ops)
}

func saveWithOps(path, token string, ops credentialOps) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("cannot secure credential directory: %w", err)
	}
	previous, readErr := os.ReadFile(path)
	hadPrevious := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("cannot read previous credential: %w", readErr)
	}

	file, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("cannot create credential replacement: %w", err)
	}
	tmp := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(tmp) }
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("cannot secure credential replacement: %w", err)
	}
	if err := ops.write(file, token); err != nil {
		cleanup()
		return fmt.Errorf("cannot write credential replacement: %w", err)
	}
	if err := ops.fileSync(file); err != nil {
		cleanup()
		return fmt.Errorf("cannot sync credential replacement: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cannot close credential replacement: %w", err)
	}

	backup := ""
	if hadPrevious {
		marker, err := os.CreateTemp(dir, ".token-backup-*")
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("cannot prepare credential backup: %w", err)
		}
		backup = marker.Name()
		_ = marker.Close()
		_ = os.Remove(backup)
		if err := ops.rename(path, backup); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("cannot back up previous credential: %w", err)
		}
	}
	if err := ops.rename(tmp, path); err != nil {
		if hadPrevious {
			_ = os.Rename(backup, path)
		}
		_ = os.Remove(tmp)
		return fmt.Errorf("cannot install credential replacement: %w", err)
	}

	rollback := func(cause error) error {
		_ = os.Remove(path)
		if hadPrevious {
			if backup != "" {
				if err := os.Rename(backup, path); err != nil {
					return fmt.Errorf("%v; cannot restore previous credential: %w", cause, err)
				}
			} else if err := restoreCredential(path, previous); err != nil {
				return fmt.Errorf("%v; cannot restore previous credential: %w", cause, err)
			}
		}
		if err := syncDirectory(dir); err != nil {
			return fmt.Errorf("%v; cannot sync credential rollback: %w", cause, err)
		}
		return cause
	}
	if err := ops.dirSync(dir); err != nil {
		return fmt.Errorf("cannot sync credential directory: %w", rollback(err))
	}
	if hadPrevious {
		if err := os.Remove(backup); err != nil {
			return fmt.Errorf("cannot remove credential backup: %w", rollback(err))
		}
		backup = ""
		if err := ops.dirSync(dir); err != nil {
			return fmt.Errorf("cannot sync credential backup cleanup: %w", rollback(err))
		}
	}
	return nil
}

func restoreCredential(path string, value []byte) error {
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".token-rollback-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads only the canonical identity credential. The legacy token remains
// untouched as later migration input and is never accepted as identity auth.
func Load() (string, bool, error) {
	path, err := tokenPath()
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, ErrNotLoggedIn
	}
	if err != nil {
		return "", false, fmt.Errorf("cannot read token file: %w", err)
	}
	return string(data), false, nil
}

// LoadAndMigrate is retained for callers while migration is deferred to VI-10.
// It loads only the canonical credential and never reads or copies legacy auth.
func LoadAndMigrate() (string, error) { token, _, err := Load(); return token, err }

func Wipe() error {
	dir, err := tokenDir()
	if err != nil {
		return err
	}
	for _, name := range []string{"token", "relay-ca.pem"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot remove %s: %w", name, err)
		}
	}
	return nil
}
