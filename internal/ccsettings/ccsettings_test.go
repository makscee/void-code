package ccsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// read parses the settings.json at p into a map for assertions.
func read(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func preToolUseEntries(t *testing.T, m map[string]any) []any {
	t.Helper()
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or wrong type")
	}
	pre, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatal("PreToolUse key missing or wrong type")
	}
	return pre
}

func TestEnsureHook_FreshFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := EnsureHook(p, "/usr/local/bin/vc hook"); err != nil {
		t.Fatalf("EnsureHook: %v", err)
	}
	m := read(t, p)
	if _, ok := m["hooks"]; !ok {
		t.Fatal("hooks key missing after fresh write")
	}
	entries := preToolUseEntries(t, m)
	if len(entries) != 1 {
		t.Fatalf("expected 1 PreToolUse entry, got %d", len(entries))
	}
}

func TestEnsureHook_Idempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = EnsureHook(p, "/usr/local/bin/vc hook")
	_ = EnsureHook(p, "/usr/local/bin/vc hook")
	m := read(t, p)
	entries := preToolUseEntries(t, m)
	if len(entries) != 1 {
		t.Fatalf("expected 1 PreToolUse entry after double-run, got %d", len(entries))
	}
}

func TestEnsureHook_PreservesForeignKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, []byte(`{"theme":"light","env":{"FOO":"bar"}}`), 0600)
	if err := EnsureHook(p, "/usr/local/bin/vc hook"); err != nil {
		t.Fatalf("EnsureHook: %v", err)
	}
	m := read(t, p)
	if m["theme"] != "light" {
		t.Fatalf("foreign key 'theme' clobbered: got %v", m["theme"])
	}
	env, _ := m["env"].(map[string]any)
	if env["FOO"] != "bar" {
		t.Fatalf("foreign key 'env.FOO' clobbered: got %v", env["FOO"])
	}
}

func TestEnsureHook_UnparseableNotClobbered(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, []byte(`{not json`), 0600)
	err := EnsureHook(p, "/usr/local/bin/vc hook")
	if err == nil {
		t.Fatal("expected error on unparseable settings.json")
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{not json` {
		t.Fatalf("unparseable file was clobbered: got %q", string(b))
	}
}

func TestEnsureHook_UpdatesPathInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = EnsureHook(p, "/old/vc hook")
	_ = EnsureHook(p, "/new/vc hook") // binary moved — must update, not append
	m := read(t, p)
	entries := preToolUseEntries(t, m)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after path update, got %d", len(entries))
	}
	// Verify it has the new path.
	entry, _ := entries[0].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	if len(inner) == 0 {
		t.Fatal("no inner hooks")
	}
	h, _ := inner[0].(map[string]any)
	if h["command"] != "/new/vc hook" {
		t.Fatalf("command not updated: %v", h["command"])
	}
}

func TestEnsureHook_PreservesForeignPreToolUseEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	initial := `{
  "hooks": {
    "PreToolUse": [
      {"matcher":"*","hooks":[{"type":"command","command":"/other/tool check","timeout":10}]}
    ]
  }
}`
	_ = os.WriteFile(p, []byte(initial), 0600)
	if err := EnsureHook(p, "/usr/local/bin/vc hook"); err != nil {
		t.Fatalf("EnsureHook: %v", err)
	}
	m := read(t, p)
	entries := preToolUseEntries(t, m)
	if len(entries) != 2 {
		t.Fatalf("expected 2 PreToolUse entries (foreign + ours), got %d", len(entries))
	}
}

// Phase 6: Windows spaced-path idempotency.
func TestEnsureHook_WindowsSpacedPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	cmd := `"C:\Program Files\void-code\vc.exe" hook`
	if err := EnsureHook(p, cmd); err != nil {
		t.Fatalf("EnsureHook: %v", err)
	}
	_ = EnsureHook(p, cmd) // idempotent re-run
	m := read(t, p)
	entries := preToolUseEntries(t, m)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// EnsureStatusLine tests
func TestEnsureStatusLine_AbsentWritesFresh(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := EnsureStatusLine(p, "/abs/vc statusline"); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != "/abs/vc statusline" || sl["type"] != "command" {
		t.Fatalf("statusLine not written correctly: %v", obj["statusLine"])
	}
}

func TestEnsureStatusLine_PreservesExistingForeignStatusLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"~/.claude/statusline.sh"}}`), 0600)
	if err := EnsureStatusLine(p, "/abs/vc statusline"); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != "~/.claude/statusline.sh" {
		t.Fatalf("clobbered user's custom statusLine: %v", sl)
	}
}

