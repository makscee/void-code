package main

import (
	"fmt"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/version"
	"github.com/spf13/cobra"
	"strings"
)

var statusCmd = &cobra.Command{Use: "status", Short: "Show subscription, relay, and version status", RunE: runStatus}

func init() { rootCmd.AddCommand(statusCmd) }
func runStatus(_ *cobra.Command, _ []string) error {
	cfg := config.OSResolve()
	fmt.Printf("version: %s\nrelay:   %s\nruntime: Pi\n", version.Version, cfg.RelayHost)
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		fmt.Println("auth:    not logged in")
		return nil
	}
	fmt.Println("auth:    token present")
	return nil
}
