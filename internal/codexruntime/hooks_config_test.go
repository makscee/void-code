package codexruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Step 3 of the Codex work (spec 2026-09-29-desktop-codex-chats-design): the
// managed config.toml always carries three hooks that run `vc codex-hook`, and
// trusts them. Codex 0.158 keeps user hooks `untrusted` by default and then
// SILENTLY does not run them, so a hook without its hooks.state entry is not a
// degraded hook, it is no hook at all — and nothing on screen says so.
//
// The trusted_hash is Codex's sha256 of the normalised hook entry. It does not
// depend on the path, but it changes with ANY change to the entry (a timeout,
// a statusMessage, a matcher) — and with the operating system. Codex hashes
// the command it would actually run: on Windows `commandWindows` (falling back
// to `command`), elsewhere `command`, and then normalises the entry without
// commandWindows (codex-rs/hooks/src/engine/discovery.rs:513,
// `if cfg!(windows) { command_windows.unwrap_or(command) }`). One triple for
// every OS left all three hooks "modified" on WIN11-VCLAB (30.09): Codex
// stopped the chat at "Hooks need review — 3 hooks are new or changed".
//
// Values measured with `codex app-server` → `hooks/list` for 0.158.0: the
// Unix triple on macOS 29.09 (Linux takes the same non-Windows branch), the
// Windows triple on WIN11-VCLAB 30.09 for the `cmd /c` command below. The smoke test in
// pinned_codex_smoke_test.go asks a real Codex on the machine it runs on.
var unixHookHashes = map[string]string{
	"session_start":      "sha256:d2aed9f24bfba2e8a3b3e910fd4a13f935bc0912c2c11eece03fa95197dab30e",
	"user_prompt_submit": "sha256:f3d178d8750d3a7181bf107bd17c4b6e8a431fba956d7b256e876cd5918d0d3a",
	"stop":               "sha256:458c3eff774889f6a55f22bdff7e82b22f56ee82e88ea07c312e50beb6840846",
}

// These hash the entry whose commandWindows is `cmd /c "%VC_HOOK_EXE%" codex-hook`
// (see wantHookCommandWindows). The earlier triple e642bd4f…/5dade9e0…/6d3d6f39…
// belonged to the bare `"%VC_HOOK_EXE%" codex-hook`, which Windows cannot run.
var windowsHookHashes = map[string]string{
	"session_start":      "sha256:dd7994ddbeafdc6806167f89e639af9445e84400bfb7fcc6e46ce0f285a80e9f",
	"user_prompt_submit": "sha256:32b1103902f574bc8baacdf15fea42c19d4132e05f69df0b9e6f9ce63ece98a8",
	"stop":               "sha256:93b1d1c6815b89634df0f49d37b9ebc1c08cdf9a5d76214669a687a2923de88a",
}

// specHookHashes is the triple Codex computes on the OS running the test.
var specHookHashes = expectedHookHashes(runtime.GOOS)

func expectedHookHashes(goos string) map[string]string {
	if goos == "windows" {
		return windowsHookHashes
	}
	return unixHookHashes
}

// hookEvents maps the config.toml event name to its snake_case key suffix.
var hookEvents = []struct{ name, snake string }{
	{"SessionStart", "session_start"},
	{"UserPromptSubmit", "user_prompt_submit"},
	{"Stop", "stop"},
}

// Codex runs a hook through the person's own shell (core/src/session/mod.rs:5178,
// build_hooks_config takes environment.shell). On Windows that is usually
// PowerShell, where `"%VC_HOOK_EXE%" codex-hook` is a syntax error: %VAR% is
// cmd.exe syntax and a quoted string followed by an argument does not parse as
// a call. On WIN11-VCLAB 30.09 every hook "exited with code 1". Handing the
// line to cmd explicitly works whatever the shell is: with it, SessionStart,
// UserPromptSubmit and Stop completed and status.json/session.json were written.
// On Unix the command runs in sh/bash/zsh, which all read "$VC_HOOK_EXE".
const (
	wantHookCommand        = `"$VC_HOOK_EXE" codex-hook`
	wantHookCommandWindows = `cmd /c "%VC_HOOK_EXE%" codex-hook`
)

// ─── a TOML reader just big enough for the managed file ─────────────────────
//
// parseTOML in codexruntime_test.go flattens [[array]] tables and keeps values
// raw. Hooks need both: how many [[hooks.Stop]] groups there are, and the
// decoded string (the implementation may write "..." or '...', which TOML
// reads as the same value and Codex hashes the same way).

