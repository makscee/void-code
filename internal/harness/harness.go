// Package harness provides the Spawn seam for launching a wrapped binary with
// relay-injected env.  v0 hardcodes the wrapped binary to "claude"; a future
// codex/pi adapter calls Spawn with a different wrappedBin — no surgery here.
package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Spawn resolves wrappedBin via PATH, replaces the current process's stdio
// streams with passthrough handles, and runs the binary to completion.
// On Unix exec.Cmd.Run() is used (the bubbletea TUI must have exited before
// this call so no two programs own the terminal simultaneously).
// Exit code is propagated via ExitError.
func Spawn(ctx context.Context, wrappedBin string, args []string, env []string) error {
	bin, err := exec.LookPath(wrappedBin)
	if err != nil {
		return fmt.Errorf("vc: cannot find %q in PATH: %w", wrappedBin, err)
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
