package main

import "os"

// nonInteractiveFlag is set by the persistent --non-interactive flag on rootCmd.
// When true (or when stdin is not a TTY) vc never opens a bubbletea prompt and
// never blocks waiting for input — it prints guidance and picks safe defaults.
// This exists because blocking prompts have repeatedly hung scripts, daemons,
// and CI runs that drive vc with a non-TTY stdin.
var nonInteractiveFlag bool

// osArgs indirects os.Args so the early arg scan is testable. Production reads
// the real process args; tests override it.
var osArgs = os.Args

// interactiveStdin reports whether stdin is an interactive terminal.
// Wraps isStdinTTY (cmd/vc/main.go) so the intent reads clearly at call sites.
func interactiveStdin() bool {
	return isStdinTTY()
}

// nonInteractive is the single predicate commands consult before opening a
// prompt: true when --non-interactive was passed OR stdin is not a TTY. In
// either case vc must not block on input.
func nonInteractive() bool {
	return nonInteractiveFlag || !interactiveStdin()
}

// hasNonInteractiveArg scans the process argv for --non-interactive before cobra
// parses. Kept for callers that have no argv of their own; a caller that does
// should pass it to hasNonInteractiveArgIn instead of assigning osArgs.
func hasNonInteractiveArg() bool { return hasNonInteractiveArgIn(osArgs) }

// hasNonInteractiveArgIn scans the given argv for --non-interactive. The
// bare-launch gate runs before rootCmd.Execute(), so the cobra flag value is not
// yet populated there — this mirrors the early --raw scan. Stops at "--"
// (everything after is for claude).
func hasNonInteractiveArgIn(args []string) bool {
	for _, a := range args[1:] {
		if a == "--" {
			return false
		}
		if a == "--non-interactive" {
			return true
		}
	}
	return false
}
