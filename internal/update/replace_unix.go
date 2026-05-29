//go:build !windows

package update

import (
	"os"
	"path/filepath"
)

// atomicReplace writes data to a temp file in the same directory as dest,
// then renames it over dest.  This is atomic on most Unix filesystems.
func atomicReplace(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".vc-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	// Ensure temp file is removed on failure.
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return err
	}
	ok = true
	return nil
}
