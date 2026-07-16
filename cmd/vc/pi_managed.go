package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const managedPiExtensionMarker = "// void-code-managed-pi-extension:v1\n"

// reconcileManagedPiExtension installs the versioned provider in Pi's standard
// user extension directory. An empty path means the user opted out. Existing
// files without our ownership marker are left untouched.
func reconcileManagedPiExtension() (string, error) {
	dir := piAgentDir()
	if dir == "" {
		return "", fmt.Errorf("cannot resolve Pi configuration directory")
	}
	path := filepath.Join(dir, "extensions", "void-code.ts")
	disabled := isFalse(os.Getenv("VC_PI_MANAGED_PROVIDER"))

	existing, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return "", fmt.Errorf("read managed Pi extension: %w", err)
	}
	owned := err == nil && strings.HasPrefix(string(existing), managedPiExtensionMarker)
	if disabled {
		if err == nil && !owned {
			return "", fmt.Errorf("Pi extension conflict at %s: file is not owned by void-code", path)
		}
		if owned {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("remove managed Pi extension: %w", err)
			}
		}
		return "", nil
	}
	if err == nil {
		if !owned {
			return "", fmt.Errorf("Pi extension conflict at %s: file is not owned by void-code", path)
		}
		if string(existing) == piVoidCodexExtensionSource {
			return path, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create Pi extension directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".void-code.ts.tmp-*")
	if err != nil {
		return "", fmt.Errorf("stage managed Pi extension: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(piVoidCodexExtensionSource)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("stage managed Pi extension: %w", err)
	}
	if missing {
		// Link publishes the first install atomically without replacing a file
		// another process may have created after our ownership check.
		if err := os.Link(tmp, path); err != nil {
			return "", fmt.Errorf("install managed Pi extension: %w", err)
		}
		return path, nil
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", fmt.Errorf("update managed Pi extension: %w", err)
	}
	return path, nil
}

func piAgentDir() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

func isFalse(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}
