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

	// Load before save → ErrNotLoggedIn (neither vc nor legacy path present).
	_, _, err := Load()
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

	// Load → same token, not migrated.
	got, migrated, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != tok {
		t.Errorf("Load = %q, want %q", got, tok)
	}
	if migrated {
		t.Error("Load: migratedFromLegacy should be false when canonical token exists")
	}

	// Create a relay-ca.pem too to test Wipe.
	caPath := filepath.Join(dir, ".void-code", "relay-ca.pem")
	if err := os.WriteFile(caPath, []byte("fake-ca"), 0o600); err != nil {
		t.Fatalf("setup relay-ca.pem: %v", err)
	}

	// Wipe → both vc files gone; legacy file (if any) untouched.
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
	_, _, err = Load()
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
	got, _, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != "second" {
		t.Errorf("Load after overwrite = %q, want %q", got, "second")
	}
}

// TestLegacyFallback verifies that when ~/.void-code/token is absent but
// ~/.claudev/token is present, Load returns the legacy token with migratedFromLegacy=true.
func TestLegacyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	const legacyTok = "legacy-cv-token-xyz"

	// Write only the legacy cv token.
	legacyDir := filepath.Join(dir, ".claudev")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir .claudev: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "token"), []byte(legacyTok), 0o600); err != nil {
		t.Fatalf("write legacy token: %v", err)
	}

	// Load → legacy token returned with migrated=true.
	got, migrated, err := Load()
	if err != nil {
		t.Fatalf("Load with only legacy token: %v", err)
	}
	if got != legacyTok {
		t.Errorf("Load = %q, want %q", got, legacyTok)
	}
	if !migrated {
		t.Error("Load: migratedFromLegacy should be true when only legacy token exists")
	}
}

// TestLoadAndMigrate verifies that LoadAndMigrate promotes the legacy token to
// ~/.void-code/token without deleting ~/.claudev/token.
func TestLoadAndMigrate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	const legacyTok = "legacy-cv-token-migrate"

	// Populate only the legacy cv token.
	legacyDir := filepath.Join(dir, ".claudev")
	legacyFile := filepath.Join(legacyDir, "token")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir .claudev: %v", err)
	}
	if err := os.WriteFile(legacyFile, []byte(legacyTok), 0o600); err != nil {
		t.Fatalf("write legacy token: %v", err)
	}

	// LoadAndMigrate should succeed and return the token.
	got, err := LoadAndMigrate()
	if err != nil {
		t.Fatalf("LoadAndMigrate: %v", err)
	}
	if got != legacyTok {
		t.Errorf("LoadAndMigrate = %q, want %q", got, legacyTok)
	}

	// Canonical token file must have been created.
	canonicalFile := filepath.Join(dir, ".void-code", "token")
	canonicalBytes, err := os.ReadFile(canonicalFile)
	if err != nil {
		t.Fatalf("~/.void-code/token not materialised after LoadAndMigrate: %v", err)
	}
	if string(canonicalBytes) != legacyTok {
		t.Errorf("canonical token = %q, want %q", string(canonicalBytes), legacyTok)
	}

	// Legacy file must still be present (HARD INVARIANT: never delete it).
	if _, err := os.Stat(legacyFile); os.IsNotExist(err) {
		t.Error("~/.claudev/token must NOT be deleted by LoadAndMigrate (cv still uses it)")
	}

	// Second Load should return canonical (not legacy), migrated=false.
	tok2, migrated2, err := Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if tok2 != legacyTok {
		t.Errorf("Load after migration = %q, want %q", tok2, legacyTok)
	}
	if migrated2 {
		t.Error("Load after migration: migratedFromLegacy should be false (canonical exists now)")
	}
}

// TestWipePreservesLegacy verifies that Wipe removes only vc files and leaves
// ~/.claudev/token untouched.
func TestWipePreservesLegacy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	const tok = "some-vc-token"
	const legacyTok = "some-cv-token"

	// Set up both canonical and legacy files.
	if err := Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	legacyDir := filepath.Join(dir, ".claudev")
	legacyFile := filepath.Join(legacyDir, "token")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir .claudev: %v", err)
	}
	if err := os.WriteFile(legacyFile, []byte(legacyTok), 0o600); err != nil {
		t.Fatalf("write legacy token: %v", err)
	}

	// Wipe.
	if err := Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}

	// vc canonical file must be gone.
	if _, err := os.Stat(filepath.Join(dir, ".void-code", "token")); !os.IsNotExist(err) {
		t.Error("~/.void-code/token should be gone after Wipe")
	}

	// Legacy cv file must still exist.
	if _, err := os.Stat(legacyFile); os.IsNotExist(err) {
		t.Error("~/.claudev/token must NOT be deleted by Wipe")
	}
}

// TestLegacyTokenExists verifies the LegacyTokenExists helper.
func TestLegacyTokenExists(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// No legacy file → false.
	if LegacyTokenExists() {
		t.Error("LegacyTokenExists should be false when ~/.claudev/token absent")
	}

	// Create legacy file → true.
	legacyDir := filepath.Join(dir, ".claudev")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir .claudev: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "token"), []byte("tok"), 0o600); err != nil {
		t.Fatalf("write legacy token: %v", err)
	}
	if !LegacyTokenExists() {
		t.Error("LegacyTokenExists should be true when ~/.claudev/token present")
	}
}
