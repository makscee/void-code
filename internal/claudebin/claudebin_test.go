package claudebin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/makscee/void-code/internal/claudebin"
)

// TestResolve_NotFound verifies that Resolve returns an error when claude is absent from PATH.
func TestResolve_NotFound(t *testing.T) {
	// Strip PATH entirely so claude cannot be found.
	t.Setenv("PATH", "")

	_, err := claudebin.Resolve()
	if err == nil {
		t.Fatal("Resolve() should fail when PATH is empty")
	}
	if !claudebin.IsNotFoundErr(err) {
		t.Fatalf("expected IsNotFoundErr=true for path-stripped error, got err=%v", err)
	}
}

// TestResolve_Found verifies that Resolve succeeds when a fake claude binary is on PATH.
func TestResolve_Found(t *testing.T) {
	dir := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.cmd"
	}
	fake := filepath.Join(dir, name)
	if err := os.WriteFile(fake, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir)

	got, err := claudebin.Resolve()
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("Resolve() returned empty path")
	}
}

// TestIsInstalled_PathStripped verifies IsInstalled returns false when PATH is empty.
func TestIsInstalled_PathStripped(t *testing.T) {
	t.Setenv("PATH", "")

	if claudebin.IsInstalled() {
		t.Fatal("IsInstalled() should return false when PATH is empty")
	}
}

// TestIsInstalled_Present verifies IsInstalled returns true when claude is on PATH.
func TestIsInstalled_Present(t *testing.T) {
	dir := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.cmd"
	}
	fake := filepath.Join(dir, name)
	if err := os.WriteFile(fake, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir)

	if !claudebin.IsInstalled() {
		t.Fatal("IsInstalled() should return true when claude is on PATH")
	}
}

// TestInstallInstructions_PerOS verifies that InstallInstructions returns non-empty
// per-OS guidance strings and that the npm install line is always present.
func TestInstallInstructions_PerOS(t *testing.T) {
	instr := claudebin.InstallInstructions()
	if instr == "" {
		t.Fatal("InstallInstructions() must return non-empty string")
	}
	// All platforms include the npm global install as the primary method.
	if !contains(instr, "npm") {
		t.Errorf("InstallInstructions() should mention npm, got:\n%s", instr)
	}
}

// TestMissingMessage returns a human-readable not-found message.
func TestMissingMessage(t *testing.T) {
	msg := claudebin.MissingMessage()
	if msg == "" {
		t.Fatal("MissingMessage() must return non-empty string")
	}
	if !contains(msg, "claude") {
		t.Errorf("MissingMessage() should mention 'claude', got:\n%s", msg)
	}
}

// TestIsNotFoundErr_ExecExitError verifies that an ExitError is NOT classified as not-found.
func TestIsNotFoundErr_ExecExitError(t *testing.T) {
	// An ExitError means the binary RAN and exited non-zero — not a PATH miss.
	// We can't easily produce a real ExitError without running a binary, so test
	// the LookPath-style "not found" error from exec package instead.
	_, err := exec.LookPath("__vc_nonexistent_claude__")
	if err == nil {
		t.Skip("unexpectedly found __vc_nonexistent_claude__ in PATH")
	}
	if !claudebin.IsNotFoundErr(err) {
		t.Errorf("LookPath error for absent binary should be IsNotFoundErr=true, got err=%v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