type tomlTable struct {
	path  []string // nil for the root table
	array bool     // declared with [[...]]
	kv    map[string]string
	line  int
}

func (tb tomlTable) name() string { return strings.Join(tb.path, ".") }

// splitTOMLKey splits a dotted key (bare or quoted segments) into its parts.
func splitTOMLKey(t *testing.T, s string) []string {
	t.Helper()
	var parts []string
	s = strings.TrimSpace(s)
	for s != "" {
		var part string
		switch s[0] {
		case '"':
			end := closingQuote(s)
			if end < 0 {
				t.Fatalf("unterminated quoted key in %q", s)
			}
			part = decodeTOMLString(t, s[:end+1])
			s = s[end+1:]
		case '\'':
			end := strings.IndexByte(s[1:], '\'')
			if end < 0 {
				t.Fatalf("unterminated literal key in %q", s)
			}
			part = s[1 : end+1]
			s = s[end+2:]
		default:
			end := strings.IndexByte(s, '.')
			if end < 0 {
				end = len(s)
			}
			part = strings.TrimSpace(s[:end])
			s = s[end:]
		}
		parts = append(parts, part)
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, ".") {
			s = strings.TrimSpace(s[1:])
		} else if s != "" {
			t.Fatalf("bad dotted key near %q", s)
		}
	}
	return parts
}

// closingQuote returns the index of the quote closing the basic string that
// opens s, honouring backslash escapes.
func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

// decodeTOMLString decodes a basic ("...") or literal ('...') TOML string;
// anything else comes back as written.
func decodeTOMLString(t *testing.T, raw string) string {
	t.Helper()
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return raw
	}
	body := raw[1 : len(raw)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(body) {
			t.Fatalf("dangling escape in %s", raw)
		}
		switch body[i] {
		case '"', '\\':
			b.WriteByte(body[i])
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'u', 'U':
			n := 4
			if body[i] == 'U' {
				n = 8
			}
			if i+1+n > len(body) {
				t.Fatalf("short unicode escape in %s", raw)
			}
			code, err := strconv.ParseUint(body[i+1:i+1+n], 16, 32)
			if err != nil {
				t.Fatalf("bad unicode escape in %s: %v", raw, err)
			}
			b.WriteRune(rune(code))
			i += n
		default:
			t.Fatalf("unknown escape \\%c in %s", body[i], raw)
		}
	}
	return b.String()
}

// readTOMLTables returns every table in file order, the root first. A plain
// [table] declared twice is invalid TOML and fails the test.
func readTOMLTables(t *testing.T, text string) []tomlTable {
	t.Helper()
	tables := []tomlTable{{kv: map[string]string{}}}
	seen := map[string]bool{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			tables = append(tables, tomlTable{path: splitTOMLKey(t, line[2:len(line)-2]), array: true, kv: map[string]string{}, line: i + 1})
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			tb := tomlTable{path: splitTOMLKey(t, line[1:len(line)-1]), kv: map[string]string{}, line: i + 1}
			if seen[strings.Join(tb.path, "\x00")] {
				t.Fatalf("line %d: table [%s] declared twice", i+1, tb.name())
			}
			seen[strings.Join(tb.path, "\x00")] = true
			tables = append(tables, tb)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("line %d is not key = value: %q", i+1, raw)
		}
		keyParts := splitTOMLKey(t, key)
		k := strings.Join(keyParts, ".")
		cur := &tables[len(tables)-1]
		if _, dup := cur.kv[k]; dup {
			t.Fatalf("line %d: key %s repeated in [%s]", i+1, k, cur.name())
		}
		cur.kv[k] = decodeTOMLString(t, value)
	}
	return tables
}

