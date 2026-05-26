package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
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

func runStatus(_ *cobra.Command, _ []string) error {
	cfg := config.OSResolve()

	// TODO(VCD-3): read actual auth state from ~/.void-code/token
	fmt.Printf("%s %s\n", labelStyle.Render("version:"), valueStyle.Render(version.Version))
	fmt.Printf("%s %s\n", labelStyle.Render("relay:  "), valueStyle.Render(cfg.RelayHost))
	fmt.Printf("%s %s\n", labelStyle.Render("auth:   "), valueStyle.Render("not logged in (stub — VCD-3)"))
	return nil
}
