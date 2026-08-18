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
	return nil
}
