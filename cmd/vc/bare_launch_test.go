package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// The bare-launch path is the last thing standing between the identity work and
// the user, and while it lived in main() it was unreachable from a test: delete
// the call to runWelcomeMenu and the whole feature leaves production with every
// test still green. runBareLaunch is that block with its globals — the argv it
// reads, the terminal it asks about, the process it ends — turned into
// parameters, so the decisions it makes can be read instead of assumed.
//
// It decides rather than exits: main turns the outcome into os.Exit or a fall
// through to Cobra. That is the only way the three endings can be told apart
// from a test, and it keeps each ending exactly what it was.

// bareLaunchProbe records the menu call the path made, so a test can see what
// the menu was actually handed.
type bareLaunchProbe struct {
	calls  int
	state  welcome.AuthState
	token  string
	host   string
	p      *launchPreflight
	result welcome.RunResult
	err    error
}

func (b *bareLaunchProbe) menu(state welcome.AuthState, token, authHost string, p *launchPreflight) (welcome.RunResult, error) {
	b.calls++
	b.state, b.token, b.host, b.p = state, token, authHost, p
	return b.result, b.err
}

// testBareLaunchDeps is a path that reaches nothing real: no terminal, no
// network, no process exit. Each test overrides the one edge it is about.
func testBareLaunchDeps(t *testing.T, menu *bareLaunchProbe) (bareLaunchDeps, *bytes.Buffer) {
	t.Helper()
	stderr := &bytes.Buffer{}
	return bareLaunchDeps{
		args:        []string{"vc"},
		stderr:      stderr,
		stdinTTY:    func() bool { return true },
		diagnostics: newLaunchDiagnostics(true, time.Now, io.Discard),
		localState: func() (welcome.AuthState, string, string, launchSource) {
			return welcome.AuthState{LoggedIn: true, IdentityUnverified: true}, "tok", "https://auth.example", sourceLocal
		},
		startProbe: func(token, authHost string) *launchPreflight {
			return newIdentityPreflight(t, &preflightClock{now: time.Now()}, token, func(string, string, *http.Client) (auth.MeResult, bool, error) {
				return auth.MeResult{UserID: "u-probe", Email: "probe@example.com"}, true, nil
			})
		},
		menu:        menu.menu,
		handleError: func(error) { t.Fatal("the path handled an error none of its parts produced") },
	}, stderr
}

func TestRunBareLaunchOnATTYBuildsTheMenuFromTheProbeAndLocalState(t *testing.T) {
	withTempHome(t)
	menu := &bareLaunchProbe{result: welcome.Quit}
	deps, stderr := testBareLaunchDeps(t, menu)

	previous := currentLaunchDiagnostics
	t.Cleanup(func() { currentLaunchDiagnostics = previous })

	if outcome := runBareLaunch(deps); outcome != bareLaunchQuit {
		t.Fatalf("outcome = %v, want bareLaunchQuit — Quit ends the process, it does not fall through to Cobra", outcome)
	}
	if menu.calls != 1 {
		t.Fatalf("menu ran %d times, want 1", menu.calls)
	}
	if !menu.state.LoggedIn || menu.token != "tok" || menu.host != "https://auth.example" {
		t.Fatalf("menu got state=%+v token=%q host=%q, want the ones local state resolved", menu.state, menu.token, menu.host)
	}
	// The probe is the feature: handing the menu a nil one is how the identity
	// work leaves production while every menu test stays green.
	if menu.p == nil {
		t.Fatal("menu got no preflight — nothing would ever ask the server who this is")
	}
	waitForPreflightAuth(t, menu.p)
	if answer, done := menu.p.answerIfDone(); !done || answer.me.Email != "probe@example.com" {
		t.Fatalf("the preflight handed to the menu is not the one this path started (done=%v answer=%+v)", done, answer.me)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on the interactive path", stderr.String())
	}

	// The screen's own diagnostics hang off this global, so the path still has
	// to publish the one it was given, and the local-state record still comes
	// first — a later reader of the trace reads the same order as before.
	if currentLaunchDiagnostics != deps.diagnostics {
		t.Fatal("the diagnostics this path was given never became the current ones — the screen would record into a different trace")
	}
	if len(deps.diagnostics.pending) == 0 || deps.diagnostics.pending[0].phase != phaseLocalStateLoad {
		t.Fatalf("first diagnostic record = %+v, want %s first", deps.diagnostics.pending, phaseLocalStateLoad)
	}
	if got := deps.diagnostics.pending[0].source; got != sourceLocal {
		t.Fatalf("local state recorded with source %q, want the one local state reported (%q)", got, sourceLocal)
	}
}

