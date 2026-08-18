package main

import (
	"github.com/makscee/void-code/internal/provider"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiSpawnEnvStripsInheritedSecretsAndUsesAbsoluteBootstrap(t *testing.T) {
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, []string{"PATH=/evil", "ANTHROPIC_AUTH_TOKEN=old", "HTTPS_PROXY=old", "OPENAI_API_KEY=old", "VC_AUTH_TOKEN=old", "VC_BOOTSTRAP_EXECUTABLE=relative"}, "https", "relay.test:443", "fresh", "/ca")
	got := strings.Join(env, "\n")
	for _, bad := range []string{"ANTHROPIC_AUTH_TOKEN", "HTTPS_PROXY=old", "OPENAI_API_KEY", "VC_AUTH_TOKEN=old", "VC_BOOTSTRAP_EXECUTABLE=relative"} {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %s: %s", bad, got)
		}
	}
	for _, want := range []string{"VC_HARNESS=pi", "VC_PROVIDER=relay", "VC_AUTH_TOKEN=fresh", "VC_RELAY_CA=/ca"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s", want)
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
