// Package ccsettings idempotently merges void-code's Claude Code settings
// (permission posture + statusLine) into ~/.claude/settings.json without
// clobbering existing user keys. Mirrors the ccjson.EnsureDefaults pattern:
// absent→write, present→merge, unparseable→error (never clobber), atomic
// temp+rename, mode 0600.
//
// VCD-53 — automode killed: vc no longer installs the DeepSeek-classifier
// PreToolUse hook. Instead it writes Claude Code's native allow-all permission
// posture (EnsureAllowAllPermissions) and removes any stale classifier hook
// from prior installs (RemoveHook). The classifier hook machinery below
// (EnsureHook / HookCmd / freshDoc / mergeHook / entry) is retained but no
// longer wired from main.go, so a proper automode can be reintroduced later.
package ccsettings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureHook ensures a single PreToolUse hook entry whose command == hookCmd
// (e.g. "/abs/path/vc hook") exists in the settings file at path.
//
//   - Absent      → write fresh file with only the hook entry.
//   - Present + valid JSON → merge only our entry; all other keys are preserved.
//   - Present + invalid JSON → return error, do NOT clobber.
//
// Idempotency key: a hook whose inner command string ends with " hook" is ours.
// On re-run with the same command → no-op. On re-run with a changed path (binary
// moved) → update in-place rather than append a duplicate.
func EnsureHook(path, hookCmd string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeAtomic(path, freshDoc(hookCmd))
	}
	if err != nil {
		return err
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}

	if mergeHook(obj, hookCmd) {
		out, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return fmt.Errorf("ccsettings: marshal: %w", err)
		}
		return writeAtomic(path, append(out, '\n'))
	}
	return nil // already present and correct — no write needed
}

// SettingsPath returns the canonical path to ~/.claude/settings.json.
// Uses os.UserHomeDir for cross-platform correctness (handles %USERPROFILE% on Windows).
func SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ccsettings: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// EnsureAllowAllPermissions installs Claude Code's native allow-all permission
// posture into the settings file at path (VCD-53). This makes CC run every tool
// without prompting and without any client-side classifier sub-call:
//
//	permissions.defaultMode                   = "bypassPermissions"
//	permissions.skipDangerousModePermissionPrompt = true
//
// "bypassPermissions" is CC's documented mode that skips every permission
// prompt (Claude Code settings docs, permissions.defaultMode). On its own it
// shows a one-time confirmation before entering bypass mode;
// skipDangerousModePermissionPrompt suppresses that confirm. The latter is
// "ignored when set in project settings" — but vc writes to the USER-scope
// ~/.claude/settings.json (see SettingsPath), where it applies, so the result
// is a fully silent allow-all with no PreToolUse hook required.
//
// Cross-platform: this writes only JSON scalars (no executable path), so there
// is no Windows backslash/forward-slash concern.
//
// Non-clobbering + idempotent, mirroring EnsureHook/EnsureStatusLine:
//   - Absent file              → write fresh with just the permissions block.
//   - Present + valid JSON      → merge our two keys into permissions, keep the rest.
//   - Already correct           → no-op (no write).
//   - Present + invalid JSON    → return error, do NOT clobber.
func EnsureAllowAllPermissions(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeAtomic(path, freshPermissionsDoc())
	}
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}
	if mergeAllowAllPermissions(obj) {
		out, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return fmt.Errorf("ccsettings: marshal: %w", err)
		}
		return writeAtomic(path, append(out, '\n'))
	}
	return nil // already correct — no write needed
}

// allowAllPermissions returns the two keys vc sets inside "permissions".
func allowAllPermissions() (defaultMode string, skipPrompt bool) {
	return "bypassPermissions", true
}

// freshPermissionsDoc builds a minimal settings.json with only our permissions.
func freshPermissionsDoc() []byte {
	mode, skip := allowAllPermissions()
	doc := map[string]any{
		"permissions": map[string]any{
			"defaultMode":                       mode,
			"skipDangerousModePermissionPrompt": skip,
		},
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}

// mergeAllowAllPermissions sets our two keys inside obj["permissions"] in place,
// preserving any other permissions keys (allow/ask/deny/etc). Returns true if
// obj changed (write needed).
func mergeAllowAllPermissions(obj map[string]any) bool {
	mode, skip := allowAllPermissions()
	pm, _ := obj["permissions"].(map[string]any)
	if pm == nil {
		pm = map[string]any{}
		obj["permissions"] = pm
	}
	changed := false
	if pm["defaultMode"] != mode {
		pm["defaultMode"] = mode
		changed = true
	}
	if pm["skipDangerousModePermissionPrompt"] != skip {
		pm["skipDangerousModePermissionPrompt"] = skip
		changed = true
	}
	return changed
}

// RemoveHook strips any vc-installed classifier PreToolUse hook from the
// settings file at path (VCD-53 migration). Ownership key: a PreToolUse entry
// whose inner command ends with " hook" is ours. Foreign hooks are untouched.
//
//   - Absent file / no vc hook   → no-op (no write).
//   - Our hook present            → remove the entry (and empty PreToolUse / hooks
//     containers we leave in place, harmlessly empty).
//   - Present + invalid JSON      → return error, do NOT clobber.
func RemoveHook(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // nothing to clean up
	}
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}
	if removeOurHook(obj) {
		out, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return fmt.Errorf("ccsettings: marshal: %w", err)
		}
		return writeAtomic(path, append(out, '\n'))
	}
	return nil // no vc hook present — no write
}

