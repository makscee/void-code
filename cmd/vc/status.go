package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/version"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{Use: "status", Short: "Show subscription, relay, and version status", Long: "Verify the current void-code subscription and show relay and version status.", RunE: runStatus}

func init() {
	statusCmd.Flags().Bool("json", false, "emit auth state as a single JSON object instead of the human-readable status (for callers with no terminal, e.g. the desktop app)")
	rootCmd.AddCommand(statusCmd)
}

// statusJSONRunner is an indirection so tests can swap the destination
// runStatus dispatches to without making a network call.
var statusJSONRunner = func(cfg config.Config, out io.Writer) error {
	return runStatusJSON(cfg, out)
}

var labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
var valueStyle = lipgloss.NewStyle().Bold(true)
var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))

// runStatus never treats the presence of a token file as authentication. The
// subscription endpoint is the authority for identity, budget, and rejection.
func runStatus(cmd *cobra.Command, _ []string) error {
	if cmd != nil {
		if jsonFlag, err := cmd.Flags().GetBool("json"); err == nil && jsonFlag {
			return statusJSONRunner(config.OSResolve(), os.Stdout)
		}
	}
	cfg := config.OSResolve()
	fmt.Printf("%s %s\n", labelStyle.Render("version:"), valueStyle.Render(version.Version))
	fmt.Printf("%s %s\n", labelStyle.Render("relay:  "), valueStyle.Render(cfg.RelayHost))
	fmt.Printf("%s %s\n", labelStyle.Render("runtime:"), valueStyle.Render("Pi"))
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), errorStyle.Render("not logged in"))
		return nil
	}
	// Same authority as `vc status --json`: one token must not get two verdicts
	// depending on which form of the command a human happened to run.
	me, err := auth.FetchMe(cfg.AccessCheckHost, strings.TrimSpace(token), &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), errorStyle.Render("token present but verification failed: "+err.Error()))
		return nil
	}
	identity := me.UserID
	if me.Email != "" {
		identity = me.Email
	}
	if identity == "" {
		identity = "(unknown)"
	}
	fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), valueStyle.Render("logged in as "+identity))
	fmt.Printf("%s %s\n", labelStyle.Render("token:  "), valueStyle.Render("~/.void-code/token"))
	if me.Pct != nil {
		fmt.Printf("%s %s\n", labelStyle.Render("budget: "), valueStyle.Render(formatBudgetLine(*me.Pct, me.ResetAt)))
	}
	return nil
}

func formatBudgetLine(pct float64, resetAt string) string {
	if resetAt == "" {
		return fmt.Sprintf("%.0f%% used", pct)
	}
	return fmt.Sprintf("%.0f%% used — resets %s", pct, fmtResetDate(resetAt))
}
func fmtResetDate(resetAt string) string {
	if t, err := time.Parse(time.RFC3339, resetAt); err == nil {
		return fmt.Sprintf("%s %d", t.Format("Jan"), t.Day())
	}
	if t, err := time.Parse("2006-01-02", resetAt[:min(10, len(resetAt))]); err == nil {
		return fmt.Sprintf("%s %d", t.Format("Jan"), t.Day())
	}
	if len(resetAt) >= 10 {
		return resetAt[:10]
	}
	return resetAt
}
