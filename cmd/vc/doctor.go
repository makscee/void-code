package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/pibin"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{Use: "doctor", Short: "Check VC and Pi setup", RunE: func(_ *cobra.Command, _ []string) error { return runDoctor() }}

func init() { rootCmd.AddCommand(doctorCmd) }

type doctorCheck struct {
	name, message string
	ok            bool
}

func runDoctor() error {
	checks := []doctorCheck{checkPi(), checkToken(), checkVCPath(), checkManagedExtension(), checkManagedSearch()}
	for _, check := range checks {
		mark := "✓"
		if !check.ok {
			mark = "✗"
		}
		fmt.Printf("%s %s: %s\n", mark, check.name, check.message)
	}
	return nil
}
func checkPi() doctorCheck {
	path, err := pibin.Resolve()
	if err != nil {
		return doctorCheck{"Pi runtime", pibin.MissingMessage(), false}
	}
	return doctorCheck{"Pi runtime", path, true}
}
func checkToken() doctorCheck {
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return doctorCheck{"authentication", "not logged in; run vc login", false}
	}
	return doctorCheck{"authentication", "token present (run vc status to verify)", true}
}
func checkVCPath() doctorCheck {
	path, err := os.Executable()
	if err != nil || !filepath.IsAbs(path) {
		return doctorCheck{"VC bootstrap path", "cannot resolve current executable", false}
	}
	return doctorCheck{"VC bootstrap path", path, true}
}
func checkManagedExtension() doctorCheck {
	path := filepath.Join(piAgentDir(), "extensions", "void-code.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		return doctorCheck{"managed extension", "not installed; VC will install it on launch", false}
	}
	return doctorCheck{"managed extension", path, hasManagedPiExtensionMarker(data, managedPiExtensionMarker)}
}
func checkManagedSearch() doctorCheck {
	current, foreign, err := inspectManagedWebSearchPackage(managedWebSearchPackagePath())
	if err != nil {
		return doctorCheck{"managed web search", err.Error(), false}
	}
	if foreign {
		return doctorCheck{"managed web search", "foreign package at managed path", false}
	}
	if !current {
		return doctorCheck{"managed web search", "not installed; VC will install it on launch", false}
	}
	present, err := inspectManagedPackageSetting(managedWebSearchPackagePath())
	if err != nil || !present {
		return doctorCheck{"managed web search", "package is not enabled in Pi settings", false}
	}
	return doctorCheck{"managed web search", "installed", true}
}
