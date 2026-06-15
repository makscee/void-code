package main

import "testing"

// TestNonInteractive_FlagForcesTrue verifies that setting --non-interactive
// makes nonInteractive() report true regardless of the stdin TTY state.
func TestNonInteractive_FlagForcesTrue(t *testing.T) {
	prev := nonInteractiveFlag
	t.Cleanup(func() { nonInteractiveFlag = prev })

	nonInteractiveFlag = true
	if !nonInteractive() {
		t.Error("nonInteractive() = false with --non-interactive set; want true")
	}
}

// TestNonInteractive_NonTTYStdin verifies that when stdin is not a TTY (the case
// under `go test`, whose stdin is a pipe/devnull), nonInteractive() is true even
// with the flag unset. This is the auto-detect path the directive requires.
func TestNonInteractive_NonTTYStdin(t *testing.T) {
	prev := nonInteractiveFlag
	t.Cleanup(func() { nonInteractiveFlag = prev })

	nonInteractiveFlag = false
	// Test harness stdin is not a terminal, so interactiveStdin() is false.
	if interactiveStdin() {
		t.Skip("test stdin is unexpectedly a TTY — cannot exercise the non-TTY path")
	}
	if !nonInteractive() {
		t.Error("nonInteractive() = false with non-TTY stdin and flag unset; want true")
	}
}

// TestNonInteractiveFlag_Registered verifies the persistent --non-interactive
// flag is wired on rootCmd as a bool defaulting to false.
func TestNonInteractiveFlag_Registered(t *testing.T) {
	f := rootCmd.PersistentFlags().Lookup("non-interactive")
	if f == nil {
		t.Fatal("--non-interactive flag is not registered on rootCmd")
	}
	if f.Value.Type() != "bool" {
		t.Errorf("--non-interactive type = %q, want bool", f.Value.Type())
	}
	if f.DefValue != "false" {
		t.Errorf("--non-interactive default = %q, want false", f.DefValue)
	}
}

// TestHasNonInteractiveArg verifies the early os.Args scan used by the bare-launch
// gate (before cobra parses flags). It must detect the flag and stop at "--".
func TestHasNonInteractiveArg(t *testing.T) {
	prev := osArgs
	t.Cleanup(func() { osArgs = prev })

	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"present", []string{"vc", "--non-interactive", "doctor"}, true},
		{"absent", []string{"vc", "doctor"}, false},
		{"after-double-dash", []string{"vc", "--", "--non-interactive"}, false},
		{"before-double-dash", []string{"vc", "--non-interactive", "--", "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			osArgs = tc.args
			if got := hasNonInteractiveArg(); got != tc.want {
				t.Errorf("hasNonInteractiveArg(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
