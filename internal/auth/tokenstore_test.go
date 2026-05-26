package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadWipe(t *testing.T) {
	// Use a temp dir as the home directory so we don't touch the real ~/.void-code.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// On some macOS setups UserHomeDir checks $HOME; reset so our stub takes.

	const tok = "test-token-abc123"

	// Load before save → ErrNotLoggedIn.
	_, err := Load()
	if err != ErrNotLoggedIn {
		t.Fatalf("expected ErrNotLoggedIn before save, got %v", err)
	}

	// Save → file must exist with mode 0600.
	if err := Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tokenFile := filepath.Join(dir, ".void-code", "token")
	info, err := os.Stat(tokenFile)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 0600", perm)
	}

	// Load → same token.
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != tok {
		t.Errorf("Load = %q, want %q", got, tok)
	}

	// Create a relay-ca.pem too to test Wipe.
	caPath := filepath.Join(dir, ".void-code", "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("fake-ca"), 0o600); err != nil {
		t.Fatalf("setup relay-ca.pem: %v", err)
	}

	// Wipe → both files gone.
	if err := Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := os.Stat(tokenFile); !os.IsNotExist(err) {
		t.Errorf("token file should not exist after Wipe")
	}
	if _, err := os.Stat(caPath); !os.IsNotExist(err) {
		t.Errorf("relay-ca.pem should not exist after Wipe")
	}

	// Load after Wipe → ErrNotLoggedIn again.
	_, err = Load()
	if err != ErrNotLoggedIn {
		t.Fatalf("expected ErrNotLoggedIn after Wipe, got %v", err)
	}

	// Second Wipe → idempotent (no error for missing files).
	if err := Wipe(); err != nil {
		t.Fatalf("second Wipe should be idempotent: %v", err)
	}
}

func TestSaveOverwrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	if err := Save("first"); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := Save("second"); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "second" {
		t.Errorf("Load after overwrite = %q, want %q", got, "second")
	}
}
