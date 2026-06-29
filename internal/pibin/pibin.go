// Package pibin resolves the Pi coding-agent binary and install guidance.
package pibin

import (
	"fmt"
	"os/exec"
)

// Resolve returns the resolved path to the pi binary on PATH.
func Resolve() (string, error) {
	p, err := exec.LookPath("pi")
	if err != nil {
		return "", err
	}
	return p, nil
}

// IsInstalled reports whether pi is reachable on PATH.
func IsInstalled() bool {
	_, err := Resolve()
	return err == nil
}

// InstallInstructions returns copy-pasteable Pi install guidance.
func InstallInstructions() string {
	return "Install Pi coding agent:\n\n" +
		"  npm install -g @earendil-works/pi-coding-agent\n\n" +
		"Then restart your terminal if `pi` is still not on PATH."
}

// MissingMessage returns a concise missing-binary message plus instructions.
func MissingMessage() string {
	return fmt.Sprintf("pi CLI not found — install Pi before selecting the Pi harness\n%s", InstallInstructions())
}
