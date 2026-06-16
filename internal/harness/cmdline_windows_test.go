//go:build windows

package harness

import (
	"os/exec"
	"strings"
	"testing"
)

// TestApplyCmdLineBatchShimRoutesThroughComspec verifies the wiring that was
// shipped broken once: a .cmd shim must have cmd.Path reset to %ComSpec%
// (cmd.exe) and SysProcAttr.CmdLine set to the cmd.exe payload carrying the
// quoted, spaced shim path. This is the field CreateProcess actually uses, so a
// regression here silently breaks the spaced-home-dir launch.
func TestApplyCmdLineBatchShimRoutesThroughComspec(t *testing.T) {
	t.Setenv("ComSpec", comspec)

	bin := spacedShim
	args := []string{"--settings", `C:\x y\cc.json`}
	cmd := exec.Command(bin, args...)

	applyCmdLine(cmd, bin, args)

	if !strings.EqualFold(cmd.Path, comspec) {
		t.Fatalf("cmd.Path not reset to comspec\n  got:  %s\n  want: %s", cmd.Path, comspec)
	}
	if !strings.HasSuffix(strings.ToLower(cmd.Path), `cmd.exe`) {
		t.Fatalf("cmd.Path does not resolve to cmd.exe: %s", cmd.Path)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil; batch shim was not routed through cmd.exe")
	}
	if cmd.SysProcAttr.CmdLine == "" {
		t.Fatal("SysProcAttr.CmdLine is empty for batch shim")
	}
	if !strings.Contains(cmd.SysProcAttr.CmdLine, `"`+spacedShim+`"`) {
		t.Fatalf("CmdLine missing quoted spaced shim path: %s", cmd.SysProcAttr.CmdLine)
	}
}

// TestApplyCmdLineExeLeftUntouched verifies a real .exe stays on the
// direct-exec path: Go's argv builder already quotes a spaced path correctly,
// so applyCmdLine must not rewrite cmd.Path nor set a SysProcAttr.CmdLine.
func TestApplyCmdLineExeLeftUntouched(t *testing.T) {
	t.Setenv("ComSpec", comspec)

	bin := `C:\Program Files\nodejs\claude.exe`
	args := []string{"--settings", `C:\x y\cc.json`}
	cmd := exec.Command(bin, args...)
	origPath := cmd.Path

	applyCmdLine(cmd, bin, args)

	if cmd.Path != origPath {
		t.Fatalf("cmd.Path was rewritten for .exe\n  got:  %s\n  want: %s", cmd.Path, origPath)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.CmdLine != "" {
		t.Fatalf("SysProcAttr.CmdLine set for .exe (should be untouched): %s", cmd.SysProcAttr.CmdLine)
	}
}
