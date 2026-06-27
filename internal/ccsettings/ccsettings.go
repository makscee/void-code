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
//
// always-allow hook, ASCII-guarded: the always-allow PreToolUse hook (a
// belt-and-suspenders that keeps `auto` mode — reachable via shift+tab off
// bypass — classifier-free) is seeded ONLY when the exec path is pure ASCII and
// space-free (PathHookSafe). When the path contains non-ASCII (e.g. Cyrillic) or
// a space, Claude Code's Windows spawn of the hook command fails and emits a
// CP1251 error CC mis-decodes as UTF-8, surfacing a garbled "hook failed" banner
// on EVERY tool call. Those users get native bypassPermissions only (no hook),
// and any stale hook from a prior install is stripped via RemoveHook.
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

// WriteManagedSettings writes vc's full, trust-independent Claude Code settings
// to path (typically ~/.void-code/cc-settings.json) for passing to claude via
// `--settings <path>`.
//
// Why a separate --settings file instead of only ~/.claude/settings.json:
// CC withholds ~/.claude/settings.json until the working folder is TRUSTED, but a
// file passed explicitly with --settings is loaded regardless of trust. So the
// always-allow PreToolUse hook below takes effect even in a fresh/untrusted folder
// — which is what makes `auto` mode approve every tool locally with ZERO model
// sub-call. Without it, a user who shift+tab's into auto mode in an untrusted
// folder hits the server-side safety classifier, which a DeepSeek/relay backend
// can't serve ("<model> temporarily unavailable, so auto mode cannot determine the
// safety of Bash"). The bypass defaults + skipDangerousModePermissionPrompt make
// the --permission-mode bypassPermissions startup flag silent (no accept dialog)
// on every platform regardless of trust.
//
// The file is owned wholly by vc and rewritten each launch (atomic, 0600). hookCmd
// is the same string as for EnsureHook (e.g. "C:/Users/u/.void-code/bin/vc.exe hook").
//
// seedHook gates the always-allow PreToolUse hook (FIX B, Path 3): it is included
// only when the exec path is ASCII + space-free (see PathHookSafe). On non-ASCII
// or spaced paths the hook spawn fails on Windows and spams a garbled banner, so
// those installs get the bypassPermissions posture only and rely on the native
// allow-all (the hook is redundant with bypass; it only matters in `auto` mode).
//
// skipWebFetchPreflight (FIX A) is always written top-level so WebFetch's
// claude.ai safety preflight is skipped for relay users.
func WriteManagedSettings(path, hookCmd string, seedHook bool) error {
	mode, skip := allowAllPermissions()
	doc := map[string]any{
		"permissions": map[string]any{
			"defaultMode":                       mode,
			"skipDangerousModePermissionPrompt": skip,
		},
		// Top-level mirror: CC has accepted this key at the top level too, and
		// vc's prior ~/.claude/settings.json carried both — keep parity.
		"skipDangerousModePermissionPrompt": skip,
		// FIX A: skip CC's hardcoded claude.ai WebFetch safety preflight.
		"skipWebFetchPreflight": true,
		// Suppress the warn-level "claude.ai connectors are disabled because
		// ANTHROPIC_API_KEY ... takes precedence" nag. For relay/BYO users that
		// injected auth always takes precedence, so claude.ai connectors can
		// never load anyway — this takes CC's silent disabled_setting branch
		// before the api_key_precedence warn branch.
		"disableClaudeAiConnectors": true,
	}
	if seedHook {
		doc["hooks"] = map[string]any{
			"PreToolUse": []any{entry(hookCmd)},
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("ccsettings: marshal managed settings: %w", err)
	}
	return writeAtomic(path, append(out, '\n'))
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

// EnsureSkipWebFetchPreflight sets the top-level "skipWebFetchPreflight": true
// in the settings file at path (FIX A). Claude Code runs a server-side URL-safety
// preflight before WebFetch that calls a HARDCODED host
// (https://claude.ai/api/web/domain_info, NOT ANTHROPIC_BASE_URL). Relay users
// can't reach claude.ai directly, and PRD #059 dropped the global HTTPS_PROXY
// that used to tunnel it, so every WebFetch fails with "Unable to verify if
// domain X is safe to fetch". This top-level boolean (sibling of "permissions",
// per the CC settings schema) skips that check.
//
// Non-clobbering + idempotent, mirroring EnsureAllowAllPermissions:
//   - Absent file              → write fresh with just the flag.
//   - Present + valid JSON      → set our top-level key, keep the rest.
//   - Already true              → no-op (no write).
//   - Present + invalid JSON    → return error, do NOT clobber.
func EnsureSkipWebFetchPreflight(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeAtomic(path, freshSkipWebFetchPreflightDoc())
	}
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}
	if obj["skipWebFetchPreflight"] == true {
		return nil // already correct — no write needed
	}
	obj["skipWebFetchPreflight"] = true
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("ccsettings: marshal: %w", err)
	}
	return writeAtomic(path, append(out, '\n'))
}

// freshSkipWebFetchPreflightDoc builds a minimal settings.json with only the flag.
func freshSkipWebFetchPreflightDoc() []byte {
	doc := map[string]any{"skipWebFetchPreflight": true}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}

