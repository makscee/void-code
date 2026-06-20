package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
	"github.com/makscee/void-code/internal/version"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show auth, relay, and version status",
	Long:  `Print the current authentication state, relay endpoint, and vc version.`,
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

var labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
var valueStyle = lipgloss.NewStyle().Bold(true)
var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444"))

// meResponse is the subset of GET /v1/auth/me we care about.
type meResponse struct {
	Slug  string `json:"slug"`
	Email string `json:"email"`
}

func runStatus(_ *cobra.Command, _ []string) error {
	cfg := config.OSResolve()

	// Print version and relay unconditionally.
	fmt.Printf("%s %s\n", labelStyle.Render("version:"), valueStyle.Render(version.Version))
	fmt.Printf("%s %s\n", labelStyle.Render("relay:  "), valueStyle.Render(cfg.RelayHost))
	active := provider.Load()
	fmt.Printf("%s %s\n", labelStyle.Render("provider:"), valueStyle.Render(active.Label()))

	// Load token with legacy cv fallback and silent migration.
	token, migrated, loadErr := auth.Load()
	if loadErr != nil {
		fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), errorStyle.Render("not logged in"))
		fmt.Printf("%s %s\n", labelStyle.Render("token:  "), errorStyle.Render("no token at ~/.void-code/token (or ~/.claudev/token)"))
		return nil
	}

	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), errorStyle.Render("token file empty"))
		return nil
	}

	tokenLabel := "~/.void-code/token"
	if migrated {
		tokenLabel = "~/.claudev/token (legacy cv — will migrate on next spawn)"
	}

	// Call /v1/auth/me to get identity.
	identity, err := fetchMe(cfg.AuthHost, token)
	if err != nil {
		// Token present but /me failed — show partial info without failing hard.
		fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), errorStyle.Render("token present but verify failed: "+err.Error()))
		fmt.Printf("%s %s\n", labelStyle.Render("token:  "), valueStyle.Render(tokenLabel))
		return nil
	}

	// Display name: prefer slug, fall back to email, fall back to generic.
	displayName := identity.Slug
	if displayName == "" {
		displayName = identity.Email
	}
	if displayName == "" {
		displayName = "(unknown)"
	}

	fmt.Printf("%s %s\n", labelStyle.Render("auth:   "),
		valueStyle.Render("logged in as "+displayName))
	fmt.Printf("%s %s\n", labelStyle.Render("token:  "), valueStyle.Render(tokenLabel))

	// VCD-49: fetch budget info from /v1/vc/me (pct + reset_at only — no dollar values).
	// Degrade-safe: if FetchMe fails or pct absent/nil, skip the budget line silently.
	vcMeClient := &http.Client{Timeout: 5 * time.Second}
	if me, err := auth.FetchMe(cfg.AuthHost, token, vcMeClient); err == nil && me.Pct != nil {
		line := formatBudgetLine(*me.Pct, me.ResetAt)
		fmt.Printf("%s %s\n", labelStyle.Render("budget: "), valueStyle.Render(line))
	}

	return nil
}

// formatBudgetLine formats the budget status line shown in `vc status`.
// Operator constraint (2026-05-30): percentages only — NO dollar values.
// Format: "N% used — resets Jun 1" (or just "N% used" when resetAt absent).
func formatBudgetLine(pct float64, resetAt string) string {
	base := fmt.Sprintf("%.0f%% used", pct)
	if resetAt == "" {
		return base
	}
	// Parse reset date to produce "Mon D" (e.g. "Jun 1").
	resetStr := fmtResetDate(resetAt)
	return fmt.Sprintf("%s — resets %s", base, resetStr)
}

// fmtResetDate parses an ISO-8601 date string and returns "Mon D" (e.g. "Jun 1").
// Falls back to the first 10 chars (YYYY-MM-DD) on parse error.
func fmtResetDate(resetAt string) string {
	t, err := time.Parse(time.RFC3339, resetAt)
	if err != nil {
		// Try date-only fallback.
		t, err = time.Parse("2006-01-02", resetAt[:min(10, len(resetAt))])
		if err != nil {
			if len(resetAt) >= 10 {
				return resetAt[:10]
			}
			return resetAt
		}
	}
	return fmt.Sprintf("%s %d", t.Format("Jan"), t.Day())
}

// fetchMe calls GET /v1/auth/me and returns the user's slug + email.
func fetchMe(authHost, token string) (meResponse, error) {
	url := strings.TrimRight(authHost, "/") + "/v1/auth/me"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return meResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return meResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return meResponse{}, fmt.Errorf("auth/me returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return meResponse{}, err
	}

	var me meResponse
	if err := json.Unmarshal(body, &me); err != nil {
		return meResponse{}, err
	}
	return me, nil
}
