// Package pibin resolves VC's managed Pi entrypoint and install guidance.
package pibin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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

// managedNodePathForOS names the Node executable VC installs beside its managed
// Pi. npm's Windows distribution keeps node.exe at the root of the extracted
// archive; the unix tarballs keep it under bin/. Both are what the desktop
// assembly scripts record in the runtime manifest as node/node.exe and
// node/bin/node.
func managedNodePathForOS(home, goos string) string {
	if goos == "windows" {
		return filepath.Join(home, ".void-code", "runtime", "node", "node.exe")
	}
	return filepath.Join(home, ".void-code", "runtime", "node", "bin", "node")
}

func managedNodePath(home string) string { return managedNodePathForOS(home, runtime.GOOS) }

// managedNodeRuntimePath is the root of the tree provisioned only by the
// bundled-runtime installer. install.sh's legacy channel deliberately omits
// this whole tree and uses the machine Node instead.
func managedNodeRuntimePath(home string) string {
	return filepath.Join(home, ".void-code", "runtime", "node")
}

// ErrBundledNodeUnprovisioned is returned directly when the entire managed
// runtime/node tree is absent. Its PathError form preserves the historical
// os.IsNotExist behavior for callers that only need missing-file diagnostics;
// callers deciding whether PATH fallback is safe must use errors.Is with this
// sentinel, rather than treating every missing inner component as legacy.
var ErrBundledNodeUnprovisioned = &os.PathError{
	Op:   "resolve bundled Node runtime",
	Path: ".void-code/runtime/node",
	Err:  syscall.ENOENT,
}

// ResolveNode returns the absolute path of the Node executable VC installs with
// its managed Pi, on the same terms Resolve uses for the Pi entrypoint: a fixed
// place under the canonical home, never PATH. Pi starts through
// `#!/usr/bin/env node`, so this is the binary VC means to run it, and it is
// handed a VC token — a PATH-selected or symlink-redirected node would receive
// that token exactly as the wrong Pi would.
//
// An absent Node is a normal state, not a broken install: install.sh provisions
// only runtime/pi and npm-installs it with the Node already on the machine. The
// error says which, and it is for the caller to decide what a missing bundled
// Node means for the environment it is building.
func ResolveNode() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve VC home: %w", err)
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("canonicalize VC home: %w", err)
	}
	path := managedNodePath(canonicalHome)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("bundled Node path is not absolute")
	}
	if err := bundledNodeTreeProvisioned(canonicalHome); err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(canonicalHome, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("bundled Node is not a regular file: %s", path)
	}
	if !nodeIsExecutable(path) {
		return "", fmt.Errorf("bundled Node is not executable: %s", path)
	}
	return path, nil
}

// bundledNodeTreeProvisioned distinguishes the legacy absence of the complete
// runtime/node tree from a broken component inside a tree VC did provision.
// Check each parent for symlinks before classifying ENOENT, so a dangling
// redirected runtime remains a rejection rather than a legacy fallback.
func bundledNodeTreeProvisioned(home string) error {
	root := managedNodeRuntimePath(home)
	if err := rejectSymlinkComponents(home, filepath.Dir(root)); err != nil {
		if os.IsNotExist(err) {
			return ErrBundledNodeUnprovisioned
		}
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrBundledNodeUnprovisioned
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed Pi path contains symlink component: %s", root)
	}
	return nil
}

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
