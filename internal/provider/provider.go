// Package provider models the active "where does claude's auth come from" choice
// and persists it to the vc config file. Four kinds: Relay (default, void-relay),
// NamedKey (a saved BYO OAuth token, direct to Anthropic), Plain (native CC auth),
// RelayProvider (a server-granted provider routed via relay, VCD-72).
package provider

import (
	"strings"

	"github.com/makscee/void-code/internal/config"
)

// Kind enumerates the auth-source kinds.
type Kind int

const (
	Relay         Kind = iota // void-relay proxy + pool token (default)
	NamedKey                  // a saved OAuth token, direct to Anthropic
	Plain                     // native Claude Code auth, no injection
	RelayProvider             // a server-granted provider routed via relay; carries ID, sends x-void-provider
)

// Provider is the active auth-source selection.
// Name is only set for NamedKey. ID is only set for RelayProvider.
type Provider struct {
	Kind Kind
	Name string // NamedKey only
	ID   string // RelayProvider only
}

// Parse decodes the persisted string form. Unknown/empty → Relay (safe default).
//
//	"relay"       → Relay
//	"plain"       → Plain
//	"key:<name>"  → NamedKey{Name:<name>}
//	"prov:<id>"   → RelayProvider{ID:<id>}  (empty id → Relay)
func Parse(s string) Provider {
	s = strings.TrimSpace(s)
	switch {
	case s == "plain":
		return Provider{Kind: Plain}
	case strings.HasPrefix(s, "prov:"):
		id := s[len("prov:"):]
		if id == "" {
			return Provider{Kind: Relay}
		}
		return Provider{Kind: RelayProvider, ID: id}
	case strings.HasPrefix(s, "key:"):
		return Provider{Kind: NamedKey, Name: s[len("key:"):]}
	default:
		return Provider{Kind: Relay}
	}
}

// String is the persisted form (inverse of Parse).
func (p Provider) String() string {
	switch p.Kind {
	case Plain:
		return "plain"
	case NamedKey:
		return "key:" + p.Name
	case RelayProvider:
		return "prov:" + p.ID
	default:
		return "relay"
	}
}

// Label is the human-facing menu/status label.
func (p Provider) Label() string {
	switch p.Kind {
	case Plain:
		return "Plain Claude Code"
	case NamedKey:
		return "key: " + p.Name
	case RelayProvider:
		return p.ID
	default:
		return "Relay (void-relay)"
	}
}

// configKey is the key under which the active provider is persisted.
const configKey = "active_provider"

// labelKey is the key under which the active provider's display label is persisted.
// Written at selection time so the statusline renderer never needs a network call.
const labelKey = "active_provider_label"

// Load reads the active provider from the vc config file. Any error or absent
// key degrades to Relay (the safe default).
func Load() Provider {
	kv, err := config.ReadConfigFile()
	if err != nil {
		return Provider{Kind: Relay}
	}
	return Parse(kv[configKey])
}

// Save persists the active provider to the vc config file.
func Save(p Provider) error {
	return config.WriteConfigFile(map[string]string{configKey: p.String()})
}

// SaveLabel persists the provider's display label alongside its id.
// Called at selection time so the statusline renderer can read the label without
// a network round-trip.
func SaveLabel(label string) error {
	return config.WriteConfigFile(map[string]string{labelKey: label})
}

// LoadLabel returns the persisted display label for the active provider.
// Falls back to a best-effort derived label when the key is absent (existing
// users who selected before this was added). Never returns an empty string.
func LoadLabel() string {
	kv, err := config.ReadConfigFile()
	if err != nil {
		return Load().Label()
	}
	if label, ok := kv[labelKey]; ok && label != "" {
		return label
	}
	// Backfill: derive from active_provider id.
	return Parse(kv[configKey]).Label()
}

// GrantedEntry is a server-granted relay-provider entry (id + display name).
// Mirrors welcome.ProviderRowInfo; kept here so provider can reconcile labels
// without importing welcome (avoids circular deps).
type GrantedEntry struct {
	ID   string
	Name string
}

// FriendlyLabel returns the menu-style friendly label for a provider, mirroring
// the label logic in welcome.buildProviderRows.  It is the single source of truth
// for label strings used by both selection and reconcile paths.
//
//   - RelayProvider: looked up in granted list → "Relay: <name>" (name falls back to id).
//     Returns "" when the id is not present in the list (caller must guard).
//   - NamedKey / Plain / Relay: derived purely from the Provider itself.
func FriendlyLabel(p Provider, granted []GrantedEntry, keyNames []string) string {
	switch p.Kind {
	case RelayProvider:
		for _, g := range granted {
			if g.ID == p.ID {
				name := g.Name
				if name == "" {
					name = g.ID
				}
				return "Relay: " + name
			}
		}
		return "" // not found — caller must not persist
	case NamedKey:
		return p.Label() // "key: <name>"
	case Plain:
		return p.Label() // "Plain Claude Code"
	default:
		return p.Label() // "Relay (void-relay)"
	}
}

// ReconcileLabel refreshes the persisted active_provider_label from the live
// granted-providers list fetched at launch.  Call this ONLY on a successful
// fetch (non-empty granted list or confirmed-empty from server).
//
// Guards:
//   - For RelayProvider: only persists when the active id is found in granted.
//     If not found (provider revoked / list empty on error), label is left as-is.
//   - For all other kinds: always refreshes (label is derivable without the list).
//   - Never persists an empty string.
func ReconcileLabel(granted []GrantedEntry, keyNames []string) error {
	active := Load()
	label := FriendlyLabel(active, granted, keyNames)
	if label == "" {
		// RelayProvider not found in granted list — do not clobber.
		return nil
	}
	return SaveLabel(label)
}
