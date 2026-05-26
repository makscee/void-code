package main

import (
	"fmt"

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
	// TODO(VCD-6): implement semver probe against GH releases version.json + atomic swap.
	fmt.Println("vc update: stub — implemented in VCD-6")
	return nil
}