func tablesUnder(tables []tomlTable, prefix ...string) []tomlTable {
	var out []tomlTable
	for _, tb := range tables {
		if len(tb.path) < len(prefix) {
			continue
		}
		match := true
		for i, p := range prefix {
			if tb.path[i] != p {
				match = false
				break
			}
		}
		if match {
			out = append(out, tb)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeAndRead(t *testing.T, write func(codexHome string) error, codexHome string) (string, []tomlTable) {
	t.Helper()
	if err := write(codexHome); err != nil {
		t.Fatalf("writing the Codex config: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml not written: %v", err)
	}
	return string(data), readTOMLTables(t, string(data))
}

// realConfigPath is the path Codex keys hook trust by: config.toml under the
// symlink-resolved CODEX_HOME. Measured 29.09 with the pinned binary: started
// with CODEX_HOME=/tmp/…/probeA (a symlink path on macOS), app-server reported
// codexHome=/private/tmp/…/probeA and keyed every hook as
// /private/tmp/…/probeA/config.toml:<event>:0:0; a hooks.state keyed by the
// /tmp/… spelling left all three hooks "untrusted", the /private/tmp/… one made
// them "trusted".
func realConfigPath(t *testing.T, codexHome string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		t.Fatalf("resolve %s: %v", codexHome, err)
	}
	return filepath.Join(resolved, "config.toml")
}

// ─── the hooks ──────────────────────────────────────────────────────────────

// Exactly the spec's entry per event: one group, one command handler, the
// POSIX and the Windows command, and nothing else. A `timeout`, a `matcher` or
// a `statusMessage` would each change Codex's hash of the entry, and the hook
// would stop running without a word.
func TestWriteConfigRunsVCCodexHookOnThreeEvents(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	text, tables := writeAndRead(t, func(h string) error { return WriteConfig(h, "https://relay.test:443") }, codexHome)

	var events []string
	for _, tb := range tablesUnder(tables, "hooks") {
		if len(tb.path) >= 2 && tb.path[1] == "state" {
			continue
		}
		if len(tb.path) == 2 {
			events = append(events, tb.path[1])
		}
	}
	sort.Strings(events)
	if got, want := strings.Join(events, ","), "SessionStart,Stop,UserPromptSubmit"; got != want {
		t.Fatalf("hook groups for events [%s], want exactly one group each for [%s]\n%s", got, want, text)
	}

	for _, ev := range hookEvents {
		groups := tablesUnder(tables, "hooks", ev.name)
		var group, handlers []tomlTable
		for _, tb := range groups {
			switch len(tb.path) {
			case 2:
				group = append(group, tb)
			case 3:
				if tb.path[2] != "hooks" {
					t.Errorf("unexpected table [%s] under hooks.%s", tb.name(), ev.name)
					continue
				}
				handlers = append(handlers, tb)
			default:
				t.Errorf("unexpected table [%s] under hooks.%s", tb.name(), ev.name)
			}
		}
		if len(group) != 1 || !group[0].array {
			t.Errorf("hooks.%s: want exactly one [[hooks.%s]] group, got %d (array=%v)", ev.name, ev.name, len(group), len(group) == 1 && group[0].array)
			continue
		}
		if len(group[0].kv) != 0 {
			t.Errorf("[[hooks.%s]] carries keys %v; a matcher or any other group key changes Codex's hash", ev.name, sortedKeys(group[0].kv))
		}
		if len(handlers) != 1 || !handlers[0].array {
			t.Errorf("hooks.%s: want exactly one [[hooks.%s.hooks]] handler, got %d", ev.name, ev.name, len(handlers))
			continue
		}
		h := handlers[0].kv
		if got := strings.Join(sortedKeys(h), ","); got != "command,commandWindows,type" {
			t.Errorf("[[hooks.%s.hooks]] keys = [%s], want exactly [command,commandWindows,type] — timeout/statusMessage change the hash", ev.name, got)
		}
		if h["type"] != "command" {
			t.Errorf("[[hooks.%s.hooks]] type = %q, want \"command\"", ev.name, h["type"])
		}
		if h["command"] != wantHookCommand {
			t.Errorf("[[hooks.%s.hooks]] command = %q, want %q", ev.name, h["command"], wantHookCommand)
		}
		if h["commandWindows"] != wantHookCommandWindows {
			t.Errorf("[[hooks.%s.hooks]] commandWindows = %q, want %q", ev.name, h["commandWindows"], wantHookCommandWindows)
		}
	}
}

// The trust entries are keyed by the config file Codex actually reads — the
// symlink-resolved one — and carry the pinned hashes, nothing more.
func TestWriteConfigTrustsItsHooksUnderTheWrittenConfigPath(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	text, tables := writeAndRead(t, func(h string) error { return WriteConfig(h, "https://relay.test:443") }, codexHome)
	assertHookTrust(t, text, tables, realConfigPath(t, codexHome))
}

func assertHookTrust(t *testing.T, text string, tables []tomlTable, configPath string) {
	t.Helper()
	state := map[string]tomlTable{}
	for _, tb := range tablesUnder(tables, "hooks", "state") {
		if len(tb.path) != 3 || tb.array {
			t.Errorf("unexpected table [%s] (array=%v) under hooks.state", tb.name(), tb.array)
			continue
		}
		state[tb.path[2]] = tb
	}
	if len(state) != len(hookEvents) {
		var keys []string
		for k := range state {
			keys = append(keys, k)
		}
		t.Errorf("hooks.state has %d entries %q, want exactly %d", len(state), keys, len(hookEvents))
	}
	for _, ev := range hookEvents {
		key := configPath + ":" + ev.snake + ":0:0"
		tb, ok := state[key]
		if !ok {
			t.Errorf("no [hooks.state.%q]: without it Codex keeps the %s hook untrusted and silently skips it\n%s", key, ev.name, text)
			continue
		}
		if got := strings.Join(sortedKeys(tb.kv), ","); got != "trusted_hash" {
			t.Errorf("[hooks.state.%q] keys = [%s], want exactly [trusted_hash]", key, got)
		}
		if got, want := tb.kv["trusted_hash"], specHookHashes[ev.snake]; got != want {
			t.Errorf("[hooks.state.%q] trusted_hash = %q, want %q (Codex 0.158.0, hooks/list 29.09)", key, got, want)
		}
	}
}

// The hash does not depend on the path, the key does: two homes, two sets of
// keys, one set of hashes.
func TestWriteConfigHookTrustFollowsTheCodexHome(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"first-home", "second-home"} {
		codexHome := filepath.Join(root, name)
		text, tables := writeAndRead(t, func(h string) error { return WriteConfig(h, "https://relay.test:443") }, codexHome)
		assertHookTrust(t, text, tables, realConfigPath(t, codexHome))
	}
}

// A CODEX_HOME reached through a symlink: Codex resolves it before keying, so
// the trust entries must name the resolved path, not the spelling vc was given.
func TestWriteConfigKeysHookTrustByTheResolvedHome(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real-codex-home")
	if err := os.MkdirAll(real, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-codex-home")
	if err := os.Symlink(real, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable here: %v", err)
		}
		t.Fatal(err)
	}
	text, tables := writeAndRead(t, func(h string) error { return WriteConfig(h, "https://relay.test:443") }, link)
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	assertHookTrust(t, text, tables, filepath.Join(resolved, "config.toml"))
	if strings.Contains(text, filepath.Join(link, "config.toml")+":") {
		t.Errorf("hooks.state is keyed by the symlink spelling %s; Codex would leave the hooks untrusted", link)
	}
}

// ─── trust hashes per operating system ──────────────────────────────────────

// HookTrustedHashes(goos) is the seam: which triple a given OS needs, checked
// for every OS from the one machine running this, so the Windows branch is
// executed by a macOS or Linux run too.
func TestHookTrustedHashesArePerOperatingSystem(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux", "freebsd"} {
		got := HookTrustedHashes(goos)
		want := expectedHookHashes(goos)
		if len(got) != len(want) {
			t.Errorf("HookTrustedHashes(%q) has %d entries %v, want exactly %d (session_start, user_prompt_submit, stop)", goos, len(got), got, len(want))
		}
		for key, hash := range want {
			if got[key] != hash {
				t.Errorf("HookTrustedHashes(%q)[%q] = %q, want %q", goos, key, got[key], hash)
			}
		}
	}
}

