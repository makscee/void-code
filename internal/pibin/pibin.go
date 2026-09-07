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

// vcDownloadPage is where a person gets an installed copy of VC. A bare binary
// taken from the release page cannot install the runtime itself — vc resolves
// the managed runtime and never provisions it — so the address has to travel
// inside the message. 27.08 an external tester ran vc-darwin-arm64 from the
// release page and was told to re-run an installer she had never run.
const vcDownloadPage = "https://auth.makscee.ru/download"

// installMarker is the directory a VC installation owns. Its presence is what
// separates "VC was never installed here" from "VC is installed and its runtime
// is broken": the runtime below it is the part reported missing.
const installMarker = ".void-code"

// MissingMessage returns the missing-runtime message for the current user.
func MissingMessage() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Without a home directory there is no installation to speak of, and the
		// bare-binary text is the one that holds true either way.
		return MissingMessageFor("")
	}
	return MissingMessageFor(home)
}

// MissingMessageFor returns the missing-runtime message for the installation
// rooted at installRoot. The two situations it separates need opposite actions:
// a binary that was never installed has to be installed, an installation whose
// runtime is gone has to be repaired. One text for both sends half the readers
// nowhere.
func MissingMessageFor(installRoot string) string {
	if installRoot == "" || !hasManagedInstall(installRoot) {
		return fmt.Sprintf(
			"VC is not installed on this computer — this is only the downloaded vc file, "+
				"without the managed Pi runtime it needs to work.\n"+
				"Install VC from %s and start it from there.",
			vcDownloadPage)
	}
	return fmt.Sprintf(
		"VC is installed on this computer, but its managed Pi runtime is missing or damaged: %s\n"+
			"Install VC again from %s to put it back.",
		filepath.Join(installRoot, installMarker, "runtime"), vcDownloadPage)
}

func hasManagedInstall(installRoot string) bool {
	info, err := os.Stat(filepath.Join(installRoot, installMarker))
	return err == nil && info.IsDir()
}
