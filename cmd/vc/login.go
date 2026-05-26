package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var loginDeviceFlag bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with void-code (access code or device flow)",
	Long: `Authenticate with void-code.

Default: access-code flow — reads VC_CODE from env or prompts interactively.
  VC_CODE=ABCD-EFGH vc login

Device flow (--device): opens a browser URL + polls until approved.
  vc login --device

Credentials are written to ~/.void-code/token (mode 0600).`,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().BoolVar(&loginDeviceFlag, "device", false, "Use device-code flow instead of access-code")
	rootCmd.AddCommand(loginCmd)
}

func runLogin(_ *cobra.Command, _ []string) error {
	// TODO(VCD-3): implement full auth flow (code-exchange + device flow).
	if loginDeviceFlag {
		fmt.Println("vc login --device: stub — implemented in VCD-3")
	} else {
		fmt.Println("vc login: stub — implemented in VCD-3")
	}
	return nil
}
