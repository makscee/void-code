// Package harnesschoice persists which local coding harness vc launches.
package harnesschoice

import (
	"strings"

	"github.com/makscee/void-code/internal/config"
)

// Kind enumerates supported local harnesses.
type Kind int

const (
	Claude Kind = iota // Claude Code, the historical default
	Pi                 // Pi coding agent
)

// Choice is the active harness selection.
type Choice struct {
	Kind Kind
}

// Parse decodes the persisted string form. Unknown/empty values default to Claude.
func Parse(s string) Choice {
	switch strings.TrimSpace(s) {
	case "pi":
		return Choice{Kind: Pi}
	default:
		return Choice{Kind: Claude}
	}
}

// String is the persisted form.
func (c Choice) String() string {
	if c.Kind == Pi {
		return "pi"
	}
	return "claude"
}

// Label is the human-facing menu/status label.
func (c Choice) Label() string {
	if c.Kind == Pi {
		return "Pi"
	}
	return "Claude Code"
}

const configKey = "active_harness"

// Load reads the active harness from the vc config file. Missing/invalid values
// degrade to Claude Code for backward compatibility.
func Load() Choice {
	kv, err := config.ReadConfigFile()
	if err != nil {
		return Choice{Kind: Claude}
	}
	return Parse(kv[configKey])
}

// Save persists the active harness to the vc config file.
func Save(c Choice) error {
	return config.WriteConfigFile(map[string]string{configKey: c.String()})
}