func TestEnsureStatusLine_IdempotentNoWriteWhenOurs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	EnsureStatusLine(p, "/abs/vc statusline")
	fi1, _ := os.Stat(p)
	EnsureStatusLine(p, "/abs/vc statusline") // second call
	fi2, _ := os.Stat(p)
	if fi1.ModTime() != fi2.ModTime() {
		t.Fatal("second EnsureStatusLine rewrote file — not idempotent")
	}
}

func TestEnsureStatusLine_UpdatesOursOnPathChange(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"/old/vc statusline"}}`), 0600)
	EnsureStatusLine(p, "/new/vc statusline")
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != "/new/vc statusline" {
		t.Fatalf("did not update our own moved-binary statusLine: %v", sl)
	}
}

func TestEnsureStatusLine_PreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"hooks":{"PreToolUse":[]},"env":{"X":"1"}}`), 0600)
	EnsureStatusLine(p, "/abs/vc statusline")
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	if _, ok := obj["hooks"]; !ok {
		t.Fatal("dropped hooks key")
	}
	if _, ok := obj["env"]; !ok {
		t.Fatal("dropped env key")
	}
}

func TestQuoteIfSpace(t *testing.T) {
	if QuoteIfSpace("/no/space") != "/no/space" {
		t.Fatal("no-space path should not be quoted")
	}
	if QuoteIfSpace("/has space/vc") != `"/has space/vc"` {
		t.Fatalf("spaced path should be quoted: got %q", QuoteIfSpace("/has space/vc"))
	}
}

func TestHookCmd(t *testing.T) {
	got := HookCmd("/usr/local/bin/vc")
	if got != "/usr/local/bin/vc hook" {
		t.Fatalf("HookCmd => %q, want /usr/local/bin/vc hook", got)
	}
	got = HookCmd("/Program Files/vc.exe")
	if got != `"/Program Files/vc.exe" hook` {
		t.Fatalf("HookCmd spaced => %q", got)
	}
}

// ---------------------------------------------------------------------------
// VCD-53: KILL automode — allow-all permission posture, no classifier hook.
//
// vc no longer installs the DeepSeek-classifier PreToolUse hook. Instead it
// writes Claude Code's native allow-all permission posture into
// ~/.claude/settings.json:
//
//	permissions.defaultMode == "bypassPermissions"   (allow every tool, no prompt)
//	permissions.skipDangerousModePermissionPrompt == true  (suppress the bypass confirm)
//
// EnsureAllowAllPermissions installs that posture (non-clobbering, idempotent,
// atomic). RemoveHook strips any previously-installed vc classifier hook so
// upgraders stop making classifier sub-calls.
// ---------------------------------------------------------------------------

// perms reads the "permissions" object from settings.json at p.
func perms(t *testing.T, p string) map[string]any {
	t.Helper()
	m := read(t, p)
	pm, ok := m["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions key missing or wrong type: %v", m["permissions"])
	}
	return pm
}

func TestEnsureAllowAllPermissions_AbsentWritesFresh(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := EnsureAllowAllPermissions(p); err != nil {
		t.Fatalf("EnsureAllowAllPermissions: %v", err)
	}
	pm := perms(t, p)
	if pm["defaultMode"] != "bypassPermissions" {
		t.Fatalf("defaultMode = %v, want bypassPermissions", pm["defaultMode"])
	}
	if pm["skipDangerousModePermissionPrompt"] != true {
		t.Fatalf("skipDangerousModePermissionPrompt = %v, want true", pm["skipDangerousModePermissionPrompt"])
	}
}

