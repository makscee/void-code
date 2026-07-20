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

func Save(token string) error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	return saveAt(path, token, os.Rename)
}

func saveAt(path, token string, rename func(string, string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create credential directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("cannot secure credential directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("cannot create credential replacement: %w", err)
	}
	tmp := file.Name()
	cleanup := func() { file.Close(); os.Remove(tmp) }
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("cannot secure credential replacement: %w", err)
	}
	if _, err := file.WriteString(token); err != nil {
		cleanup()
		return fmt.Errorf("cannot write credential replacement: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("cannot sync credential replacement: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cannot close credential replacement: %w", err)
	}
	if err := rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cannot install credential replacement: %w", err)
	}
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("cannot open credential directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("cannot sync credential directory: %w", err)
	}
	return nil
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
