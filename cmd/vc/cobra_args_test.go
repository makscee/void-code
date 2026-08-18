package main

import "testing"

// TestRootCmd_RejectsPositionalArgs verifies root launch has no arbitrary
// forwarding surface.
func TestRootCmd_RejectsPositionalArgs(t *testing.T) {
	if err := rootCmd.ValidateArgs([]string{"dev-VCD57-paste"}); err == nil {
		t.Fatal("root command accepted arbitrary positional Pi argument")
	}
}

// TestRootCmd_RejectsPiAuthorityArgs keeps model/provider and permission choice
// inside Pi's native UI rather than allowing the VC launcher to forward it.
func TestRootCmd_RejectsPiAuthorityArgs(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "user-choice"},
		{"--provider", "foreign"},
		{"--permission-mode", "bypassPermissions"},
		{"--dangerously-skip-permissions"},
		{"--", "--debug"},
	} {
		if err := rootCmd.ValidateArgs(args); err == nil {
			t.Fatalf("ValidateArgs(%q) accepted forbidden Pi forwarding", args)
		}
	}
}
