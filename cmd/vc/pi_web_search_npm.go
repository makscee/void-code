package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// managedWebSearchNpmWaitDelay is the WaitDelay on npm's exec.Cmd: once npm
// has exited or been cancelled, how long vc waits for anything npm started to
// let go of its output pipe before giving up on it.
var managedWebSearchNpmWaitDelay = 5 * time.Second

// npmManagedWebSearchInstall is the production npm step, compiled into every
// build so tagged test binaries can run it too. npm is resolved from PATH. On
// cancellation the whole process tree is killed where the platform allows it
// (see configureNpmProcessTree), so a grandchild cannot keep the stage busy or
// hold the output pipe; WaitDelay bounds the rest.
func npmManagedWebSearchInstall(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = dir
	cmd.WaitDelay = managedWebSearchNpmWaitDelay
	configureNpmProcessTree(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("install pinned web-search dependencies: %w", ctxErr)
		}
		return fmt.Errorf("install pinned web-search dependencies: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