// The two triples really differ; a seam returning the Unix triple for every
// OS is the bug from WIN11-VCLAB.
func TestHookTrustedHashesDifferForWindows(t *testing.T) {
	win, unix := HookTrustedHashes("windows"), HookTrustedHashes("linux")
	for _, ev := range hookEvents {
		if win[ev.snake] == unix[ev.snake] {
			t.Errorf("%s: Windows and Unix trust the same hash %q; Codex hashes commandWindows on Windows", ev.snake, win[ev.snake])
		}
	}
}

// The seam hands out a copy: a caller writing into the map must not change
// what the next config trusts.
func TestHookTrustedHashesCannotBeChangedByACaller(t *testing.T) {
	first := HookTrustedHashes(runtime.GOOS)
	for key := range first {
		first[key] = "sha256:tampered"
	}
	for key, hash := range HookTrustedHashes(runtime.GOOS) {
		if hash == "sha256:tampered" {
			t.Fatalf("HookTrustedHashes returned its own map: %s stays tampered", key)
		}
	}
}

// What is written is the triple for the OS vc runs on — the config is read by
// a Codex on this same machine.
func TestWriteConfigTrustsTheHashesOfThisOperatingSystem(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	text, tables := writeAndRead(t, func(h string) error { return WriteConfigFor(h, "https://relay.test:443", "") }, codexHome)
	want := HookTrustedHashes(runtime.GOOS)
	for _, tb := range tablesUnder(tables, "hooks", "state") {
		key := tb.path[len(tb.path)-1]
		snake := ""
		for _, ev := range hookEvents {
			if strings.HasSuffix(key, ":"+ev.snake+":0:0") {
				snake = ev.snake
			}
		}
		if snake == "" {
			t.Errorf("unexpected trust key %q", key)
			continue
		}
		if tb.kv["trusted_hash"] != want[snake] {
			t.Errorf("[hooks.state.%q] trusted_hash = %q, want %q for %s\n%s", key, tb.kv["trusted_hash"], want[snake], runtime.GOOS, text)
		}
	}
}

