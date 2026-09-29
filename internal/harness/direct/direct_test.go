package direct

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}

// parent simulates an inherited env that already has relay vars set, to prove
// they get stripped on the direct/plain paths.
var parent = []string{
	"PATH=/usr/bin",
	"HTTPS_PROXY=http://relay.makscee.ru:8448",
	"NODE_EXTRA_CA_CERTS=/some/ca.pem",
	"ANTHROPIC_BASE_URL=",
	"CLAUDE_CODE_OAUTH_TOKEN=pool-token",
	"ANTHROPIC_AUTH_TOKEN=stale-bearer",
	"ANTHROPIC_API_KEY=",
}

func TestPlainEnv_NoInjection(t *testing.T) {
	env := PlainEnv(parent)
	m := envMap(env)

	if m["PATH"] != "/usr/bin" {
		t.Errorf("PATH not preserved: %q", m["PATH"])
	}
	for _, k := range []string{"HTTPS_PROXY", "NODE_EXTRA_CA_CERTS", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s should be stripped on plain path (native CC auth)", k)
		}
	}
}
