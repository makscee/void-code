package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	// versionFlag is set by the --version flag on rootCmd.
	versionFlag bool
)

// brandStyle is the top-level banner style for vc.
var brandStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#7C3AED"))

// rootCmd is the entry-point Cobra command.  When invoked with no sub-command
// arguments it spawns claude with relay env (handled in main.go).
var rootCmd = &cobra.Command{
	Use:   "vc [flags] [-- claude-args...]",
	Short: "void-code — relay harness for Claude Code",
	Long: fmt.Sprintf(`%s

vc wraps the claude CLI with void-relay authentication.
Running "vc" with no sub-command launches claude with relay env injected.

Sub-commands:
  login    Authenticate with an access code or device flow
  logout   Wipe cached credentials
  status   Show current auth / relay / version status
  update   Fetch the latest vc release and swap the binary

Run "vc <command> --help" for sub-command details.`,
		brandStyle.Render("void-code")),
	Example: `  # Launch claude normally
  vc

  # Pass flags directly to claude using -- (double-dash terminator)
  vc -- --dangerously-skip-permissions
  vc -- --debug --verbose

  # vc flags before --, claude flags after
  vc --version
  vc -- --help`,
	// SilenceUsage hides the usage block on runtime errors — less noise for users.
	SilenceUsage: true,
	// When no sub-command is matched, RunE (in main.go) handles the spawn.
	RunE: runSpawn,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&versionFlag, "version", false, "Print version and exit")
	// Disable the auto-generated 'completion' sub-command — not needed for v0.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

// Execute is the single entry-point called from main().
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// Cobra already prints the error; exit with code 1.
		os.Exit(1)
	}
}
