package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type managedWebSearchState string

const (
	managedWebSearchReady       managedWebSearchState = "installed"
	managedWebSearchUnavailable managedWebSearchState = "unavailable"
	managedWebSearchBroken      managedWebSearchState = "broken"
	managedWebSearchPackageName                       = "@void-code/pi-web-access"
	managedWebSearchMarker                            = "VC-10 managed void-codex seam v1"
)

var renameManagedWebSearchPath = os.Rename

func managedWebSearchPackagePath() string {
	return filepath.Join(piAgentDir(), "void-code", "pi-web-access-0.13.0-void.1")
}

func reconcileManagedWebSearch(eligible bool) (managedWebSearchState, error) {
	if piAgentDir() == "" {
		return managedWebSearchBroken, fmt.Errorf("cannot resolve Pi configuration directory")
	}
	disabled := isFalse(os.Getenv("VC_PI_MANAGED_WEB_SEARCH"))
	path := managedWebSearchPackagePath()
	if disabled {
		if err := removeManagedWebSearchPackage(path); err != nil {
			return managedWebSearchBroken, err
		}
		if err := reconcileManagedPackageSetting(path, false); err != nil {
			return managedWebSearchBroken, err
		}
		return managedWebSearchUnavailable, nil
	}
	if !eligible {
		// Ordinary ineligibility or unknown authority must not destructively
		// change an installation owned by vc. Explicit opt-out above is the sole
		// deregistration/removal operation.
		return managedWebSearchUnavailable, nil
	}

	current, foreign, err := inspectManagedWebSearchPackage(path)
	if err != nil {
		return managedWebSearchBroken, err
	}
	if foreign {
		return managedWebSearchBroken, fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	if !current {
		if err := installManagedWebSearchPackage(path); err != nil {
			return managedWebSearchBroken, err
		}
	}
	if err := reconcileManagedPackageSetting(path, true); err != nil {
		return managedWebSearchBroken, err
	}
	return managedWebSearchReady, nil
}

func inspectManagedWebSearchPackage(path string) (current, foreign bool, err error) {
	data, err := os.ReadFile(filepath.Join(path, "package.json"))
	if os.IsNotExist(err) {
		if _, statErr := os.Stat(path); statErr == nil {
			return false, true, nil
		}
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read managed web-search package: %w", err)
	}
	var pkg struct {
		Name, Version string
		VoidCodeFork  struct{ Patch string } `json:"voidCodeFork"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false, true, nil
	}
	owned := pkg.Name == managedWebSearchPackageName && pkg.VoidCodeFork.Patch == managedWebSearchMarker
	if !owned {
		return false, true, nil
	}
	_, depErr := os.Stat(filepath.Join(path, "node_modules", "@mozilla", "readability", "package.json"))
	return pkg.Version == "0.13.0-void.1" && depErr == nil, false, nil
}

func installManagedWebSearchPackage(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create managed package parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".pi-web-access-stage-*")
	if err != nil {
		return fmt.Errorf("stage managed web-search package: %w", err)
	}
	defer os.RemoveAll(stage)
	root := "embed/pi-web-access-0.13.0"
	err = fs.WalkDir(piWebAccessFork, root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, name)
		if rel == "." {
			return nil
		}
		dst := filepath.Join(stage, rel)
		if entry.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		data, readErr := piWebAccessFork.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dst, data, 0600)
	})
	if err != nil {
		return fmt.Errorf("copy managed web-search fork: %w", err)
	}
	if err := installManagedWebSearchDependencies(stage); err != nil {
		return err
	}
	if _, foreign, err := inspectManagedWebSearchPackage(path); err != nil {
		return err
	} else if foreign {
		return fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	backup := ""
	if _, err := os.Stat(path); err == nil {
		backupDir, err := os.MkdirTemp(parent, ".pi-web-access-backup-*")
		if err != nil {
			return fmt.Errorf("prepare managed web-search rollback: %w", err)
		}
		if err := os.Remove(backupDir); err != nil {
			return fmt.Errorf("prepare managed web-search rollback: %w", err)
		}
		backup = backupDir
		if err := renameManagedWebSearchPath(path, backup); err != nil {
			return fmt.Errorf("stage prior managed web-search package: %w", err)
		}
	}
	if err := renameManagedWebSearchPath(stage, path); err != nil {
		if backup != "" {
			if rollbackErr := renameManagedWebSearchPath(backup, path); rollbackErr != nil {
				return fmt.Errorf("publish managed web-search package: %w (rollback failed: %v)", err, rollbackErr)
			}
		}
		return fmt.Errorf("publish managed web-search package: %w", err)
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

func removeManagedWebSearchPackage(path string) error {
	_, foreign, err := inspectManagedWebSearchPackage(path)
	if err != nil {
		return err
	}
	if foreign {
		return fmt.Errorf("managed web-search path %s is not owned by void-code", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove managed web-search package: %w", err)
	}
	return nil
}

// reconcileManagedPackageSetting adds or removes the managed package path in
// settings.json's "packages", and does nothing at all when the file already
// says what it should.
//
// It owns one key and nothing else: the read, the lock and the atomic write are
// updatePiSettings's, so a user's file mode and the other writer's keys survive
// a change here the same way they survive a change there. The error a mutator
// cannot return travels out in mutateErr.
func reconcileManagedPackageSetting(packagePath string, present bool) error {
	var mutateErr error
	err := updatePiSettings(func(settings map[string]any) bool {
		raw, exists := settings["packages"]
		packages := []any{}
		if exists {
			var ok bool
			packages, ok = raw.([]any)
			if !ok {
				mutateErr = fmt.Errorf("Pi settings packages is not an array")
				return false
			}
		}
		out := make([]any, 0, len(packages)+1)
		matches := 0
		for _, item := range packages {
			if source, ok := item.(string); ok && source == packagePath {
				matches++
				if !present || matches > 1 {
					continue
				}
			}
			out = append(out, item)
		}
		if present && matches == 0 {
			out = append(out, packagePath)
		}
		if !present && !exists {
			return false
		}
		if (present && matches == 1) || (!present && matches == 0) {
			return false
		}
		settings["packages"] = out
		return true
	})
	if mutateErr != nil {
		return mutateErr
	}
	return err
}

func inspectManagedPackageSetting(packagePath string) (bool, error) {
	data, err := os.ReadFile(piSettingsPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read Pi settings: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, fmt.Errorf("parse Pi settings: %w", err)
	}
	raw, ok := settings["packages"]
	if !ok {
		return false, nil
	}
	packages, ok := raw.([]any)
	if !ok {
		return false, fmt.Errorf("Pi settings packages is not an array")
	}
	matches := 0
	for _, item := range packages {
		if source, ok := item.(string); ok && source == packagePath {
			matches++
		}
	}
	return matches == 1, nil
}
