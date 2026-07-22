package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type managedWebSearchState string

const (
	managedWebSearchReady       managedWebSearchState = "installed"
	managedWebSearchUnavailable managedWebSearchState = "unavailable"
	managedWebSearchBroken      managedWebSearchState = "broken"
	managedWebSearchPackageName                       = "@void-code/pi-web-access"
	managedWebSearchMarker                            = "VC-10 managed void-codex seam v1"
)

var installManagedWebSearchDependencies = func(dir string) error {
	cmd := exec.Command("npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("install pinned web-search dependencies: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

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
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("replace managed web-search package: %w", err)
	}
	if err := os.Rename(stage, path); err != nil {
		return fmt.Errorf("publish managed web-search package: %w", err)
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

func reconcileManagedPackageSetting(packagePath string, present bool) error {
	path := piSettingsPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data = []byte("{}")
	} else if err != nil {
		return fmt.Errorf("read Pi settings: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("parse Pi settings without modifying it: %w", err)
	}
	raw, exists := settings["packages"]
	packages := []any{}
	if exists {
		var ok bool
		packages, ok = raw.([]any)
		if !ok {
			return fmt.Errorf("Pi settings packages is not an array")
		}
	}
	out := make([]any, 0, len(packages)+1)
	found := false
	for _, item := range packages {
		if source, ok := item.(string); ok && source == packagePath {
			found = true
			if !present {
				continue
			}
		}
		out = append(out, item)
	}
	if present && !found {
		out = append(out, packagePath)
	}
	if !present && !exists {
		return nil
	}
	if found == present {
		return nil
	}
	settings["packages"] = out
	next, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	next = append(next, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(next)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func checkManagedWebSearch() checkResult {
	if isFalse(os.Getenv("VC_PI_MANAGED_WEB_SEARCH")) {
		return checkResult{name: "web search", status: "!", message: "web search: unavailable (opted out)"}
	}
	current, foreign, err := inspectManagedWebSearchPackage(managedWebSearchPackagePath())
	if err != nil || foreign || !current {
		return checkResult{name: "web search", status: "✗", message: "web search: broken or not installed", guidance: []string{"run `vc doctor --fix` to safely reconcile the void-code-owned package"}, fix: func() error { _, err := reconcileManagedWebSearch(true); return err }}
	}
	return checkResult{name: "web search", status: "✓", message: "web search: installed (web_search, fetch_content, get_search_content)"}
}
