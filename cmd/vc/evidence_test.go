//go:build evidence

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/keystore"
	"github.com/makscee/void-code/internal/provider"
)

// TestEvidence_AllThreeEnvModes captures the resolved env for each provider
// mode. Run with: HOME=/tmp/vc57-evidence go test -tags evidence ./cmd/vc/ -v -run TestEvidence
func TestEvidence_AllThreeEnvModes(t *testing.T) {
	// Pre-save a key in the evidence home (already done by keystore evidence test,
	// but re-add here in case this test runs standalone).
	if err := keystore.AddKey("evidence-key", "sk-ant-oat01-EVID"); err != nil {
		t.Logf("AddKey (may already exist): %v", err)
	}

	parent := []string{
		"PATH=/usr/bin:/usr/local/bin",
	}

	fmt.Println("\n=== MODE: Relay ===")
	relayEnv, err := buildSpawnEnv(provider.Provider{Kind: provider.Relay},
		parent, "https", "relay.makscee.ru:443", "pool-token-relay", "/ca.pem")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	for _, e := range relayEnv {
		k, _, _ := strings.Cut(e, "=")
		switch k {
		case "HTTPS_PROXY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "NODE_EXTRA_CA_CERTS", "ANTHROPIC_API_KEY":
			fmt.Printf("  %s\n", e)
		}
	}

	fmt.Println("\n=== MODE: Named key (direct Anthropic) ===")
	namedEnv, err := buildSpawnEnv(provider.Provider{Kind: provider.NamedKey, Name: "evidence-key"},
		parent, "https", "relay.makscee.ru:443", "pool-token-relay", "/ca.pem")
	if err != nil {
		t.Fatalf("named key: %v", err)
	}
	injectedKeys := []string{"HTTPS_PROXY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL", "NODE_EXTRA_CA_CERTS", "ANTHROPIC_API_KEY"}
	found := map[string]string{}
	for _, e := range namedEnv {
		k, v, _ := strings.Cut(e, "=")
		for _, ik := range injectedKeys {
			if k == ik {
				found[k] = v
			}
		}
	}
	// Print what's present
	for _, k := range injectedKeys {
		if v, ok := found[k]; ok {
			fmt.Printf("  %s=%s\n", k, v)
		} else {
			fmt.Printf("  %s=<absent>\n", k)
		}
	}

	fmt.Println("\n=== MODE: Plain (native CC auth) ===")
	plainEnv, err := buildSpawnEnv(provider.Provider{Kind: provider.Plain},
		parent, "https", "relay.makscee.ru:443", "pool-token-relay", "/ca.pem")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	found = map[string]string{}
	for _, e := range plainEnv {
		k, v, _ := strings.Cut(e, "=")
		for _, ik := range injectedKeys {
			if k == ik {
				found[k] = v
			}
		}
	}
	for _, k := range injectedKeys {
		if v, ok := found[k]; ok {
			fmt.Printf("  %s=%s\n", k, v)
		} else {
			fmt.Printf("  %s=<absent>\n", k)
		}
	}

	// Assertions
	relayMap := map[string]string{}
	for _, e := range relayEnv {
		k, v, _ := strings.Cut(e, "=")
		relayMap[k] = v
	}
	if relayMap["HTTPS_PROXY"] != "https://relay.makscee.ru:443" {
		t.Errorf("relay HTTPS_PROXY wrong: %q", relayMap["HTTPS_PROXY"])
	}
	if relayMap["CLAUDE_CODE_OAUTH_TOKEN"] != "pool-token-relay" {
		t.Errorf("relay token wrong: %q", relayMap["CLAUDE_CODE_OAUTH_TOKEN"])
	}

	namedMap := map[string]string{}
	for _, e := range namedEnv {
		k, v, _ := strings.Cut(e, "=")
		namedMap[k] = v
	}
	if _, ok := namedMap["HTTPS_PROXY"]; ok {
		t.Error("named key must NOT have HTTPS_PROXY")
	}
	if namedMap["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-EVID" {
		t.Errorf("named key token wrong: %q", namedMap["CLAUDE_CODE_OAUTH_TOKEN"])
	}
	if _, ok := namedMap["ANTHROPIC_BASE_URL"]; ok {
		t.Error("named key must NOT have ANTHROPIC_BASE_URL")
	}

	plainMap := map[string]string{}
	for _, e := range plainEnv {
		k, v, _ := strings.Cut(e, "=")
		plainMap[k] = v
	}
	for _, k := range injectedKeys {
		if _, ok := plainMap[k]; ok {
			t.Errorf("plain must NOT have %s", k)
		}
	}

	fmt.Println("\n=== ASSERTIONS: all passed ===")
}
