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
	return nil
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
