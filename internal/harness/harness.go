// Package harness provides the Spawn seam for launching a wrapped binary with
// relay-injected env.  v0 hardcodes the wrapped binary to "claude"; a future
// codex/pi adapter calls Spawn with a different wrappedBin — no surgery here.
package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Spawn directly executes an absolute, validated wrappedBin, replaces the
// current process's stdio streams with passthrough handles, and runs it to completion.
// On Unix exec.Cmd.Run() is used (the bubbletea TUI must have exited before
// this call so no two programs own the terminal simultaneously).  On Windows,
// if the resolved binary is a .cmd/.bat shim (npm installs claude this way), it
// is routed through cmd.exe (see applyCmdLine) because CreateProcess cannot
// launch a script directly; a spaced shim path is preserved by cmd.exe quoting.
// Exit code is propagated via ExitError.
func Spawn(ctx context.Context, wrappedBin string, args []string, env []string) error {
	if !filepath.IsAbs(wrappedBin) {
		return fmt.Errorf("vc: wrapped binary path must be absolute")
	}
	info, err := os.Lstat(wrappedBin)
	if err != nil {
		return fmt.Errorf("vc: wrapped binary unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("vc: wrapped binary is not a regular file: %s", wrappedBin)
	}

	cmd := exec.CommandContext(ctx, wrappedBin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// On Windows, if wrappedBin is a .cmd/.bat shim (the managed npm pi.cmd may
	// live under a home directory with a space), re-point cmd.Path at cmd.exe
	// and build a /s /c command line so a script can launch and the spaced path
	// survives.  Real .exe stays on the direct path.  No-op on Unix.
	applyCmdLine(cmd, wrappedBin, args)

	return cmd.Run()
}
