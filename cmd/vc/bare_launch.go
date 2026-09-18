package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/makscee/void-code/internal/welcome"
)

// bareLaunchOutcome is how the bare-launch path ends. It decides; main turns the
// decision into an exit code or a fall through to Cobra. Keeping the exit in
// main is what lets a test run the whole path — including the one ending that
// refuses admission — without killing the test process.
type bareLaunchOutcome int

const (
	// bareLaunchFallThrough: not a bare launch, or one that Cobra finishes —
	// a sub-command, --raw, or a non-TTY caller who is already signed in.
	bareLaunchFallThrough bareLaunchOutcome = iota
	// bareLaunchSpawned: the menu already ran Pi through Cobra; running Cobra
	// again would run it twice.
	bareLaunchSpawned
	// bareLaunchQuit: the user left the menu.
	bareLaunchQuit
	// bareLaunchAuthFailed: no terminal to sign in on and no usable token.
	bareLaunchAuthFailed
)

// bareLaunchDeps are the edges of the launch path: the argv it reads, the
// terminal it asks about, where its refusal is printed, and the three things it
// starts — local state, the probe, and the menu. They are parameters rather than
// globals because the feature reaching the user is a property of this wiring:
// a path handed a probe that asks nobody, or a menu that draws nothing, leaves
// every other test green while production stops naming anyone.
type bareLaunchDeps struct {
	args        []string
	stderr      io.Writer
	stdinTTY    func() bool
	diagnostics *launchDiagnostics
	localState  func() (welcome.AuthState, string, string, launchSource)
	startProbe  func(token, authHost string) *launchPreflight
	menu        func(state welcome.AuthState, token, authHost string, p *launchPreflight) (welcome.RunResult, error)
	handleError func(error)
}

// runBareLaunch is the persistent landing screen on a bare `vc` invocation:
// read local state, start the probe, and decide between the menu, a straight
// hand-off to Cobra, and a refusal.
func runBareLaunch(deps bareLaunchDeps) bareLaunchOutcome {
	if !isBareLaunch(deps.args) {
		return bareLaunchFallThrough
	}

	// hasNonInteractiveArg reads the same argv indirection every early scan
	// does, so the path's own args are what it sees.
	previousArgs := osArgs
	osArgs = deps.args
	defer func() { osArgs = previousArgs }()

	// Published before the probe starts: everything downstream — the probe's own
	// phases and the screen's first render — records into this trace.
	currentLaunchDiagnostics = deps.diagnostics
	state, token, authHost, localSource := deps.localState()
	deps.diagnostics.record(phaseLocalStateLoad, outcomeComplete, localSource)
	preflight := deps.startProbe(token, authHost)

	// Interactive only when stdin is a TTY AND --non-interactive was not passed.
	// cobra has not parsed flags yet at this point, so the flag is scanned from
	// argv directly (mirrors the early --raw scan). When not interactive, the
	// title screen is skipped — same effect as --raw, but the gate still
	// distinguishes logged-in (spawn) from logged-out (fail).
	interactive := deps.stdinTTY() && !hasNonInteractiveArg()
	switch decideGate(interactive, state.LoggedIn) {
	case gateFailAuth:
		// Non-interactive (non-TTY) context with no usable token: fail fast
		// instead of hanging in the login picker or device-flow poll loop.
		// Automation callers (void-os, subagents, scripts) re-auth manually.
		fmt.Fprintln(deps.stderr, "vc: auth failed: session token missing or expired — re-authenticate with `vc login`")
		return bareLaunchAuthFailed
	case gateSpawn:
		// Non-interactive + logged in: skip the welcome TUI entirely. It blocks
		// on a keypress that a non-TTY stdin can never deliver, which hangs
		// automation callers forever. Fall straight through to spawn.
		return bareLaunchFallThrough
	case gateShowWelcome:
		switch result, err := deps.menu(state, token, authHost, preflight); result {
		case welcome.SpawnPi:
			if err != nil {
				deps.handleError(err)
			}
			return bareLaunchSpawned
		case welcome.Quit:
			return bareLaunchQuit
		}
	}
	return bareLaunchFallThrough
}

// isBareLaunch reports whether argv asks for the landing screen at all: no
// sub-command of its own, and no --raw before the `--` that hands the rest to
// Pi. Past that `--` the flags belong to Pi, so `vc -- --raw` is a bare launch.
func isBareLaunch(args []string) bool {
	if len(args) > 1 && welcomeGateSkippingSubCommands[args[1]] {
		return false
	}
	for _, a := range args[1:] {
		if a == "--raw" {
			return false
		}
		if a == "--" {
			break
		}
	}
	return true
}

func defaultBareLaunchDeps() bareLaunchDeps {
	return bareLaunchDeps{
		args:        os.Args,
		stderr:      os.Stderr,
		stdinTTY:    isStdinTTY,
		diagnostics: newLaunchDiagnosticsFromEnv(time.Now, os.Stderr),
		localState:  resolveLocalAuthStateWithSource,
		startProbe: func(token, authHost string) *launchPreflight {
			return startLaunchPreflight(token, authHost, true, defaultLaunchPreflightDeps())
		},
		menu: func(state welcome.AuthState, token, authHost string, p *launchPreflight) (welcome.RunResult, error) {
			return runWelcomeMenu(state, token, authHost, p, defaultWelcomeMenuDeps())
		},
		handleError: handleExecuteError,
	}
}
