// Package runtimechoice keeps which runtime `vc` launches: Pi or Codex.
//
// The choice lives under the `runtime` key of ~/.void-code/config. The older
// `active_harness` key is never read: configs have carried codex or claude
// there since June, from a selector that no longer exists, and nobody may get
// a different runtime because of it (spec 2026-09-29-vc-runtime-choice-codex).
package runtimechoice

import (
	"fmt"
	"strings"

	"github.com/makscee/void-code/internal/config"
)

// Runtime is a launchable runtime, in the form it is persisted.
type Runtime string

const (
	// Pi is the default runtime, and the one used whenever nothing was chosen.
	Pi Runtime = "pi"
	// Codex is OpenAI's Codex CLI, installed and pinned by vc.
	Codex Runtime = "codex"
)

// configKey is the key in ~/.void-code/config that holds the choice.
const configKey = "runtime"

// Label is the runtime's name as people read it.
func (r Runtime) Label() string {
	switch r {
	case Pi:
		return "Pi"
	case Codex:
		return "Codex"
	}
	return string(r)
}

// Parse accepts exactly "pi" or "codex", surrounding space aside. Anything
// else, legacy harness names included, is an error rather than a default.
func Parse(s string) (Runtime, error) {
	value := strings.TrimSpace(s)
	switch Runtime(value) {
	case Pi:
		return Pi, nil
	case Codex:
		return Codex, nil
	}
	return "", fmt.Errorf("unknown runtime %q: expected pi or codex", value)
}

// Load returns the saved runtime and whether one was saved. With nothing saved
// it returns (Pi, false, nil). A saved value that is not a runtime is an error:
// a hand-edited typo is not a choice.
func Load() (Runtime, bool, error) {
	kv, err := config.ReadConfigFile()
	if err != nil {
		return Pi, false, err
	}
	value, ok := kv[configKey]
	if !ok || strings.TrimSpace(value) == "" {
		return Pi, false, nil
	}
	r, err := Parse(value)
	if err != nil {
		return Pi, false, fmt.Errorf("~/.void-code/config: %w", err)
	}
	return r, true, nil
}

// Save persists r and keeps every other key of the config file. An unknown
// runtime is refused before the file is touched.
func Save(r Runtime) error {
	if r != Pi && r != Codex {
		return fmt.Errorf("unknown runtime %q: expected pi or codex", string(r))
	}
	return config.WriteConfigFile(map[string]string{configKey: string(r)})
}
