package compat

import (
	"testing"

	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/provider"
)

func TestClassifyProvider(t *testing.T) {
	grants := []Grant{{ID: "opaque-1", Name: "OpenAI seats"}, {ID: "deepseek-sub", Name: "DeepSeek"}}
	cases := []struct {
		name  string
		p     provider.Provider
		label string
		want  ProviderClass
	}{
		{"relay", provider.Provider{Kind: provider.Relay}, "", ProviderDeepSeek},
		{"chatgpt id", provider.Provider{Kind: provider.RelayProvider, ID: "chatgpt-sub"}, "", ProviderChatGPT},
		{"openai grant name", provider.Provider{Kind: provider.RelayProvider, ID: "opaque-1"}, "", ProviderChatGPT},
		{"codex label", provider.Provider{Kind: provider.RelayProvider, ID: "opaque-2"}, "Codex relay", ProviderChatGPT},
		{"deepseek id", provider.Provider{Kind: provider.RelayProvider, ID: "deepseek-sub"}, "", ProviderDeepSeek},
		{"plain invalid", provider.Provider{Kind: provider.Plain}, "Plain", ProviderInvalid},
		{"key invalid", provider.Provider{Kind: provider.NamedKey, Name: "work"}, "key: work", ProviderInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyProvider(tc.p, tc.label, grants); got != tc.want {
				t.Fatalf("ClassifyProvider = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReconcileMatrix(t *testing.T) {
	chatGrant := []Grant{{ID: "opaque", Name: "OpenAI"}}
	chat := provider.Provider{Kind: provider.RelayProvider, ID: "opaque"}
	deep := provider.Provider{Kind: provider.Relay}

	cases := []struct {
		name        string
		h           harnesschoice.Choice
		p           provider.Provider
		label       string
		grants      []Grant
		wantHarness harnesschoice.Kind
		wantProv    string
	}{
		{"claude chatgpt to deepseek", harnesschoice.Choice{Kind: harnesschoice.Claude}, chat, "ChatGPT relay", chatGrant, harnesschoice.Claude, "relay"},
		{"pi chatgpt stays", harnesschoice.Choice{Kind: harnesschoice.Pi}, chat, "ChatGPT relay", chatGrant, harnesschoice.Pi, "prov:opaque"},
		{"pi deepseek stays", harnesschoice.Choice{Kind: harnesschoice.Pi}, deep, "", nil, harnesschoice.Pi, "relay"},
		{"codex deepseek to chatgpt grant", harnesschoice.Choice{Kind: harnesschoice.Codex}, deep, "", chatGrant, harnesschoice.Codex, "prov:opaque"},
		{"codex deepseek no grant to pi", harnesschoice.Choice{Kind: harnesschoice.Codex}, deep, "", nil, harnesschoice.Pi, "relay"},
		{"plain invalid to relay", harnesschoice.Choice{Kind: harnesschoice.Pi}, provider.Provider{Kind: provider.Plain}, "Plain harness run", nil, harnesschoice.Pi, "relay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Reconcile(tc.h, tc.p, tc.label, tc.grants)
			if got.Harness.Kind != tc.wantHarness || got.Provider.String() != tc.wantProv {
				t.Fatalf("Reconcile = harness %v provider %q, want %v %q", got.Harness.Kind, got.Provider.String(), tc.wantHarness, tc.wantProv)
			}
		})
	}
}
