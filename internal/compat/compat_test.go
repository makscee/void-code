package compat

import (
	"testing"

	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/provider"
)

func TestClassifyProvider(t *testing.T) {
	grants := []Grant{
		{ID: "opaque-1", Name: "OpenAI seats"},
		{ID: "typed-incompatible", Name: "ChatGPT", Type: "anthropic-api-key"},
		{ID: "opaque-type-chat", Name: "Enterprise", Type: "openai-codex-oauth"},
		{ID: "opaque-type-deep", Name: "Enterprise", Type: "deepseek"},
		{ID: "deepseek-sub", Name: "DeepSeek"},
	}
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
		{"chatgpt type", provider.Provider{Kind: provider.RelayProvider, ID: "opaque-type-chat"}, "", ProviderChatGPT},
		{"deepseek type", provider.Provider{Kind: provider.RelayProvider, ID: "opaque-type-deep"}, "", ProviderDeepSeek},
		{"deepseek id", provider.Provider{Kind: provider.RelayProvider, ID: "deepseek-sub"}, "", ProviderDeepSeek},
		{"explicit incompatible type never falls back to name", provider.Provider{Kind: provider.RelayProvider, ID: "typed-incompatible"}, "ChatGPT", ProviderInvalid},
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

func TestExactGrantClassRequiresOpaqueIDAndAuthoritativeType(t *testing.T) {
	active := provider.Provider{Kind: provider.RelayProvider, ID: "active"}
	cases := []struct {
		name   string
		grants []Grant
		want   ProviderClass
		ok     bool
	}{
		{"exact compatible", []Grant{{ID: "active", Name: "neutral", Type: "openai-codex-oauth"}}, ProviderChatGPT, true},
		{"other compatible", []Grant{{ID: "other", Name: "ChatGPT", Type: "openai-codex-oauth"}}, ProviderInvalid, false},
		{"exact explicit incompatible", []Grant{{ID: "active", Name: "ChatGPT", Type: "anthropic-api-key"}}, ProviderInvalid, false},
		{"exact legacy name", []Grant{{ID: "active", Name: "ChatGPT"}}, ProviderChatGPT, true},
		{"confirmed empty", []Grant{}, ProviderInvalid, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ExactGrantClass(active, tc.grants)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ExactGrantClass = %v,%v want %v,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestReconcileMatrix(t *testing.T) {
	chatGrant := []Grant{{ID: "opaque", Name: "Enterprise", Type: "openai-codex-oauth"}}
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
		{"claude chatgpt stays", harnesschoice.Choice{Kind: harnesschoice.Claude}, chat, "ChatGPT relay", chatGrant, harnesschoice.Claude, "prov:opaque"},
		{"claude deepseek stays", harnesschoice.Choice{Kind: harnesschoice.Claude}, deep, "", nil, harnesschoice.Claude, "relay"},
		{"claude plain invalid to relay", harnesschoice.Choice{Kind: harnesschoice.Claude}, provider.Provider{Kind: provider.Plain}, "Plain harness run", nil, harnesschoice.Claude, "relay"},
		{"pi chatgpt stays", harnesschoice.Choice{Kind: harnesschoice.Pi}, chat, "", chatGrant, harnesschoice.Pi, "prov:opaque"},
		{"pi deepseek stays", harnesschoice.Choice{Kind: harnesschoice.Pi}, deep, "", nil, harnesschoice.Pi, "relay"},
		{"pi named-key invalid to relay", harnesschoice.Choice{Kind: harnesschoice.Pi}, provider.Provider{Kind: provider.NamedKey, Name: "work"}, "key: work", nil, harnesschoice.Pi, "relay"},
		{"codex deepseek to chatgpt grant", harnesschoice.Choice{Kind: harnesschoice.Codex}, deep, "", chatGrant, harnesschoice.Codex, "prov:opaque"},
		{"codex deepseek no grant to pi", harnesschoice.Choice{Kind: harnesschoice.Codex}, deep, "", nil, harnesschoice.Pi, "relay"},
		{"codex named-key to chatgpt grant", harnesschoice.Choice{Kind: harnesschoice.Codex}, provider.Provider{Kind: provider.NamedKey, Name: "work"}, "key: work", chatGrant, harnesschoice.Codex, "prov:opaque"},
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
