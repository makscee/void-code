package pibin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesManagedRuntimeNotPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	malicious := filepath.Join(home, "malicious")
	if err := os.MkdirAll(malicious, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(malicious, "pi"), []byte("malicious"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", malicious)

	path := managedPiPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("managed"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("Resolve() = %q, want managed path %q", got, path)
	}
}

func TestMissingMessage(t *testing.T) {
	const want = "VC managed Pi runtime not found — Pi must be provisioned by VC\n" +
		"Re-run the VC installer to provision its managed Pi runtime."
	if got := MissingMessage(); got != want {
		t.Fatalf("MissingMessage() = %q, want %q", got, want)
	}
}
