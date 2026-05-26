package main

import (
	"fmt"

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
	// TODO(VCD-3): wipe ~/.void-code/token + ~/.void-code/relay-ca.pem
	fmt.Println("vc logout: stub — implemented in VCD-3")
	return nil
}
