package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedExtensionOwnershipDoesNotOverwriteForeignFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	dir := filepath.Join(home, "agent", "extensions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "void-code.ts")
	if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileManagedPiExtension(); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign extension accepted: %v", err)
	}
}
