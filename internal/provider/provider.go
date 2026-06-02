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
