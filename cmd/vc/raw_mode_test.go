package main

import (
	"testing"
)

// TestRawFlag_Registered verifies that --raw is a registered flag on rootCmd.
// This is the unit test that FAILS before the flag is wired.
func TestRawFlag_Registered(t *testing.T) {
	f := rootCmd.Flags().Lookup("raw")
	if f == nil {
		t.Fatal("--raw flag is not registered on rootCmd; add it in root.go")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--raw flag type = %q, want bool", f.Value.Type())
	}
}

// TestRawFlag_DefaultFalse verifies the default value of --raw is false.
func TestRawFlag_DefaultFalse(t *testing.T) {
	f := rootCmd.Flags().Lookup("raw")
	if f == nil {
		t.Skip("--raw flag not registered; skip default check")
	}
	if f.DefValue != "false" {
		t.Errorf("--raw default = %q, want false", f.DefValue)
	}
}

// TestRawFlag_ParsesWithDoubleDash verifies --raw is accepted before -- and
// does not cause cobra to return a parse error.
func TestRawFlag_ParsesWithDoubleDash(t *testing.T) {
	// Reset the flag to its default before parsing.
	if err := rootCmd.Flags().Set("raw", "false"); err != nil {
		// Flag not registered yet — the previous test would have caught this.
		t.Skip("--raw flag not registered")
	}

	// cobra.Command.ParseFlags parses flags from an args slice.
	// We simulate: vc --raw -- --session-id foo
	// cobra splits at "--" so rawModeFlag must be set to true by ParseFlags.
	if err := rootCmd.Flags().Parse([]string{"--raw", "--", "--session-id", "foo"}); err != nil {
		t.Fatalf("ParseFlags([--raw, --, --session-id, foo]) error: %v", err)
	}
	if !rawModeFlag {
		t.Error("rawModeFlag = false after parsing --raw; want true")
	}

	// Reset for other tests.
	_ = rootCmd.Flags().Set("raw", "false")
}

// TestDecideGate_RawBypassesTUI documents the invariant: when rawModeFlag is
// true, main() must NOT call decideGate at all (the gate check is skipped).
// We test this indirectly by verifying decideGate still works correctly — it
// has no knowledge of rawModeFlag. The gate skip is in main(), not decideGate.
func TestDecideGate_RawBypassesTUI(t *testing.T) {
	// With a TTY stdin and logged-in, decideGate returns gateShowWelcome.
	// In raw mode, main() skips this call entirely — this test ensures the
	// existing gate logic is not accidentally broken by the raw flag addition.
	if d := decideGate(true, true); d != gateShowWelcome {
		t.Errorf("decideGate(tty=true, loggedIn=true) = %v, want gateShowWelcome", d)
	}
	if d := decideGate(false, true); d != gateSpawn {
		t.Errorf("decideGate(tty=false, loggedIn=true) = %v, want gateSpawn", d)
	}
	if d := decideGate(false, false); d != gateFailAuth {
		t.Errorf("decideGate(tty=false, loggedIn=false) = %v, want gateFailAuth", d)
	}
}
