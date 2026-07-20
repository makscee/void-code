package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadWipeAndPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save("opaque-first"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".void-code", "token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode=%o", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(path))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode=%o", dirInfo.Mode().Perm())
	}
	got, legacy, err := Load()
	if err != nil || got != "opaque-first" || legacy {
		t.Fatalf("load mismatch")
	}
	if err := Wipe(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected logged out, got %v", err)
	}
}

func TestAtomicReplacementPreservesPreviousOnRenameFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save("previous-credential"); err != nil {
		t.Fatal(err)
	}
	path, _ := tokenPath()
	failure := errors.New("injected rename failure")
	if err := saveAt(path, "replacement-credential", func(string, string) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
	got, _, err := Load()
	if err != nil || got != "previous-credential" {
		t.Fatalf("previous credential was replaced: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".token-*"))
	if len(matches) != 0 {
		t.Fatalf("temporary credentials remain")
	}
	if err := Save("replacement-credential"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = Load()
	if got != "replacement-credential" {
		t.Fatal("replacement did not persist")
	}
}

func TestLegacyTokenIsRetainedButNeverLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, ".claudev", "token")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy-input-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !LegacyTokenExists() {
		t.Fatal("legacy migration input not detected")
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("legacy token was accepted: %v", err)
	}
	if _, err := LoadAndMigrate(); !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("legacy token was migrated early: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != "legacy-input-only" {
		t.Fatal("legacy input changed")
	}
}
