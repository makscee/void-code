// Package compat enforces the supported harness/provider matrix.
package compat

import (
	"strings"

	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/provider"
)

// ProviderClass is the compatibility class used by the matrix.
type ProviderClass int

const (
	ProviderInvalid ProviderClass = iota
	ProviderDeepSeek
	ProviderChatGPT
)

// Grant is a safe relay-provider descriptor fetched from auth.
type Grant struct {
	ID    string
	Name  string
	Type  string
	Label string
}

// Decision is the reconciled matrix row.
type Decision struct {
	Harness       harnesschoice.Choice
	Provider      provider.Provider
	ProviderLabel string
	Changed       bool
	Warning       string
}

// ClassifyProvider maps provider selections to the only matrix classes.
// Relay is the built-in DeepSeek row. RelayProvider is classified by safe
// provider type first, then legacy id/name/label substrings; Plain and NamedKey
// are not valid matrix rows.
func ClassifyProvider(p provider.Provider, label string, grants []Grant) ProviderClass {
	switch p.Kind {
	case provider.Relay:
		return ProviderDeepSeek
	case provider.RelayProvider:
		fields := []string{p.ID, label}
		for _, g := range grants {
			if g.ID == p.ID {
				if class, ok := classifyType(g.Type); ok {
					return class
				}
				fields = append(fields, g.ID, g.Name, g.Label)
			}
		}
		return classifyFields(fields)
	default:
		return ProviderInvalid
	}
}

func classifyType(t string) (ProviderClass, bool) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "openai-codex-oauth":
		return ProviderChatGPT, true
	case "deepseek":
		return ProviderDeepSeek, true
	default:
		return ProviderInvalid, false
	}
}

func classifyFields(fields []string) ProviderClass {
	for _, f := range fields {
		v := strings.ToLower(strings.TrimSpace(f))
		if strings.Contains(v, "chatgpt") || strings.Contains(v, "openai") || strings.Contains(v, "codex") {
			return ProviderChatGPT
		}
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(strings.TrimSpace(f)), "deepseek") {
			return ProviderDeepSeek
		}
	}
	return ProviderInvalid
}

// FirstChatGPT returns the first granted ChatGPT/OpenAI/Codex relay provider.
func FirstChatGPT(grants []Grant) (provider.Provider, string, bool) {
	for _, g := range grants {
		p := provider.Provider{Kind: provider.RelayProvider, ID: g.ID}
		if ClassifyProvider(p, g.Label, grants) == ProviderChatGPT {
			return p, "ChatGPT relay", true
		}
	}
	return provider.Provider{}, "", false
}

// Reconcile returns a safe supported matrix row, preserving compatible choices.
// Matrix: Claude=DeepSeek, Codex=ChatGPT, Pi=DeepSeek/ChatGPT.
func Reconcile(h harnesschoice.Choice, p provider.Provider, label string, grants []Grant) Decision {
	class := ClassifyProvider(p, label, grants)
	if label == "" {
		label = defaultLabel(p, class)
	}
	out := Decision{Harness: h, Provider: p, ProviderLabel: label}

	set := func(h2 harnesschoice.Choice, p2 provider.Provider, label2, warning string) Decision {
		return Decision{Harness: h2, Provider: p2, ProviderLabel: label2, Changed: h2.String() != h.String() || p2.String() != p.String() || label2 != label, Warning: warning}
	}

	switch h.Kind {
	case harnesschoice.Claude:
		if class == ProviderDeepSeek {
			return out
		}
		return set(h, provider.Provider{Kind: provider.Relay}, provider.Provider{Kind: provider.Relay}.Label(), "Claude Code supports DeepSeek only; switched provider to DeepSeek relay.")
	case harnesschoice.Codex:
		if class == ProviderChatGPT {
			return out
		}
		if chat, chatLabel, ok := FirstChatGPT(grants); ok {
			return set(h, chat, chatLabel, "OpenAI Codex supports ChatGPT only; switched provider to ChatGPT relay.")
		}
		return set(harnesschoice.Choice{Kind: harnesschoice.Pi}, provider.Provider{Kind: provider.Relay}, provider.Provider{Kind: provider.Relay}.Label(), "OpenAI Codex requires a granted ChatGPT relay provider; switched to Pi with DeepSeek relay.")
	default: // Pi
		if class == ProviderDeepSeek || class == ProviderChatGPT {
			return out
		}
		return set(harnesschoice.Choice{Kind: harnesschoice.Pi}, provider.Provider{Kind: provider.Relay}, provider.Provider{Kind: provider.Relay}.Label(), "Plain and named-key providers are not supported in vc harness mode; switched provider to DeepSeek relay.")
	}
}

func defaultLabel(p provider.Provider, class ProviderClass) string {
	switch class {
	case ProviderChatGPT:
		return "ChatGPT relay"
	case ProviderDeepSeek:
		return provider.Provider{Kind: provider.Relay}.Label()
	default:
		return p.Label()
	}
}
