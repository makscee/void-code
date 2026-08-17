package main

import (
	"fmt"
	"github.com/makscee/void-code/internal/pibin"
	"github.com/spf13/cobra"
)

var doctorFixFlag bool
var doctorCmd = &cobra.Command{Use: "doctor", Short: "Check VC and Pi setup", RunE: func(_ *cobra.Command, _ []string) error { return runDoctor() }}

func init() {
	doctorCmd.Flags().BoolVar(&doctorFixFlag, "fix", false, "Reserved; doctor is report-only")
	rootCmd.AddCommand(doctorCmd)
}
func runDoctor() error {
	if !piIsInstalled() {
		fmt.Println(pibin.MissingMessage())
		return nil
	}
	path, err := pibin.Resolve()
	if err != nil {
		return err
	}
	fmt.Printf("Pi runtime: %s\n", path)
	return nil
}