// removeOurHook deletes any PreToolUse entry whose inner command ends with
// " hook" (ours). Returns true if obj changed (write needed).
func removeOurHook(obj map[string]any) bool {
	hooks, _ := obj["hooks"].(map[string]any)
	if hooks == nil {
		return false
	}
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) == 0 {
		return false
	}
	kept := make([]any, 0, len(pre))
	changed := false
	for _, raw := range pre {
		if isOurHookEntry(raw) {
			changed = true
			continue
		}
		kept = append(kept, raw)
	}
	if !changed {
		return false
	}
	hooks["PreToolUse"] = kept
	return true
}

// isOurHookEntry reports whether a PreToolUse entry is a vc classifier hook
// (any inner command ending with " hook").
func isOurHookEntry(raw any) bool {
	e, _ := raw.(map[string]any)
	if e == nil {
		return false
	}
	inner, _ := e["hooks"].([]any)
	for _, hraw := range inner {
		h, _ := hraw.(map[string]any)
		if h == nil {
			continue
		}
		if c, _ := h["command"].(string); strings.HasSuffix(c, " hook") {
			return true
		}
	}
	return false
}

// QuoteIfSpace wraps path in double-quotes if it contains a space character.
// Needed on Windows where Program Files paths have spaces.
func QuoteIfSpace(path string) string {
	if strings.Contains(path, " ") {
		return `"` + path + `"`
	}
	return path
}

// HookCmd builds the hookCmd string from the running binary's absolute path.
// Returns (<abs-path-or-quoted> + " hook").
func HookCmd(execPath string) string {
	return QuoteIfSpace(execPath) + " hook"
}

// entry builds a single PreToolUse hook entry map.
func entry(hookCmd string) map[string]any {
	return map[string]any{
		"matcher": "*",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": hookCmd,
			"timeout": 15,
		}},
	}
}

// freshDoc builds a minimal settings.json document with only our hook.
func freshDoc(hookCmd string) []byte {
	doc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{entry(hookCmd)},
		},
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}

// mergeHook modifies obj in-place to contain exactly one copy of our hook entry.
// Returns true if obj was changed (write needed), false if already correct.
func mergeHook(obj map[string]any, hookCmd string) bool {
	hooks, _ := obj["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		obj["hooks"] = hooks
	}

	pre, _ := hooks["PreToolUse"].([]any)

	// Look for an existing entry that is ours (inner command ends with " hook").
	for i, raw := range pre {
		e, _ := raw.(map[string]any)
		if e == nil {
			continue
		}
		inner, _ := e["hooks"].([]any)
		for _, hraw := range inner {
			h, _ := hraw.(map[string]any)
			if h == nil {
				continue
			}
			c, _ := h["command"].(string)
			if strings.HasSuffix(c, " hook") {
				if c == hookCmd {
					return false // already correct, no write needed
				}
				// Path changed (binary moved) — update in-place.
				pre[i] = entry(hookCmd)
				hooks["PreToolUse"] = pre
				return true
			}
		}
	}

	// Not found — append new entry.
	hooks["PreToolUse"] = append(pre, entry(hookCmd))
	return true
}

// StatusLineCmd builds the statusLine command string from the running binary's
// absolute path: "<abs-or-quoted-path> statusline".
// On Windows, callers MUST pass a forward-slash path (CC's Git-Bash/PowerShell runner
// strips backslashes) — see ForwardSlash below.
func StatusLineCmd(execPath string) string {
	return QuoteIfSpace(execPath) + " statusline"
}

// ForwardSlash converts backslashes to forward slashes for the statusLine
// command string on Windows (CC's Git-Bash runner strips backslashes).
// No-op on paths that already use '/'.
func ForwardSlash(p string) string { return strings.ReplaceAll(p, `\`, `/`) }

// EnsureStatusLine ensures the top-level "statusLine" entry points at slCmd
// (e.g. "/abs/vc statusline"), without clobbering a user's foreign statusLine.
//
//   - Absent statusLine            → write our entry.
//   - Present + ours (cmd ends " statusline"): same → no-op; moved → update.
//   - Present + FOREIGN (any other command) → leave untouched, no error.
//   - File present + invalid JSON  → return error, do NOT clobber.
func EnsureStatusLine(path, slCmd string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeAtomic(path, freshStatusLineDoc(slCmd))
	}
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}
	if mergeStatusLine(obj, slCmd) {
		out, err := json.MarshalIndent(obj, "", "  ")
		if err != nil {
			return fmt.Errorf("ccsettings: marshal: %w", err)
		}
		return writeAtomic(path, append(out, '\n'))
	}
	return nil
}

func statusLineEntry(slCmd string) map[string]any {
	return map[string]any{"type": "command", "command": slCmd}
}

func freshStatusLineDoc(slCmd string) []byte {
	doc := map[string]any{"statusLine": statusLineEntry(slCmd)}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}

// mergeStatusLine returns true if obj changed (write needed).
// Idempotency / ownership key: command ends with " statusline".
func mergeStatusLine(obj map[string]any, slCmd string) bool {
	sl, _ := obj["statusLine"].(map[string]any)
	if sl == nil {
		// absent (or wrong type) → install ours
		obj["statusLine"] = statusLineEntry(slCmd)
		return true
	}
	cur, _ := sl["command"].(string)
	if !strings.HasSuffix(cur, " statusline") {
		return false // FOREIGN statusLine — never clobber, no write
	}
	if cur == slCmd {
		return false // ours, unchanged
	}
	obj["statusLine"] = statusLineEntry(slCmd) // ours, moved → update
	return true
}

// writeAtomic writes data to path using temp-file + rename (crash-safe).
// Mode is always 0600 — settings.json can hold tokens.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ccsettings: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings.json.tmp")
	if err != nil {
		return fmt.Errorf("ccsettings: create temp: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("ccsettings: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("ccsettings: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("ccsettings: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("ccsettings: rename temp: %w", err)
	}
	ok = true
	return nil
}
