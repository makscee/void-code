// Package ccjson pre-seeds ~/.claude.json before the first claude spawn so
// Claude Code's first-run onboarding (theme picker + login) is skipped on
// fresh machines. The file is written only when it does not already exist —
// an existing file is never mutated.
package ccjson

import (
	"os"
)

// seed is the minimal JSON written to a fresh ~/.claude.json.
// Keep it small: only the two fields CC checks during first-run.
const seed = `{"hasCompletedOnboarding":true,"theme":"dark"}` + "\n"

// EnsureDefaults writes the minimal seed to path if the file does not exist.
// If the file is already present (any content), EnsureDefaults is a no-op.
// Non-fatal write errors are returned; callers should warn and continue.
func EnsureDefaults(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		// File exists — leave it untouched.
		return nil
	}
	if !os.IsNotExist(err) {
		// Unexpected stat error — surface it so callers can warn.
		return err
	}

	// File is absent: write the seed with mode 0600 (private, like other CC files).
	return os.WriteFile(path, []byte(seed), 0600)
}
