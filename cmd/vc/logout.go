package main

import (
	"fmt"

	"github.com/makscee/void-code/internal/auth"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Wipe cached credentials",
	Long:  `Remove ~/.void-code/token and the cached relay CA certificate.`,
	RunE:  runLogout,
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}

func runLogout(_ *cobra.Command, _ []string) error {
	if err := auth.Wipe(); err != nil {
		return fmt.Errorf("logout failed: %w", err)
	}
	fmt.Println("Logged out — credentials removed.")
	// Warn if the legacy cv token is still present so the user knows cv is still active.
	if auth.LegacyTokenExists() {
		fmt.Println("Note: cv login still active (~/.claudev/token). Run `claudev logout` if you want to clear that too.")
	}
	return nil
}
