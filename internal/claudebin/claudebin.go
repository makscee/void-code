// Package claudebin resolves the claude CLI binary and surfaces clear install
// guidance when it is missing from PATH.
//
// vc is a wrapper over claude; if claude is absent the wrapper cannot function.
// This package provides the canonical pre-flight check used at launch and in
// `vc doctor` so the user sees a clear, actionable message instead of a raw
// exec/cobra error.
//
// Install-mechanism decision (VCD-58):
//   Guided per-OS instructions rather than auto-install. Auto-install is
//   fragile on Windows (npm may not be on PATH in all shell contexts, user
//   may prefer a different install path like the official install script),
//   and silently running `npm install -g` without user consent goes against
//   the "no silent network install" requirement. Per-OS copy-pasteable
//   instructions are reliable on every platform, require no network from vc
//   itself, and keep the user fully informed.
package claudebin

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Resolve returns the resolved absolute path to the claude binary on PATH.
// On Windows it accepts both "claude" (no extension) and "claude.cmd" / "claude.exe"
// because exec.LookPath already handles PATHEXT on Windows.
// Returns an error (classifiable via IsNotFoundErr) when claude is absent.
func Resolve() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	return p, nil
}

// IsInstalled reports whether the claude binary is reachable on PATH.
func IsInstalled() bool {
	_, err := Resolve()
	return err == nil
}

// IsNotFoundErr returns true when err signals that the binary was not found in
// PATH (as opposed to the binary running and exiting non-zero).
// Distinguishing these two is important: a *exec.ExitError means claude ran
// and failed; any other error (including exec.ErrNotFound and *os.PathError)
// means the binary was never found.
func IsNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false // binary ran, exited non-zero — NOT a path miss
	}
	return true
}

// InstallInstructions returns per-OS copy-pasteable install guidance for the
// canonical Claude Code installation paths.
//
// Primary method (all platforms): npm global install.
// Secondary (macOS/Linux): official install script.
// Windows note: after npm install, restart the terminal so claude.cmd lands on PATH.
func InstallInstructions() string {
	switch runtime.GOOS {
	case "windows":
		return "Install Claude Code (choose one):\n\n" +
			"  Option 1 — npm (recommended):\n" +
			"    npm install -g @anthropic-ai/claude-code\n" +
			"    (restart your terminal afterwards so claude.cmd appears on PATH)\n\n" +
			"  Option 2 — winget:\n" +
			"    winget install Anthropic.Claude\n\n" +
			"  Docs: https://docs.anthropic.com/en/docs/claude-code/setup"
	case "darwin":
		return "Install Claude Code (choose one):\n\n" +
			"  Option 1 — npm (recommended):\n" +
			"    npm install -g @anthropic-ai/claude-code\n\n" +
			"  Option 2 — official install script:\n" +
			"    curl -fsSL https://claude.ai/install.sh | sh\n\n" +
			"  Docs: https://docs.anthropic.com/en/docs/claude-code/setup"
	default: // linux and others
		return "Install Claude Code (choose one):\n\n" +
			"  Option 1 — npm (recommended):\n" +
			"    npm install -g @anthropic-ai/claude-code\n\n" +
			"  Option 2 — official install script:\n" +
			"    curl -fsSL https://claude.ai/install.sh | sh\n\n" +
			"  Docs: https://docs.anthropic.com/en/docs/claude-code/setup"
	}
}

// MissingMessage returns a single-line summary for use in error output.
// The full per-OS instructions are in InstallInstructions.
func MissingMessage() string {
	return fmt.Sprintf("claude CLI not found — vc is a wrapper over claude and requires it to be installed\n%s", InstallInstructions())
}
