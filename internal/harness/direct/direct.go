// Package direct builds claude env for the non-relay provider modes (VCD-57):
//   - NamedKeyEnv: BYO OAuth token, direct to api.anthropic.com (relay bypassed).
//   - PlainEnv:    native Claude Code auth, no vc injection at all.
//
// Both strip the same relay-specific keys that relay.BuildEnv injects, so a
// previously-relay parent env cannot leak proxy settings onto the direct path.
package direct

import "strings"

// stripKeys are the relay/auth vars removed from the parent env before the
// direct or plain values (if any) are applied. Mirrors relay.stripKeys.
var stripKeys = map[string]bool{
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	"HTTPS_PROXY":             true,
	"NODE_EXTRA_CA_CERTS":     true,
	"ANTHROPIC_API_KEY":       true,
	"ANTHROPIC_BASE_URL":      true,
}

func stripped(parent []string) []string {
	out := make([]string, 0, len(parent)+1)
	for _, e := range parent {
		k, _, _ := strings.Cut(e, "=")
		if stripKeys[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// NamedKeyEnv builds env for direct-to-Anthropic with a BYO OAuth token.
// No proxy, no base-url override (CC defaults to api.anthropic.com), token
// supplied via CLAUDE_CODE_OAUTH_TOKEN (the var CC reads for sk-ant-oat01 tokens).
func NamedKeyEnv(parent []string, token string) []string {
	out := stripped(parent)
	out = append(out, "CLAUDE_CODE_OAUTH_TOKEN="+token)
	return out
}

// PlainEnv builds env for native Claude Code auth: strip all vc injection and
// let claude use whatever credentials the user's own install already has.
func PlainEnv(parent []string) []string {
	return stripped(parent)
}
