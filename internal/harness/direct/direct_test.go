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

func TestNamedKeyEnv_DirectToAnthropic(t *testing.T) {
	env := NamedKeyEnv(parent, "sk-ant-oat01-USERKEY")
	m := envMap(env)

	if m["PATH"] != "/usr/bin" {
		t.Errorf("PATH not preserved: %q", m["PATH"])
	}
	if _, ok := m["HTTPS_PROXY"]; ok {
		t.Error("HTTPS_PROXY should be stripped (no relay proxy on direct path)")
	}
	if _, ok := m["NODE_EXTRA_CA_CERTS"]; ok {
		t.Error("NODE_EXTRA_CA_CERTS should be stripped")
	}
	if _, ok := m["ANTHROPIC_BASE_URL"]; ok {
		t.Error("ANTHROPIC_BASE_URL must be absent so CC talks to api.anthropic.com directly")
	}
	// Bearer is carried by ANTHROPIC_AUTH_TOKEN (not CLAUDE_CODE_OAUTH_TOKEN),
	// so the machine's stored account can't override the user's selected key.
	if _, ok := m["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Error("CLAUDE_CODE_OAUTH_TOKEN must be stripped, never re-emitted")
	}
	if m["ANTHROPIC_AUTH_TOKEN"] != "sk-ant-oat01-USERKEY" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the user key", m["ANTHROPIC_AUTH_TOKEN"])
	}
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