func TestEnsureAllowAllPermissions_Idempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := EnsureAllowAllPermissions(p); err != nil {
		t.Fatal(err)
	}
	fi1, _ := os.Stat(p)
	if err := EnsureAllowAllPermissions(p); err != nil { // second call must be no-op
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if fi1.ModTime() != fi2.ModTime() {
		t.Fatal("second EnsureAllowAllPermissions rewrote file — not idempotent")
	}
}

func TestEnsureAllowAllPermissions_PreservesForeignKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"theme":"light","env":{"FOO":"bar"}}`), 0600)
	if err := EnsureAllowAllPermissions(p); err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	if m["theme"] != "light" {
		t.Fatalf("clobbered foreign key theme: %v", m["theme"])
	}
	env, _ := m["env"].(map[string]any)
	if env["FOO"] != "bar" {
		t.Fatalf("clobbered foreign key env.FOO: %v", env["FOO"])
	}
	pm, _ := m["permissions"].(map[string]any)
	if pm["defaultMode"] != "bypassPermissions" {
		t.Fatalf("did not install defaultMode: %v", pm)
	}
}

func TestEnsureAllowAllPermissions_MergesIntoExistingPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	// User already has a permissions object with their own deny list — must be kept.
	os.WriteFile(p, []byte(`{"permissions":{"deny":["Read(./.env)"]}}`), 0600)
	if err := EnsureAllowAllPermissions(p); err != nil {
		t.Fatal(err)
	}
	pm := perms(t, p)
	if pm["defaultMode"] != "bypassPermissions" {
		t.Fatalf("defaultMode not set: %v", pm)
	}
	deny, _ := pm["deny"].([]any)
	if len(deny) != 1 || deny[0] != "Read(./.env)" {
		t.Fatalf("clobbered user's deny list: %v", pm["deny"])
	}
}

func TestEnsureAllowAllPermissions_UnparseableNotClobbered(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{not json`), 0600)
	if err := EnsureAllowAllPermissions(p); err == nil {
		t.Fatal("expected error on unparseable settings.json")
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{not json` {
		t.Fatalf("unparseable file was clobbered: %q", string(b))
	}
}

// RemoveHook tests — strip a previously-installed vc classifier PreToolUse hook.

func TestRemoveHook_RemovesOurHook(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	// Simulate an old-vc install: our classifier hook present.
	_ = EnsureHook(p, "/usr/local/bin/vc hook")
	if err := RemoveHook(p); err != nil {
		t.Fatalf("RemoveHook: %v", err)
	}
	m := read(t, p)
	hooks, _ := m["hooks"].(map[string]any)
	if hooks != nil {
		if pre, ok := hooks["PreToolUse"].([]any); ok && len(pre) != 0 {
			t.Fatalf("our classifier hook was not removed: %v", pre)
		}
	}
}

func TestRemoveHook_PreservesForeignHooks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	// User has their own PreToolUse hook (not ours — command does not end " hook").
	os.WriteFile(p, []byte(`{"hooks":{"PreToolUse":[{"matcher":"*","hooks":[{"type":"command","command":"/usr/bin/my-linter"}]}]}}`), 0600)
	if err := RemoveHook(p); err != nil {
		t.Fatal(err)
	}
	m := read(t, p)
	pre := preToolUseEntries(t, m)
	if len(pre) != 1 {
		t.Fatalf("foreign hook removed: %v", pre)
	}
}

func TestRemoveHook_IdempotentWhenAbsent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"env":{"X":"1"}}`), 0600)
	fi1, _ := os.Stat(p)
	if err := RemoveHook(p); err != nil { // no vc hook present → no write
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if fi1.ModTime() != fi2.ModTime() {
		t.Fatal("RemoveHook rewrote file when no vc hook was present")
	}
}

