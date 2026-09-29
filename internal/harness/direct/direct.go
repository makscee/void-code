// Package direct strips inherited Claude Code relay and auth variables from a
// parent environment (PlainEnv), so none of them reach the child process.
package direct

import "strings"

// stripKeys are the relay/auth vars removed from the parent env before the
// direct or plain values (if any) are applied. Mirrors relay.stripKeys.
var stripKeys = map[string]bool{
	"ANTHROPIC_AUTH_TOKEN":    true,
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

// PlainEnv builds env for native Claude Code auth: strip all vc injection and
// let claude use whatever credentials the user's own install already has.
func PlainEnv(parent []string) []string {
	return stripped(parent)
}
