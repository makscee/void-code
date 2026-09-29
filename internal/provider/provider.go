// Package provider names the relay route buildPiSpawnEnv sets up for a Pi
// launch. vc always launches with Relay; the other kinds are left from the
// multi-harness era and only decide which VC_* variables a launch exports.
package provider

// Kind enumerates the auth-source kinds.
type Kind int

const (
	Relay         Kind = iota // DeepSeek relay default route
	NamedKey                  // a saved OAuth token, direct to Anthropic
	Plain                     // native Claude Code auth, no injection
	RelayProvider             // a server-granted provider routed via relay; carries ID, sends x-void-provider
)

// Provider is the auth-source a launch uses. ID is only set for RelayProvider.
type Provider struct {
	Kind Kind
	Name string // NamedKey only
	ID   string // RelayProvider only
}
