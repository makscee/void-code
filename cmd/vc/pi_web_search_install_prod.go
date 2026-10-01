//go:build !vctestfixture

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// installManagedWebSearchDependencies runs npm in the staged package. ctx is
// cancelled when vc gives up on the install after Pi has exited; WaitDelay
// bounds how long a killed npm's children may keep its output pipe open.
var installManagedWebSearchDependencies = func(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("install pinned web-search dependencies: %w", ctxErr)
		}
		return fmt.Errorf("install pinned web-search dependencies: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