func TestRunBareLaunchTellsTheThreeGateOutcomesApart(t *testing.T) {
	cases := map[string]struct {
		tty      bool
		args     []string
		loggedIn bool
		want     bareLaunchOutcome
		menuRuns int
		stderr   string
	}{
		"ttyAndLoggedIn": {tty: true, args: []string{"vc"}, loggedIn: true, want: bareLaunchQuit, menuRuns: 1},
		"ttyAndLoggedOut": {
			// No token, but a terminal to sign in on: the menu is the login path.
			tty: true, args: []string{"vc"}, loggedIn: false, want: bareLaunchQuit, menuRuns: 1,
		},
		"noTTYButLoggedIn": {
			// Automation with a usable token: skip the TUI, let Cobra spawn Pi.
			tty: false, args: []string{"vc"}, loggedIn: true, want: bareLaunchFallThrough, menuRuns: 0,
		},
		"noTTYAndLoggedOut": {
			tty: false, args: []string{"vc"}, loggedIn: false, want: bareLaunchAuthFailed, menuRuns: 0,
			stderr: "vc: auth failed: session token missing or expired — re-authenticate with `vc login`\n",
		},
		"nonInteractiveFlagOnATTY": {
			// --non-interactive means the same as no terminal, even on one.
			tty: true, args: []string{"vc", "--non-interactive"}, loggedIn: true, want: bareLaunchFallThrough, menuRuns: 0,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)
			menu := &bareLaunchProbe{result: welcome.Quit}
			deps, stderr := testBareLaunchDeps(t, menu)
			deps.args = tc.args
			deps.stdinTTY = func() bool { return tc.tty }
			loggedIn := tc.loggedIn
			deps.localState = func() (welcome.AuthState, string, string, launchSource) {
				return welcome.AuthState{LoggedIn: loggedIn, IdentityUnverified: loggedIn}, "tok", "https://auth.example", sourceLocal
			}
			previous := currentLaunchDiagnostics
			t.Cleanup(func() { currentLaunchDiagnostics = previous })

			if outcome := runBareLaunch(deps); outcome != tc.want {
				t.Fatalf("outcome = %v, want %v", outcome, tc.want)
			}
			if menu.calls != tc.menuRuns {
				t.Fatalf("menu ran %d times, want %d", menu.calls, tc.menuRuns)
			}
			if got := stderr.String(); got != tc.stderr {
				t.Fatalf("stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}

func TestRunBareLaunchPassesOnSubCommandsAndRaw(t *testing.T) {
	for name, args := range map[string][]string{
		"subCommand":      {"vc", "status"},
		"rawFlag":         {"vc", "--raw"},
		"argsAfterDouble": {"vc", "--", "--raw"}, // past `--` the flags are Pi's
	} {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)
			menu := &bareLaunchProbe{result: welcome.Quit}
			deps, _ := testBareLaunchDeps(t, menu)
			deps.args = args
			probes := 0
			deps.startProbe = func(string, string) *launchPreflight { probes++; return nil }
			previous := currentLaunchDiagnostics
			t.Cleanup(func() { currentLaunchDiagnostics = previous })

			want := bareLaunchFallThrough
			if name == "argsAfterDouble" {
				// `--` hides --raw from the early scan, so this is a bare launch
				// after all: the menu runs and its Quit ends the process.
				want = bareLaunchQuit
			}
			if outcome := runBareLaunch(deps); outcome != want {
				t.Fatalf("outcome = %v, want %v", outcome, want)
			}
			if want == bareLaunchFallThrough && (menu.calls != 0 || probes != 0) {
				t.Fatalf("a launch that is not bare started %d probe(s) and %d menu(s), want none", probes, menu.calls)
			}
		})
	}
}

func TestRunBareLaunchEndsTheProcessOnSpawnAndReportsItsError(t *testing.T) {
	t.Run("spawnWithoutError", func(t *testing.T) {
		withTempHome(t)
		menu := &bareLaunchProbe{result: welcome.SpawnPi}
		deps, _ := testBareLaunchDeps(t, menu)
		previous := currentLaunchDiagnostics
		t.Cleanup(func() { currentLaunchDiagnostics = previous })

		if outcome := runBareLaunch(deps); outcome != bareLaunchSpawned {
			t.Fatalf("outcome = %v, want bareLaunchSpawned — the spawn already happened, Cobra must not run again", outcome)
		}
	})

	t.Run("spawnWithError", func(t *testing.T) {
		withTempHome(t)
		failure := io.ErrUnexpectedEOF
		menu := &bareLaunchProbe{result: welcome.SpawnPi, err: failure}
		deps, _ := testBareLaunchDeps(t, menu)
		handled := []error{}
		deps.handleError = func(err error) { handled = append(handled, err) }
		previous := currentLaunchDiagnostics
		t.Cleanup(func() { currentLaunchDiagnostics = previous })

		if outcome := runBareLaunch(deps); outcome != bareLaunchSpawned {
			t.Fatalf("outcome = %v, want bareLaunchSpawned", outcome)
		}
		if len(handled) != 1 || handled[0] != failure {
			t.Fatalf("handled errors = %v, want exactly the spawn's own %v", handled, failure)
		}
	})
}

// The feature being on is a property of the default wiring, not of the loop:
// every test above would stay green if defaultBareLaunchDeps handed the menu a
// probe that asks nobody, or a menu that draws nothing. So this one takes the
// production deps and makes both halves prove themselves — the probe by
// reaching a server, the menu by putting that answer on a screen.
func TestDefaultBareLaunchDepsCarryTheRealProbeAndTheRealMenu(t *testing.T) {
	withTempHome(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/vc/me" {
			t.Errorf("probe asked for %s, want /v1/vc/me", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"userId":"u-real","email":"real@example.com","balanceUsd":4.5}`))
	}))
	defer srv.Close()

	deps := defaultBareLaunchDeps()

	// Half one: the probe is a real one — it goes to the server it is given.
	p := deps.startProbe("tok", srv.URL)
	if p == nil {
		t.Fatal("the default path starts no probe")
	}
	waitForPreflightAuth(t, p)
	if requests == 0 {
		t.Fatal("the default probe never asked the server anything")
	}
	answer, done := p.answerIfDone()
	if !done || answer.me.Email != "real@example.com" {
		t.Fatalf("probe answer done=%v me=%+v, want the server's", done, answer.me)
	}

	// Half two: the menu is the real one, drawing on the real screen. Only the
	// terminal is replaced, so an answer that reaches the frame proves the whole
	// chain — state building, the menu loop, and the screen behind it.
	in, keys := io.Pipe()
	out := &screenOutput{}
	previousOpts := welcomeProgramOptions
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignals()}
	t.Cleanup(func() { welcomeProgramOptions = previousOpts })

	done2 := make(chan welcome.RunResult, 1)
	go func() {
		result, _ := deps.menu(welcome.AuthState{LoggedIn: true, IdentityUnverified: true}, "tok", srv.URL, p)
		done2 <- result
	}()

	waitForScreenText(t, out, "real@example.com")
	waitForScreenText(t, out, welcome.FormatBalance(answer.me.BalanceUsd))

	_, _ = keys.Write([]byte("q"))
	select {
	case result := <-done2:
		if result != welcome.Quit {
			t.Fatalf("menu returned %v, want Quit", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the default menu never returned")
	}
	_ = keys.Close()
}
