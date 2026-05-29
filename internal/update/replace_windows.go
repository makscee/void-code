//go:build windows

package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicReplace replaces dest with data on Windows.
//
// Windows locks a running executable so os.Rename(tmp, dest) fails directly.
// We use the same approach as install.ps1:
//  1. Write new binary to a temp file.
//  2. Rename dest → dest+".old"  (allowed while the process is running).
//  3. Rename tmp → dest.
//  4. Best-effort remove the .old file (next launch also cleans it up).
//
// If step 3 fails we attempt to restore the .old file so the user is never
// left without a working binary.
func atomicReplace(dest string, data []byte) error {
	dir := filepath.Dir(dest)

	// Step 1: write new binary to a temp file in the same directory.
	tmp, err := os.CreateTemp(dir, ".vc-update-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	tmpOK := false
	defer func() {
		if !tmpOK {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	// Step 2: rename running exe → .old (Windows allows this while it runs).
	oldPath := dest + ".old"
	// Remove any leftover .old from a previous update attempt.
	_ = os.Remove(oldPath)
	if err := os.Rename(dest, oldPath); err != nil {
		return fmt.Errorf("rename current to .old: %w", err)
	}

	// Step 3: move new binary into place.
	if err := os.Rename(tmpPath, dest); err != nil {
		// Attempt to restore the original binary so the user is not stranded.
		_ = os.Rename(oldPath, dest)
		return fmt.Errorf("rename new binary into place: %w", err)
	}
	tmpOK = true // temp file is now dest; don't double-remove

	// Step 4: best-effort cleanup of the .old file.
	_ = os.Remove(oldPath)

	return nil
}

// CleanOldBinary removes the leftover .old file (if any) from a previous
// Windows self-update.  Call once at startup, before any update check.
func CleanOldBinary() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(exe + ".old")
}
