// Package auth implements void-code authentication primitives:
//   - access-code exchange (Flow 1a)
//   - device-code flow (Flow 1b)
//   - token store (~/.void-code/token, mode 0600)
//   - legacy cv fallback (~/.claudev/token read-only, never written or deleted)
package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotLoggedIn is returned by Load when no token file exists (neither vc nor legacy cv path).
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

// legacyTokenPath returns the full path to ~/.claudev/token (cv legacy path).
// This file is read-only from vc's perspective — never written or deleted.
func legacyTokenPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home dir: %w", err)
	}
	return filepath.Join(home, ".claudev", "token"), nil
}

// LegacyTokenExists reports whether the cv legacy token file (~/.claudev/token) is present.
// Used by logout to warn the user that cv credentials are still active.
func LegacyTokenExists() bool {
	path, err := legacyTokenPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Save writes token to ~/.void-code/token with mode 0600.
// Creates ~/.void-code with mode 0700 if it doesn't exist.
// Never writes to the legacy ~/.claudev/token path.
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

// Load reads the token, trying ~/.void-code/token first, then the legacy cv
// path ~/.claudev/token.
//
// Return values:
//   - (token, false, nil)  — canonical vc token found
//   - (token, true,  nil)  — only legacy cv token found; caller should call Save to migrate
//   - ("",   false, ErrNotLoggedIn) — neither file exists
func Load() (token string, migratedFromLegacy bool, err error) {
	path, pathErr := tokenPath()
	if pathErr != nil {
		return "", false, pathErr
	}
	data, readErr := os.ReadFile(path)
	if readErr == nil {
		return string(data), false, nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("cannot read token file: %w", readErr)
	}

	// Canonical path absent — try legacy cv path.
	legacyPath, legacyPathErr := legacyTokenPath()
	if legacyPathErr != nil {
		return "", false, ErrNotLoggedIn
	}
	legacyData, legacyReadErr := os.ReadFile(legacyPath)
	if legacyReadErr != nil {
		if errors.Is(legacyReadErr, os.ErrNotExist) {
			return "", false, ErrNotLoggedIn
		}
		return "", false, fmt.Errorf("cannot read legacy token file: %w", legacyReadErr)
	}
	return string(legacyData), true, nil
}

// LoadAndMigrate loads the token (falling back to legacy cv path) and, if the
// token came from the legacy path, silently writes it to ~/.void-code/token so
// subsequent loads skip the fallback.  The legacy ~/.claudev/token is NEVER
// deleted — cv may still be installed and using it.
func LoadAndMigrate() (string, error) {
	token, migrated, err := Load()
	if err != nil {
		return "", err
	}
	if migrated {
		// Best-effort: ignore save errors so a read-only filesystem never blocks launch.
		_ = Save(token)
	}
	return token, nil
}

// Wipe removes ~/.void-code/token and ~/.void-code/relay-ca.pem.
// Missing files are silently ignored (already wiped = success).
// The legacy cv token at ~/.claudev/token is intentionally NOT removed.
// Use LegacyTokenExists to check whether a warning is warranted.
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