// EnsureDisableClaudeAiConnectors sets the top-level "disableClaudeAiConnectors":
// true in the settings file at path. When vc injects a relay/BYO bearer, Claude
// Code prints a warn-level nag at the bottom ("claude.ai connectors are disabled
// because ANTHROPIC_API_KEY or another auth source is set and takes precedence
// over your claude.ai login"). This top-level boolean takes CC's silent
// disabled_setting branch BEFORE the api_key_precedence warn branch, so the
// warning disappears and claude still works normally. This is correct for vc
// users: relay/BYO auth always takes precedence, so claude.ai connectors can
// never load anyway.
//
// Non-clobbering + idempotent, mirroring EnsureSkipWebFetchPreflight:
//   - Absent file              → write fresh with just the flag.
//   - Present + valid JSON      → set our top-level key, keep the rest.
//   - Already true              → no-op (no write).
//   - Present + invalid JSON    → return error, do NOT clobber.
func EnsureDisableClaudeAiConnectors(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return writeAtomic(path, freshDisableClaudeAiConnectorsDoc())
	}
	if err != nil {
		return err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("ccsettings: %s invalid JSON (leaving untouched): %w", path, err)
	}
	if obj["disableClaudeAiConnectors"] == true {
		return nil // already correct — no write needed
	}
	obj["disableClaudeAiConnectors"] = true
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("ccsettings: marshal: %w", err)
	}
	return writeAtomic(path, append(out, '\n'))
}

// freshDisableClaudeAiConnectorsDoc builds a minimal settings.json with only the flag.
func freshDisableClaudeAiConnectorsDoc() []byte {
	doc := map[string]any{"disableClaudeAiConnectors": true}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
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

// PathHookSafe reports whether execPath is safe to use as a spawned PreToolUse
// hook command on Windows: pure ASCII and space-free. Claude Code spawns the
// hook command via the OS shell; when the path contains non-ASCII bytes (e.g.
// Cyrillic in a Windows username) or a space, that spawn fails and Windows
// emits a CP1251 error CC mis-decodes as UTF-8 — a garbled "PreToolUse hook
// failed" banner on every tool call. vc only seeds the hook when this returns
// true; otherwise it relies on native bypassPermissions alone and strips any
// stale hook (RemoveHook). Pass the ORIGINAL exec path (pre-ForwardSlash) so the
// space check sees the real characters.
func PathHookSafe(execPath string) bool {
	for _, r := range execPath {
		if r > 0x7e || r < 0x20 || r == ' ' {
			return false
		}
	}
	return true
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

// MergeStatusLineCmd builds the merge-wrapper statusLine command:
// "<abs-or-quoted-path> statusline --merge".
func MergeStatusLineCmd(execPath string) string {
	return QuoteIfSpace(execPath) + " statusline --merge"
}

// IsOurStatusLineCmd reports whether cmd is a vc-owned statusLine command.
// Recognizes both the plain form ("<vc> statusline") and the merge-wrapper
// form ("<vc> statusline --merge") so idempotency + classify both work.
func IsOurStatusLineCmd(cmd string) bool {
	return strings.HasSuffix(cmd, " statusline") || strings.HasSuffix(cmd, " statusline --merge")
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
// Idempotency / ownership key: IsOurStatusLineCmd (covers plain + --merge suffixes).
// Special rule: if the current command is already the merge-wrapper (ends
// " statusline --merge"), it is left as-is regardless of slCmd — the merge
// wrapper was installed after explicit user consent and must not be silently
// downgraded by the spawn-path EnsureStatusLine.
func mergeStatusLine(obj map[string]any, slCmd string) bool {
	sl, _ := obj["statusLine"].(map[string]any)
	if sl == nil {
		// absent (or wrong type) → install ours
		obj["statusLine"] = statusLineEntry(slCmd)
		return true
	}
	cur, _ := sl["command"].(string)
	if !IsOurStatusLineCmd(cur) {
		return false // FOREIGN statusLine — never clobber, no write
	}
	if cur == slCmd {
		return false // ours, unchanged
	}
	// If current is the merge wrapper, leave it alone — user consent was given.
	if strings.HasSuffix(cur, " statusline --merge") {
		return false
	}
	obj["statusLine"] = statusLineEntry(slCmd) // ours (plain), moved → update
	return true
}

// SetStatusLine unconditionally overwrites the "statusLine" entry in settings.json
// at path with slCmd. Used for override and merge-install (after explicit consent).
// If the file does not exist it is created fresh. Invalid JSON → return error.
func SetStatusLine(path, slCmd string) error {
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
	obj["statusLine"] = statusLineEntry(slCmd)
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("ccsettings: marshal: %w", err)
	}
	return writeAtomic(path, append(out, '\n'))
}

// GetStatusLineCommand reads the current statusLine.command from settings at path.
// Returns "", nil when the file is absent or has no statusLine entry.
func GetStatusLineCommand(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("ccsettings: %s invalid JSON: %w", path, err)
	}
	sl, _ := obj["statusLine"].(map[string]any)
	if sl == nil {
		return "", nil
	}
	cmd, _ := sl["command"].(string)
	return cmd, nil
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
