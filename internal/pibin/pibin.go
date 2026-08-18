// Package pibin resolves VC's managed Pi entrypoint and install guidance.
package pibin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const managedPiRelativePath = ".void-code/runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"

// managedPiPathForOS names the package artifact installed and launched on each
// platform. npm's Windows package entrypoint is its generated .cmd shim; Unix
// launches the package's executable cli.js directly.
func managedPiPathForOS(home, goos string) string {
	if goos == "windows" {
		return filepath.Join(home, ".void-code", "runtime", "pi", "node_modules", ".bin", "pi.cmd")
	}
	return filepath.Join(home, filepath.FromSlash(managedPiRelativePath))
}

func managedPiPath(home string) string { return managedPiPathForOS(home, runtime.GOOS) }

// Resolve returns VC's absolute, installed Pi entrypoint. It intentionally does
// not consult PATH: a PATH-selected Pi must not receive VC credentials.
//
// This is not provenance verification and is not race-safe against the account
// owner: ~/.void-code and the token are both in that user's trust boundary. The
// component checks only reject accidental or lower-authority symlink redirection.
func Resolve() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve VC home: %w", err)
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("canonicalize VC home: %w", err)
	}
	path := managedPiPath(canonicalHome)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("managed Pi path is not absolute")
	}
	if err := rejectSymlinkComponents(canonicalHome, path); err != nil {
		return "", err
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

func rejectSymlinkComponents(home, path string) error {
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("managed Pi path escapes canonical home: %s", path)
	}
	current := home
	for _, component := range splitPath(rel) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed Pi path contains symlink component: %s", current)
		}
	}
	return nil
}

func splitPath(path string) []string {
	var components []string
	for path != "." && path != "" {
		component := filepath.Base(path)
		components = append([]string{component}, components...)
		path = filepath.Dir(path)
	}
	return components
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