// ─── the trusted folder ─────────────────────────────────────────────────────

// The desktop runs Codex in the folder the person already chose and trusted in
// the app; Codex asking "Trust this folder?" again is a second gate for the
// same decision. WriteConfigFor writes that trust; WriteConfig (the CLI) never
// does — in a terminal the question is Codex's to ask.
func TestWriteConfigForTrustsTheChosenFolder(t *testing.T) {
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	t.Setenv("VC_AUTH_TOKEN", "tok-must-not-be-written-desktop-91c2")
	text, tables := writeAndRead(t, func(h string) error { return WriteConfigFor(h, "https://relay.makscee.ru:443", folder) }, codexHome)

	projects := tablesUnder(tables, "projects")
	if len(projects) != 1 {
		t.Fatalf("want exactly one [projects.\"%s\"] table, got %d\n%s", folder, len(projects), text)
	}
	tb := projects[0]
	if len(tb.path) != 2 || tb.path[1] != folder || tb.array {
		t.Fatalf("projects table is [%s], want [projects.%q]", tb.name(), folder)
	}
	if got := strings.Join(sortedKeys(tb.kv), ","); got != "trust_level" {
		t.Errorf("[projects.%q] keys = [%s], want exactly [trust_level]", folder, got)
	}
	if tb.kv["trust_level"] != "trusted" {
		t.Errorf("[projects.%q] trust_level = %q, want \"trusted\"", folder, tb.kv["trust_level"])
	}
	if strings.Contains(text, "tok-must-not-be-written-desktop-91c2") {
		t.Fatal("config.toml carries the VC token")
	}
	// Everything else is the same managed file: relay, hooks, their trust.
	root := tables[0].kv
	if root["model_provider"] != "void" {
		t.Errorf("model_provider = %q, want void", root["model_provider"])
	}
	for _, tb := range tables {
		if tb.name() == "model_providers.void" && tb.kv["base_url"] != "https://relay.makscee.ru:443/codex" {
			t.Errorf("base_url = %q", tb.kv["base_url"])
		}
	}
	assertHookTrust(t, text, tables, realConfigPath(t, codexHome))
}

func TestWriteConfigTrustsNoFolder(t *testing.T) {
	for name, write := range map[string]func(string) error{
		"WriteConfig":           func(h string) error { return WriteConfig(h, "https://relay.test:443") },
		"WriteConfigFor, empty": func(h string) error { return WriteConfigFor(h, "https://relay.test:443", "") },
	} {
		t.Run(name, func(t *testing.T) {
			text, tables := writeAndRead(t, write, filepath.Join(t.TempDir(), "codex-home"))
			if got := tablesUnder(tables, "projects"); len(got) != 0 {
				t.Fatalf("%s trusted a folder nobody chose: [%s]\n%s", name, got[0].name(), text)
			}
			if strings.Contains(text, "trust_level") {
				t.Fatalf("%s wrote a trust_level:\n%s", name, text)
			}
		})
	}
}

// A new launch in another folder must not keep trusting the previous one: the
// file is rewritten whole.
func TestWriteConfigForForgetsThePreviousFolder(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	first, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfigFor(codexHome, "https://relay.test:443", first); err != nil {
		t.Fatal(err)
	}
	text, tables := writeAndRead(t, func(h string) error { return WriteConfigFor(h, "https://relay.test:443", second) }, codexHome)
	projects := tablesUnder(tables, "projects")
	if len(projects) != 1 || projects[0].path[len(projects[0].path)-1] != second {
		t.Fatalf("after a second launch the projects tables are %v, want only %s\n%s", projects, second, text)
	}
}

// A relative folder is not a folder the person chose; it would be resolved
// against whatever Codex's cwd happens to be.
func TestWriteConfigForRefusesARelativeFolder(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := WriteConfigFor(codexHome, "https://relay.test:443", filepath.Join("some", "relative")); err == nil {
		t.Fatal("WriteConfigFor accepted a relative folder to trust")
	}
}

// No temporary file is left beside the config after a write.
func TestWriteConfigForLeavesNoTemporaryFiles(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	if err := WriteConfigFor(codexHome, "https://relay.test:443", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.toml" {
			t.Errorf("%s left beside config.toml", e.Name())
		}
	}
}
