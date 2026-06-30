// Package codexbin resolves the OpenAI Codex CLI binary and install guidance.
package codexbin

import (
	"fmt"
	"os/exec"
)

// Resolve returns the resolved path to the codex binary on PATH.
func Resolve() (string, error) {
	p, err := exec.LookPath("codex")
	if err != nil {
		return "", err
	}
	return p, nil
}

// IsInstalled reports whether codex is reachable on PATH.
func IsInstalled() bool {
	_, err := Resolve()
	return err == nil
}

// InstallInstructions returns copy-pasteable OpenAI Codex CLI install guidance.
func InstallInstructions() string {
	return "Install OpenAI Codex CLI:\n\n" +
		"  npm install -g @openai/codex\n\n" +
		"Then restart your terminal if `codex` is still not on PATH."
}

// MissingMessage returns a concise missing-binary message plus instructions.
func MissingMessage() string {
	return fmt.Sprintf("codex CLI not found — install OpenAI Codex before selecting the Codex harness\n%s", InstallInstructions())
}
