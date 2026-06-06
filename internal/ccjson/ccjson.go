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
	"strings"
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

// TrustKeys returns the set of ~/.claude.json "projects" keys that should be
// marked trusted for the working directory dir. It always includes dir as-is
// and, when it differs (i.e. on Windows, where dir uses backslashes), the
// forward-slash form too — Claude Code's path normalization for the projects
// map differs across versions, so seeding both variants is the safe hedge.
// Returns nil for an empty dir.
func TrustKeys(dir string) []string {
	if dir == "" {
		return nil
	}
	keys := []string{dir}
	// GOOS-independent backslash→slash (mirrors ccsettings.ForwardSlash): a
	// Windows path "C:\Users\u\p" also gets its "C:/Users/u/p" variant, since
	// CC's projects-map key normalization differs across versions. filepath.ToSlash
	// is unsuitable here — it only swaps the *host* OS separator, so it would be a
	// no-op when this binary is built/run anywhere the separator is already '/'.
	if s := strings.ReplaceAll(dir, `\`, "/"); s != dir {
		keys = append(keys, s)
	}
	return keys
}

// EnsureFolderTrust marks each dir in dirs as a trusted folder in the
// ~/.claude.json at path (projects.<dir>.hasTrustDialogAccepted = true), so
// Claude Code skips the folder-trust dialog and actually LOADS the user's
// ~/.claude/settings.json on the first launch in a brand-new folder.
//
// Why this is load-bearing: CC does not load user settings — including
// permissions.defaultMode=bypassPermissions and any PreToolUse hooks — until
// the working directory is trusted. A fresh project folder is untrusted, so CC
// withholds those settings, drops into `auto` permission mode, and runs its
// server-side safety classifier on shell commands. That classifier is a model
// sub-call the relay backend cannot serve, which surfaces to the user as
// "<model> not accessible" the moment CC tries to run a script it created. On
// Windows the trust-acceptance write is additionally broken upstream
// (claude-code#36403 / #50858), so the dialog never persists and the failure
// recurs every session. Pre-seeding trust here is the reliable cross-platform
// fix; callers pass ccjson.TrustKeys(cwd)... to cover both path variants.
//
// Non-clobbering + idempotent, mirroring EnsureDefaults:
//   - Absent file           → write a fresh doc with the onboarding seed + trust.
//   - Present + valid JSON   → merge trust into projects.<dir>, keep everything.
//   - Already trusted        → no-op (no write).
//   - Present + invalid JSON → return error, do NOT clobber.
func EnsureFolderTrust(path string, dirs ...string) error {
	// Drop empty entries defensively.
	clean := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != "" {
			clean = append(clean, d)
		}
	}
	if len(clean) == 0 {
		return nil
	}

	var obj map[string]interface{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Seed onboarding keys too so a standalone call still skips first-run.
		obj = map[string]interface{}{
			"hasCompletedOnboarding": true,
			"theme":                  "dark",
		}
	} else if err != nil {
		return err
	} else if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccjson: %s contains invalid JSON (leaving untouched): %w", path, err)
	}

	projects, _ := obj["projects"].(map[string]interface{})
	if projects == nil {
		projects = map[string]interface{}{}
		obj["projects"] = projects
	}

	changed := false
	for _, dir := range clean {
		entry, _ := projects[dir].(map[string]interface{})
		if entry == nil {
			entry = map[string]interface{}{}
			projects[dir] = entry
		}
		if v, ok := entry["hasTrustDialogAccepted"]; !ok || v != true {
			entry["hasTrustDialogAccepted"] = true
			changed = true
		}
	}

	if !changed {
		return nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("ccjson: marshal updated config: %w", err)
	}
	return writeAtomic(path, append(out, '\n'))
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
