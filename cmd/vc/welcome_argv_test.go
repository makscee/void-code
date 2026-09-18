package main

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/welcome"
	"github.com/spf13/cobra"
)

// Argv is read in more than one place on the launch path, and the copies are
// easy to mistake for each other: in production they hold the same slice, so a
// path that reaches for the process's argv instead of the one it was built for
// behaves identically — until something is built for a different argv, which is
// exactly what a test does. These pin the wiring by giving the path an argv the
// process does not have, and watching which one comes out the far end at Cobra.

// recordCobraArgs replaces the root command with one that records what it is
// asked to run, so a spawn can be observed without spawning anything.
func recordCobraArgs(t *testing.T) *[]string {
	t.Helper()
	recorded := &[]string{}
	previous := rootCmd
	stub := &cobra.Command{
		Use:           "vc",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			*recorded = append([]string{}, args...)
			return nil
		},
	}
	rootCmd = stub
	t.Cleanup(func() { rootCmd = previous })
	return recorded
}

// chooseStartOnTheScreen drives the real welcome program to its first menu item
// — Start — which is the choice that ends in a spawn, and returns once the run
// under test has finished.
func chooseStartOnTheScreen(t *testing.T, run func() (welcome.RunResult, error)) welcome.RunResult {
	t.Helper()
	in, keys := io.Pipe()
	out := &screenOutput{}
	previous := welcomeProgramOptions
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignals()}
	t.Cleanup(func() { welcomeProgramOptions = previous })

	done := make(chan welcome.RunResult, 1)
	go func() {
		result, err := run()
		if err != nil {
			t.Errorf("run returned %v", err)
		}
		done <- result
	}()

	waitForScreenText(t, out, "Start")
	_, _ = keys.Write([]byte("\r"))
	defer func() { _ = keys.Close() }()

	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatalf("the screen never finished; output was:\n%s", out.String())
	}
	return welcome.Quit
}

// The menu's screen is the thing that hands argv to Cobra when the user picks
// Start. Building it for one argv and reading another is invisible in
// production, where the two are the same slice, and wrong everywhere else — a
// desktop session or a wrapper that launches vc with its own arguments would
// have them silently replaced by the process's.
func TestWelcomeMenuDepsHandCobraTheArgvTheyWereBuiltFor(t *testing.T) {
	withTempHome(t)
	recorded := recordCobraArgs(t)

	deps := welcomeMenuDepsFor([]string{"vc", "sentinel-arg"})
	result := chooseStartOnTheScreen(t, func() (welcome.RunResult, error) {
		return deps.screen(welcome.AuthState{LoggedIn: true, Identity: "known@example.com"}, nil)
	})

	if result != welcome.SpawnPi {
		t.Fatalf("screen returned %v, want SpawnPi", result)
	}
	if len(*recorded) != 1 || (*recorded)[0] != "sentinel-arg" {
		t.Fatalf("Cobra was run with %q, want the argv these deps were built for ([sentinel-arg]) — the screen reached for the process's own arguments instead", *recorded)
	}
}

// And the same one level up: the bare-launch path builds the menu for the argv
// the path itself was built for. The process moves on — os.Args is restored
// before the menu ever runs — so a path that reads it again gets the test
// binary's flags rather than what it was launched with.
func TestDefaultBareLaunchDepsBuildTheMenuForTheirOwnArgv(t *testing.T) {
	withTempHome(t)
	freshUpdateCheckCache(t)
	recorded := recordCobraArgs(t)

	previousArgs := os.Args
	os.Args = []string{"vc", "sentinel-arg"}
	deps := defaultBareLaunchDeps()
	os.Args = previousArgs // whatever the process says now, the path was built earlier

	previousDiagnostics := currentLaunchDiagnostics
	currentLaunchDiagnostics = newLaunchDiagnostics(false, time.Now, io.Discard)
	t.Cleanup(func() { currentLaunchDiagnostics = previousDiagnostics })

	result := chooseStartOnTheScreen(t, func() (welcome.RunResult, error) {
		return deps.menu(welcome.AuthState{LoggedIn: true, Identity: "known@example.com"}, "tok", "https://auth.example", nil)
	})

	if result != welcome.SpawnPi {
		t.Fatalf("menu returned %v, want SpawnPi", result)
	}
	if len(*recorded) != 1 || (*recorded)[0] != "sentinel-arg" {
		t.Fatalf("Cobra was run with %q, want [sentinel-arg] — the menu was wired for the process's argv rather than this path's", *recorded)
	}
}

// A login that fails ends the process. Nothing used to run this branch, and
// through the exit seam it now returns in a test instead of ending anything —
// so the branch has to be honest about what it hands back as well: a state that
// claims a session would send the next frame off to name a user who never
// signed in.
func TestFailedLoginEndsTheProcessInsteadOfReturningToTheMenu(t *testing.T) {
	withTempHome(t)

	previousRunner := deviceFlowRunner
	deviceFlowRunner = func(config.Config) error { return errors.New("no pairing code was ever entered") }
	t.Cleanup(func() { deviceFlowRunner = previousRunner })

	exits := []int{}
	previousExit := exitProcess
	exitProcess = func(code int) { exits = append(exits, code) }
	t.Cleanup(func() { exitProcess = previousExit })

	state, token, authHost, p := welcomeMenuDepsFor([]string{"vc"}).login()

	if len(exits) != 1 || exits[0] != 1 {
		t.Fatalf("process exits = %v, want exactly one with code 1 — a failed login has to end the run, not fall back into the menu", exits)
	}
	if state.LoggedIn {
		t.Fatalf("a failed login handed back a logged-in state (%+v)", state)
	}
	if token != "" || authHost != "" || p != nil {
		t.Fatalf("a failed login handed back credentials: token=%q host=%q probe=%v", token, authHost, p != nil)
	}
}
