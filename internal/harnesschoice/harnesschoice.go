// Package harnesschoice persists which local coding harness vc launches.
package harnesschoice

import (
	"strings"

	"github.com/makscee/void-code/internal/config"
)

// Kind enumerates supported local harnesses.
type Kind int

const (
	Claude Kind = iota // Claude Code
	Codex              // OpenAI Codex CLI
	Pi                 // Pi coding agent
)

// Choice is the active harness selection.
type Choice struct {
	Kind Kind
}

// Parse decodes the persisted string form. Unknown/empty values default to Pi.
func Parse(s string) Choice {
	switch strings.TrimSpace(s) {
	case "claude":
		return Choice{Kind: Claude}
	case "codex":
		return Choice{Kind: Codex}
	default:
		return Choice{Kind: Pi}
	}
}

// String is the persisted form.
func (c Choice) String() string {
	switch c.Kind {
	case Claude:
		return "claude"
	case Codex:
		return "codex"
	default:
		return "pi"
	}
}

// Label is the human-facing menu/status label.
func (c Choice) Label() string {
	switch c.Kind {
	case Claude:
		return "Claude Code"
	case Codex:
		return "OpenAI Codex"
	default:
		return "Pi"
	}
}

const configKey = "active_harness"

// Load reads the active harness from the vc config file. Missing/invalid values
// degrade to Pi, matching the default installer path.
func Load() Choice {
	kv, err := config.ReadConfigFile()
	if err != nil {
		return Choice{Kind: Pi}
	}
	return Parse(kv[configKey])
}

// Save persists the active harness to the vc config file.
func Save(c Choice) error {
	return config.WriteConfigFile(map[string]string{configKey: c.String()})
}
