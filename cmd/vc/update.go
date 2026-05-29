package main

import (
	"fmt"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/update"
	"github.com/makscee/void-code/internal/version"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Fetch the latest vc release and swap the binary",
	Long: `Check GitHub Releases for a newer version of vc.
If a newer version is found, download it and atomically replace the current binary.

Self-update uses a single-launch model (no banner + restart dance).`,
	RunE: runUpdate,
}

func init() {
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(_ *cobra.Command, _ []string) error {
	fmt.Printf("Current version: %s\nChecking for updates...\n", version.Version)

	// Derive the release base URL from config (respects VC_AUTH_HOST env override).
	cfg := config.OSResolve()
	baseURL := cfg.AuthHost + "/vc"

	updated, err := update.CheckAndUpdate(update.Options{
		Current: version.Version,
		BaseURL: baseURL,
	})
	if err != nil {
		return fmt.Errorf("update check failed: %w", err)
	}

	if updated {
		fmt.Println("vc updated successfully. Run vc again to use the new version.")
	} else {
		fmt.Println("Already up to date.")
	}
	return nil
}
