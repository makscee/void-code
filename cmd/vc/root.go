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
	// rawModeFlag is set by --raw; skips the title/menu screen and passes the
	// controlling tty straight to Pi for tmux send-keys / daemon use.
	rawModeFlag bool
)

// brandStyle is the top-level banner style for vc.
var brandStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#7C3AED"))

// rootCmd is the entry-point Cobra command.  When invoked with no sub-command
// arguments it launches Pi with VC-managed subscription transport (handled in main.go).
var rootCmd = &cobra.Command{
	Use:   "vc [flags]",
	Short: "void-code — subscription console for Pi",
	Long: fmt.Sprintf(`%s

vc launches Pi with your void-code subscription authentication.
Running "vc" starts the Pi console; Pi owns model selection.

Sub-commands:
  login    Authenticate interactively or with device flow
  logout   Wipe cached credentials
  status   Show current auth / relay / version status
  update           Fetch the latest vc release and swap the binary
  desktop-session  Launch a caller-supplied private Node/Pi runtime

Non-interactive mode (--non-interactive, or automatically when stdin is not a
TTY) never opens a prompt: vc prints guidance and picks safe defaults so it
never hangs in scripts, daemons, or CI.

Run "vc <command> --help" for sub-command details.`,
		brandStyle.Render("void-code")),
	Example: `  # Launch the Pi console
  vc

  # Skip title screen and hand the terminal to the managed Pi session
  vc --raw

  # Never prompt; fail fast / print guidance instead of blocking (scripts, CI)
  vc --non-interactive doctor

  # Print VC's version
  vc --version`,
	// SilenceUsage hides the usage block on runtime errors — less noise for users.
	SilenceUsage: true,
	// Pi launch arguments are intentionally not a public VC surface. Model,
	// provider, and permission choices remain in Pi's native UI.
	Args: cobra.NoArgs,
	// When no sub-command is matched, RunE (in main.go) handles the spawn.
	RunE: runSpawn,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&versionFlag, "version", false, "Print version and exit")
	rootCmd.PersistentFlags().BoolVar(&nonInteractiveFlag, "non-interactive", false, "Never prompt; print guidance and pick safe defaults (also implied when stdin is not a TTY)")
	rootCmd.Flags().BoolVar(&rawModeFlag, "raw", false, "Skip title screen; pass tty straight to Pi (for tmux/daemon use)")
	// Disable the auto-generated 'completion' sub-command — not needed for v0.
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

// Execute is the single entry-point called from main().
func Execute() {
	handleExecuteError(rootCmd.Execute())
}

func handleExecuteError(err error) {
	if err == nil {
		return
	}
	if exitErr, ok := err.(interface{ ExitCode() int }); ok && exitErr.ExitCode() >= 0 {
		os.Exit(exitErr.ExitCode())
	}
	// Cobra already prints the error; exit with code 1.
	os.Exit(1)
}