func TestRemoveHook_NoFileIsNoError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := RemoveHook(p); err != nil {
		t.Fatalf("RemoveHook on missing file should be a no-op, got: %v", err)
	}
}

func TestRemoveHook_UnparseableNotClobbered(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{not json`), 0600)
	if err := RemoveHook(p); err == nil {
		t.Fatal("expected error on unparseable settings.json")
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{not json` {
		t.Fatalf("unparseable file was clobbered: %q", string(b))
	}
}

// ─── VCD-62: IsOurStatusLineCmd / MergeStatusLineCmd / SetStatusLine / GetStatusLineCommand ───

func TestIsOurStatusLineCmd(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"/abs/vc statusline", true},
		{"/abs/vc statusline --merge", true},
		{`"/Program Files/vc.exe" statusline`, true},
		{`"/Program Files/vc.exe" statusline --merge`, true},
		{"~/.claude/statusline.sh", false},
		{"echo CUSTOM", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsOurStatusLineCmd(tc.cmd); got != tc.want {
			t.Errorf("IsOurStatusLineCmd(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestMergeStatusLineCmd(t *testing.T) {
	got := MergeStatusLineCmd("/abs/vc")
	if got != "/abs/vc statusline --merge" {
		t.Fatalf("MergeStatusLineCmd => %q, want /abs/vc statusline --merge", got)
	}
	got = MergeStatusLineCmd("/path with spaces/vc")
	if got != `"/path with spaces/vc" statusline --merge` {
		t.Fatalf("MergeStatusLineCmd spaced => %q", got)
	}
}

func TestMergeStatusLine_IdempotentMergeWrapper(t *testing.T) {
	// The merge-wrapper command ("vc statusline --merge") should be recognised as ours
	// and treated idempotently — EnsureStatusLine must NOT re-write it as plain.
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	mergeCmd := "/abs/vc statusline --merge"
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"`+mergeCmd+`"}}`), 0600)
	// EnsureStatusLine with the plain slCmd must leave the merge wrapper alone.
	if err := EnsureStatusLine(p, "/abs/vc statusline"); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != mergeCmd {
		t.Fatalf("EnsureStatusLine overwrote merge wrapper: %v", sl["command"])
	}
}

func TestSetStatusLine_OverwritesForeign(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"theme":"dark","statusLine":{"type":"command","command":"echo CUSTOM"}}`), 0600)
	if err := SetStatusLine(p, "/abs/vc statusline"); err != nil {
		t.Fatalf("SetStatusLine: %v", err)
	}
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != "/abs/vc statusline" {
		t.Fatalf("SetStatusLine did not overwrite: %v", sl["command"])
	}
	if obj["theme"] != "dark" {
		t.Fatal("SetStatusLine clobbered sibling keys")
	}
}

func TestSetStatusLine_FreshFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if err := SetStatusLine(p, "/abs/vc statusline"); err != nil {
		t.Fatalf("SetStatusLine fresh: %v", err)
	}
	var obj map[string]any
	b, _ := os.ReadFile(p)
	json.Unmarshal(b, &obj)
	sl, _ := obj["statusLine"].(map[string]any)
	if sl["command"] != "/abs/vc statusline" {
		t.Fatalf("SetStatusLine fresh: %v", sl)
	}
}

func TestSetStatusLine_UnparseableError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{not json`), 0600)
	if err := SetStatusLine(p, "/abs/vc statusline"); err == nil {
		t.Fatal("expected error on unparseable settings.json")
	}
}

func TestGetStatusLineCommand_Absent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	cmd, err := GetStatusLineCommand(p)
	if err != nil || cmd != "" {
		t.Fatalf("absent file: cmd=%q err=%v", cmd, err)
	}
}

func TestGetStatusLineCommand_Present(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"echo CUSTOM"}}`), 0600)
	cmd, err := GetStatusLineCommand(p)
	if err != nil || cmd != "echo CUSTOM" {
		t.Fatalf("present: cmd=%q err=%v", cmd, err)
	}
}
