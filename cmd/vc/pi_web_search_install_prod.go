//go:build !vctestfixture

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

var installManagedWebSearchDependencies = func(dir string) error {
	cmd := exec.Command("npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install pinned web-search dependencies: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
