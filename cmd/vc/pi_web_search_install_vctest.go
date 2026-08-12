//go:build vctestfixture

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// This seam exists only in explicitly tagged test binaries. It completes a
// preinstalled fixture copied by the smoke test; normal vc builds do not
// compile this file and always execute the production npm ci implementation.
var installManagedWebSearchDependencies = func(dir string) error {
	fixture := os.Getenv("VC_TEST_MANAGED_WEB_NODE_MODULES")
	if fixture == "" {
		return fmt.Errorf("test managed web fixture is not configured")
	}
	return os.Symlink(fixture, filepath.Join(dir, "node_modules"))
}
