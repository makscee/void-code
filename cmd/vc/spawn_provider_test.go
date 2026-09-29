package main

import (
	"github.com/makscee/void-code/internal/provider"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiSpawnEnvStripsInheritedSecretsAndUsesAbsoluteBootstrap(t *testing.T) {
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, []string{"PATH=/evil", "ANTHROPIC_AUTH_TOKEN=old", "HTTPS_PROXY=old", "OPENAI_API_KEY=old", "VC_AUTH_TOKEN=old", "VC_BOOTSTRAP_EXECUTABLE=relative", "VC_PROVIDER=old", "VC_RELAY_PROVIDER_ID=old", "VC_RELAY_URL=old", "VC_RELAY_CA=old"})
	got := strings.Join(env, "\n")
	for _, bad := range []string{"ANTHROPIC_AUTH_TOKEN", "HTTPS_PROXY=old", "OPENAI_API_KEY", "VC_AUTH_TOKEN=old", "VC_BOOTSTRAP_EXECUTABLE=relative"} {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %s: %s", bad, got)
		}
	}
	if !strings.Contains(got, "VC_HARNESS=pi") {
		t.Fatalf("missing VC_HARNESS=pi: %s", got)
	}
	// Pi gets the token and relay from `vc pi-bootstrap`, never from its
	// environment, where every command the agent runs could read them.
	for _, key := range []string{"VC_PROVIDER", "VC_RELAY_PROVIDER_ID", "VC_RELAY_URL", "VC_RELAY_CA", "VC_AUTH_TOKEN"} {
		if value, ok := findEnv(env, key); ok {
			t.Fatalf("Pi env carries %s=%q", key, value)
		}
	}
	if value, ok := findEnv(env, "VC_BOOTSTRAP_EXECUTABLE"); ok && !filepath.IsAbs(value) {
		t.Fatalf("untrusted bootstrap path %q", value)
	}
}
func findEnv(env []string, key string) (string, bool) {
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}
