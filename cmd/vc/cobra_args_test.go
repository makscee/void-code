package main

import (
	"strings"
	"testing"
)

// TestRootCmd_ArbitraryArgs verifies that rootCmd accepts arbitrary positional
// args without cobra rejecting them as "unknown subcommand".
//
// Regression: version strings like "dev-VCD57-paste" contain dashes and look
// like potential subcommand names to cobra when passed as positional args.
// Without Args: cobra.ArbitraryArgs, cobra says "unknown command "dev-VCD57-paste"
// for "vc"" instead of passing the arg to runSpawn (and on to claude).
func TestRootCmd_ArbitraryArgs(t *testing.T) {
	// rootCmd is the package-level cobra command defined in root.go.
	// Simulate cobra routing: traverse args against the command tree.
	// We use cobra's own traversal logic rather than Execute() so we don't
	// actually spawn claude or touch network — just confirm routing succeeds.

	// cobra.Command.Find() traverses the command tree for the given args and
	// returns (subCmd, remainingArgs, error). For arbitrary args to work, it
	// must return rootCmd itself with the args intact, no error.
	cmd, args, err := rootCmd.Find([]string{"dev-VCD57-paste"})
	if err != nil {
		t.Fatalf("rootCmd.Find([dev-VCD57-paste]) err = %v; cobra must not reject it as unknown subcommand", err)
	}
	if cmd.Name() != "vc" {
		t.Errorf("Find resolved to cmd %q, want root cmd %q", cmd.Name(), rootCmd.Name())
	}
	if len(args) != 1 || args[0] != "dev-VCD57-paste" {
		t.Errorf("Find remaining args = %v, want [dev-VCD57-paste]", args)
	}
}

// TestRootCmd_ValidateArgs verifies the Args validator accepts arbitrary args.
func TestRootCmd_ValidateArgs(t *testing.T) {
	// ValidateArgs is the cobra function that runs the Args validator.
	// If rootCmd.Args is nil or cobra.ArbitraryArgs, this must not error.
	err := rootCmd.ValidateArgs([]string{"dev-VCD57", "--dangerously-skip-permissions"})
	if err != nil {
		t.Fatalf("ValidateArgs rejected positional args: %v\n  rootCmd.Args must be cobra.ArbitraryArgs", err)
	}
}

// TestRootCmd_VersionStringNotSubcommand verifies common version-like strings
// are NOT misrouted as subcommands.
func TestRootCmd_VersionStringNotSubcommand(t *testing.T) {
	versionStrings := []string{
		"dev-VCD57-paste",
		"dev-VCD57",
		"v0.1.8",
		"v1.2.3",
	}
	for _, v := range versionStrings {
		_, _, err := rootCmd.Find([]string{v})
		if err != nil && strings.Contains(err.Error(), "unknown command") {
			t.Errorf("rootCmd.Find([%q]) returned 'unknown command' error: %v", v, err)
		}
	}
}
