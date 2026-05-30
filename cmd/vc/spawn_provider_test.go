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
		[]string{"PATH=/usr/bin"}, "relay.makscee.ru:8448", "pool-tok", "/ca.pem")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if v, ok := findEnv(env, "HTTPS_PROXY"); !ok || v != "http://relay.makscee.ru:8448" {
		t.Errorf("relay HTTPS_PROXY = %q ok=%v", v, ok)
	}
	if v, _ := findEnv(env, "CLAUDE_CODE_OAUTH_TOKEN"); v != "pool-tok" {
		t.Errorf("relay token = %q", v)
	}
}

func TestBuildSpawnEnv_Plain(t *testing.T) {
	env, err := buildSpawnEnv(provider.Provider{Kind: provider.Plain},
		[]string{"PATH=/usr/bin", "HTTPS_PROXY=x"}, "h", "tok", "/ca.pem")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if _, ok := findEnv(env, "HTTPS_PROXY"); ok {
		t.Error("plain must strip HTTPS_PROXY")
	}
	if _, ok := findEnv(env, "CLAUDE_CODE_OAUTH_TOKEN"); ok {
		t.Error("plain must inject no token")
	}
}
