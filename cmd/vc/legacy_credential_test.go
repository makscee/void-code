package main

import (
	"github.com/makscee/void-code/internal/auth"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalPathsDoNotMigrateLegacyClaudeToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	legacy := filepath.Join(home, ".claudev")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "token"), []byte("legacy-only"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _, err := auth.Load()
	if token != "" {
		t.Fatalf("Load imported legacy token %q err=%v", token, err)
	}
	state, _, _, _ := resolveLocalAuthStateWithSource()
	if state.LoggedIn {
		t.Fatal("legacy-only credential authenticated normal launch")
	}
	if _, err := os.Stat(filepath.Join(home, ".void-code", "token")); !os.IsNotExist(err) {
		t.Fatalf("legacy credential copied: %v", err)
	}
	if err := auth.Save("current"); err != nil {
		t.Fatal(err)
	}
	token, _, err = auth.Load()
	if err != nil || token != "current" {
		t.Fatalf("current token not loaded: %q %v", token, err)
	}
}
