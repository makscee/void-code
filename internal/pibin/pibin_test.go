package pibin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedPiPathUsesPlatformPackageEntrypoint(t *testing.T) {
	home := filepath.Join("test home", "user")
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"linux", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")},
		{"darwin", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")},
		{"windows", filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", ".bin", "pi.cmd")},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			if got := managedPiPathForOS(home, tc.goos); got != tc.want {
				t.Fatalf("managedPiPathForOS() = %q, want %q", got, tc.want)
			}
		})
	}
}

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

	// Resolve canonicalizes the home directory with filepath.EvalSymlinks before
	// building the managed path, so the expectation must be canonical too. On
	// macOS t.TempDir() lives under /var/folders and /var is a symlink to
	// /private/var: comparing against the uncanonicalized path fails there while
	// passing in Linux CI, which is why this went unnoticed.
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	path := managedPiPath(canonicalHome)
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

func TestResolveRejectsSymlinkedRuntimeParent(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("symlink permissions vary on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(outside, "runtime", "pi", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	if err := os.WriteFile(entry, []byte("managed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".void-code")); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve(); err == nil {
		t.Fatal("Resolve accepted a managed runtime below a symlinked parent")
	}
}

func TestMissingMessage(t *testing.T) {
	const want = "VC managed Pi runtime not found — Pi must be provisioned by VC\n" +
		"Re-run the VC installer to provision its managed Pi runtime."
	if got := MissingMessage(); got != want {
		t.Fatalf("MissingMessage() = %q, want %q", got, want)
	}
}
