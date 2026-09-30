package codexruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Model is the model Codex starts with.
const Model = "gpt-6-sol"

// configTemplate is the whole of $CODEX_HOME/config.toml. The relay URL is the
// only variable part; the VC token never goes in the file, Codex reads it from
// VC_AUTH_TOKEN in its environment, and the granted relay provider from
// VC_CODEX_PROVIDER. Plugins are off: Codex otherwise clones
// github.com/openai/plugins in the background on every start.
const configTemplate = `# Managed by vc: rewritten whole on every launch; edits here do not survive.
# The VC token is not stored here, Codex reads it from VC_AUTH_TOKEN.
model = "%s"
model_provider = "void"
check_for_update_on_startup = false

[analytics]
enabled = false

[feedback]
enabled = false

[features]
plugins = false

[model_providers.void]
name = "Void relay"
base_url = %s
env_key = "VC_AUTH_TOKEN"
wire_api = "responses"
requires_openai_auth = false
env_http_headers = { "x-void-provider" = "VC_CODEX_PROVIDER" }
`

// HookCommand and HookCommandWindows are what every managed hook runs. Codex
// runs hooks through the person's shell, so the executable comes from
// VC_HOOK_EXE in Codex's environment rather than from a path baked into the
// file. On Windows that shell is PowerShell, which neither expands %VAR% nor
// runs a quoted string as a command, so the Windows command goes through
// `cmd /c` explicitly.
const (
	HookCommand        = `"$VC_HOOK_EXE" codex-hook`
	HookCommandWindows = `cmd /c "%VC_HOOK_EXE%" codex-hook`
)

// hookEvent is one managed hook: its config.toml event name and the
// snake_case suffix Codex keys its trust by.
type hookEvent struct {
	name, snake string
}

// managedHooks are the hooks the managed config carries, in file order.
var managedHooks = []hookEvent{
	{"SessionStart", "session_start"},
	{"UserPromptSubmit", "user_prompt_submit"},
	{"Stop", "stop"},
}

// pinnedUnixHookHashes and pinnedWindowsHookHashes are Codex's own sha256 of each managed
// hook entry (codex app-server → hooks/list) for the pinned Version. Codex
// hashes the command it would run on its own platform — commandWindows on
// Windows, command elsewhere (discovery.rs:513) — so the two triples differ;
// the Windows one was measured on WIN11-VCLAB 30.09 for the `cmd /c` command. They do not depend on the path
// but change with ANY edit to the entry, and a wrong one leaves the hook
// "untrusted", which Codex skips without a word. Recompute both on every bump
// of the pin; TestPinnedCodexTrustsTheManagedHooks fails with the new values
// for the platform it runs on.
var (
	pinnedUnixHookHashes = map[string]string{
		"session_start":      "sha256:d2aed9f24bfba2e8a3b3e910fd4a13f935bc0912c2c11eece03fa95197dab30e",
		"user_prompt_submit": "sha256:f3d178d8750d3a7181bf107bd17c4b6e8a431fba956d7b256e876cd5918d0d3a",
		"stop":               "sha256:458c3eff774889f6a55f22bdff7e82b22f56ee82e88ea07c312e50beb6840846",
	}
	pinnedWindowsHookHashes = map[string]string{
		"session_start":      "sha256:dd7994ddbeafdc6806167f89e639af9445e84400bfb7fcc6e46ce0f285a80e9f",
		"user_prompt_submit": "sha256:32b1103902f574bc8baacdf15fea42c19d4132e05f69df0b9e6f9ce63ece98a8",
		"stop":               "sha256:93b1d1c6815b89634df0f49d37b9ebc1c08cdf9a5d76214669a687a2923de88a",
	}
)

// HookTrustedHashes returns the trusted_hash of each managed hook, keyed by
// its snake_case event, for Codex running on goos. The map is a fresh copy.
func HookTrustedHashes(goos string) map[string]string {
	source := pinnedUnixHookHashes
	if goos == "windows" {
		source = pinnedWindowsHookHashes
	}
	out := make(map[string]string, len(source))
	for k, v := range source {
		out[k] = v
	}
	return out
}

// WriteConfig replaces codexHome/config.toml with the managed configuration
// that sends Codex to relayURL + "/codex". It trusts no folder: in a terminal
// the "Trust this folder?" question is Codex's to ask.
func WriteConfig(codexHome, relayURL string) error {
	return WriteConfigFor(codexHome, relayURL, "")
}

// WriteConfigFor is WriteConfig plus, when trustedFolder is not empty, a
// [projects] entry trusting that folder — the one the person already chose in
// the desktop. trustedFolder must be absolute.
func WriteConfigFor(codexHome, relayURL, trustedFolder string) error {
	if trustedFolder != "" && !filepath.IsAbs(trustedFolder) {
		return fmt.Errorf("Codex trusted folder %q is not absolute", trustedFolder)
	}
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		return fmt.Errorf("create %s: %w", codexHome, err)
	}
	// Codex keys hook trust by config.toml under the symlink-resolved
	// CODEX_HOME; a key spelled through a symlink leaves the hooks untrusted.
	resolvedHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", codexHome, err)
	}
	baseURL := strings.TrimRight(relayURL, "/") + "/codex"
	var b strings.Builder
	fmt.Fprintf(&b, configTemplate, Model, tomlString(baseURL))
	writeHooks(&b, filepath.Join(resolvedHome, "config.toml"), HookTrustedHashes(runtime.GOOS))
	if trustedFolder != "" {
		fmt.Fprintf(&b, "\n[projects.%s]\ntrust_level = \"trusted\"\n", tomlString(trustedFolder))
	}

	tmp, err := os.CreateTemp(codexHome, ".config.toml-*")
	if err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	_, writeErr := tmp.WriteString(b.String())
	closeErr := tmp.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = rename(tmp.Name(), filepath.Join(codexHome, "config.toml"))
	}
	if writeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write Codex config: %w", writeErr)
	}
	return nil
}

// writeHooks appends the managed hooks and their trust entries. Each entry is
// exactly type + command + commandWindows: a matcher, timeout or
// statusMessage would change Codex's hash and silently untrust the hook.
func writeHooks(b *strings.Builder, configPath string, hashes map[string]string) {
	for _, h := range managedHooks {
		fmt.Fprintf(b, "\n[[hooks.%s]]\n[[hooks.%s.hooks]]\ntype = \"command\"\ncommand = %s\ncommandWindows = %s\n",
			h.name, h.name, tomlString(HookCommand), tomlString(HookCommandWindows))
	}
	for _, h := range managedHooks {
		fmt.Fprintf(b, "\n[hooks.state.%s]\ntrusted_hash = %s\n",
			tomlString(configPath+":"+h.snake+":0:0"), tomlString(hashes[h.snake]))
	}
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
