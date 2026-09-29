package codexruntime

import (
	"fmt"
	"os"
	"path/filepath"
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

// WriteConfig replaces codexHome/config.toml with the managed configuration
// that sends Codex to relayURL + "/codex".
func WriteConfig(codexHome, relayURL string) error {
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		return fmt.Errorf("create %s: %w", codexHome, err)
	}
	baseURL := strings.TrimRight(relayURL, "/") + "/codex"
	body := fmt.Sprintf(configTemplate, Model, tomlString(baseURL))
	tmp, err := os.CreateTemp(codexHome, ".config.toml-*")
	if err != nil {
		return fmt.Errorf("write Codex config: %w", err)
	}
	_, writeErr := tmp.WriteString(body)
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
