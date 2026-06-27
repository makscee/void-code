package main

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/provider"
)

func findEnv(env []string, key string) (string, bool) {
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		if k == key {
			return v, true
		}
	}
	return "", false
}

func TestBuildSpawnEnv_Relay(t *testing.T) {
	env, err := buildSpawnEnv(provider.Provider{Kind: provider.Relay},
		[]string{"PATH=/usr/bin"}, "https", "relay.makscee.ru:443", "pool-tok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if v, ok := findEnv(env, "HTTPS_PROXY"); !ok || v != "https://relay.makscee.ru:443" {
		t.Errorf("relay HTTPS_PROXY = %q ok=%v", v, ok)
	}
	if v, ok := findEnv(env, "NODE_EXTRA_CA_CERTS"); !ok || v != "/etc/relay-ca.pem" {
		t.Errorf("relay NODE_EXTRA_CA_CERTS = %q ok=%v", v, ok)
	}
	if v, ok := findEnv(env, "ANTHROPIC_BASE_URL"); !ok || v != "" {
		t.Errorf("relay ANTHROPIC_BASE_URL = %q ok=%v (want empty)", v, ok)
	}
	if v, _ := findEnv(env, "ANTHROPIC_AUTH_TOKEN"); v != "pool-tok" {
		t.Errorf("relay token = %q", v)
	}
	if _, ok := findEnv(env, "CLAUDE_CODE_OAUTH_TOKEN"); ok {
		t.Error("relay must not emit CLAUDE_CODE_OAUTH_TOKEN")
	}
}

func TestBuildSpawnEnvRelayProviderInjectsHeader(t *testing.T) {
	p := provider.Provider{Kind: provider.RelayProvider, ID: "plat-2"}
	env, err := buildSpawnEnv(p, []string{}, "https", "relay.example:8448", "pooltok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("buildSpawnEnv: %v", err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "ANTHROPIC_CUSTOM_HEADERS=x-void-provider: plat-2") {
		t.Fatalf("missing custom header; env=%v", env)
	}
	// still the relay path: proxy + pool token present.
	if !strings.Contains(joined, "HTTPS_PROXY=https://relay.example:8448") ||
		!strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=pooltok") {
		t.Fatalf("relay proxy vars missing; env=%v", env)
	}
}

func TestBuildSpawnEnvBareRelayHasNoHeader(t *testing.T) {
	p := provider.Provider{Kind: provider.Relay}
	env, _ := buildSpawnEnv(p, []string{}, "https", "relay.example:8448", "pooltok", "/etc/relay-ca.pem")
	if strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_CUSTOM_HEADERS") {
		t.Fatalf("bare Relay must not inject x-void-provider; env=%v", env)
	}
}

func TestBuildSpawnEnv_Plain(t *testing.T) {
	env, err := buildSpawnEnv(provider.Provider{Kind: provider.Plain},
		[]string{"PATH=/usr/bin", "HTTPS_PROXY=x"}, "https", "h", "tok", "/etc/relay-ca.pem")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if _, ok := findEnv(env, "HTTPS_PROXY"); ok {
		t.Error("plain must strip HTTPS_PROXY")
	}
	if _, ok := findEnv(env, "ANTHROPIC_AUTH_TOKEN"); ok {
		t.Error("plain must inject no token")
	}
}
