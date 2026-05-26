// Package auth implements void-code authentication primitives:
//   - access-code exchange (Flow 1a)
//   - device-code flow (Flow 1b)
//   - token store (~/.void-code/token, mode 0600)
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotLoggedIn is returned by Load when no token file exists.
var ErrNotLoggedIn = errors.New("not logged in — run: vc login")

// tokenDir returns the ~/.void-code directory path.
func tokenDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".void-code"), nil
}

// tokenPath returns the full path to ~/.void-code/token.
func tokenPath() (string, error) {
	dir, err := tokenDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token"), nil
}

// Save writes token to ~/.void-code/token with mode 0600.
// Creates ~/.void-code with mode 0700 if it doesn't exist.
func Save(token string) error {
	dir, err := tokenDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	path, err := tokenPath()
	if err != nil {
		return err
	}
	// WriteFile creates or truncates; set mode 0600 so token is never world-readable.
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return fmt.Errorf("cannot write token file: %w", err)
	}
	return nil
}

// Load reads the token from ~/.void-code/token.
// Returns ErrNotLoggedIn if the file does not exist.
func Load() (string, error) {
	path, err := tokenPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotLoggedIn
		}
		return "", fmt.Errorf("cannot read token file: %w", err)
	}
	return string(data), nil
}

// Wipe removes ~/.void-code/token and ~/.void-code/relay-ca.pem.
// Missing files are silently ignored (already wiped = success).
func Wipe() error {
	dir, err := tokenDir()
	if err != nil {
		return err
	}
	for _, name := range []string{"token", "relay-ca.pem"} {
		p := filepath.Join(dir, name)
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot remove %s: %w", p, err)
		}
	}
	return nil
}
