// Package ccjson pre-seeds ~/.claude.json before the first claude spawn so
// Claude Code's first-run onboarding (theme picker + login) is skipped on
// fresh machines.
//
// If the file is absent it is written from scratch. If the file is present,
// EnsureDefaults merges the two mandatory keys without touching any other
// content. Unparseable files are left untouched and an error is returned.
package ccjson

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// seed is the full JSON written when ~/.claude.json does not exist at all.
const seed = `{"hasCompletedOnboarding":true,"theme":"dark"}` + "\n"

// EnsureDefaults ensures that path contains at least
// hasCompletedOnboarding:true and a theme value.
//
//   - Absent  → write full seed (mode 0600).
//   - Present + valid JSON → merge keys, write back atomically only if changed.
//   - Present + unparseable JSON → return error, do NOT clobber.
//
// Non-fatal write errors are returned; callers should warn and continue.
func EnsureDefaults(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// File is absent: write the full seed.
		return writeAtomic(path, []byte(seed))
	}
	if err != nil {
		return err
	}

	// File exists — parse it.
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccjson: %s contains invalid JSON (leaving untouched): %w", path, err)
	}

	changed := false

	// Ensure hasCompletedOnboarding is true (overwrite false or missing).
	if v, ok := obj["hasCompletedOnboarding"]; !ok || v != true {
		obj["hasCompletedOnboarding"] = true
		changed = true
	}

	// Ensure theme exists; add "dark" only if missing — never overwrite user's choice.
	if _, ok := obj["theme"]; !ok {
		obj["theme"] = "dark"
		changed = true
	}

	if !changed {
		return nil
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("ccjson: marshal updated config: %w", err)
	}
	out = append(out, '\n')
	return writeAtomic(path, out)
}

// writeAtomic writes data to path using a temp-file + rename so a crash
// mid-write never leaves a truncated file. Mode is always 0600.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp")
	if err != nil {
		return fmt.Errorf("ccjson: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Clean up temp on any failure path.
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("ccjson: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ccjson: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ccjson: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ccjson: rename temp: %w", err)
	}
	ok = true
	return nil
}
