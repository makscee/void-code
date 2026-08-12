//go:build vctestfixture

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// This seam exists only in explicitly tagged test binaries. It copies a
// preinstalled fixture into the staged package; normal vc builds do not
// compile this file and always execute the production npm ci implementation.
var installManagedWebSearchDependencies = func(dir string) error {
	fixture := os.Getenv("VC_TEST_MANAGED_WEB_NODE_MODULES")
	if fixture == "" {
		return fmt.Errorf("test managed web fixture is not configured")
	}
	destination := filepath.Join(dir, "node_modules")
	return filepath.WalkDir(fixture, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(fixture, name)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		contents, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, 0600)
	})
}
