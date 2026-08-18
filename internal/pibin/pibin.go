// Package pibin resolves VC's managed Pi entrypoint and install guidance.
package pibin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const managedPiRelativePath = ".void-code/runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"

func managedPiPath(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", ".bin", "pi.cmd")
	}
	return filepath.Join(home, filepath.FromSlash(managedPiRelativePath))
}

// Resolve returns VC's absolute, managed Pi entrypoint. It intentionally does
// not consult PATH: a PATH-selected executable would receive VC credentials.
func Resolve() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve VC home: %w", err)
	}
	path := managedPiPath(home)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("managed Pi path is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("managed Pi entrypoint is not a regular file: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return "", fmt.Errorf("managed Pi entrypoint is not executable: %s", path)
	}
	return path, nil
}

// IsInstalled reports whether VC's managed Pi entrypoint is available.
func IsInstalled() bool {
	_, err := Resolve()
	return err == nil
}

// InstallInstructions returns copy-pasteable Pi install guidance.
func InstallInstructions() string {
	return "Re-run the VC installer to provision its managed Pi runtime."
}

// MissingMessage returns a concise missing-runtime message plus instructions.
func MissingMessage() string {
	return fmt.Sprintf("VC managed Pi runtime not found — Pi must be provisioned by VC\n%s", InstallInstructions())
}
