package main

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// The menu loop is where the identity work either reaches the user or does not.
// While it lived inside main(), the two lines that wired it in were untestable,
// and a reviewer could delete both without a single test noticing — the whole
// feature could be removed from production and CI would stay green.
//
// runWelcomeMenu is that loop with the drawing behind a seam, so the wiring is
// observable: these tests read what the screen was actually handed on each
// iteration.

// menuScreenCall records one iteration: what was drawn and what was promised.
type menuScreenCall struct {
	state welcome.AuthState
	late  <-chan welcome.IdentityUpdate
}

// scriptedScreen answers the menu with a fixed sequence of results, recording
// what it was given each time. A step may act before it answers — releasing a
// server answer, say — which is how an iteration boundary is given a meaning.
func scriptedScreen(t *testing.T, calls *[]menuScreenCall, steps ...func() welcome.RunResult) func(welcome.AuthState, <-chan welcome.IdentityUpdate) (welcome.RunResult, error) {
	t.Helper()
	return func(state welcome.AuthState, late <-chan welcome.IdentityUpdate) (welcome.RunResult, error) {
		i := len(*calls)
		*calls = append(*calls, menuScreenCall{state: state, late: late})
		if i >= len(steps) {
			t.Errorf("menu drew %d times, script has %d steps", i+1, len(steps))
			return welcome.Quit, nil
		}
		return steps[i](), nil
	}
}

func failingMenuDeps(t *testing.T, screen func(welcome.AuthState, <-chan welcome.IdentityUpdate) (welcome.RunResult, error), preflight launchPreflightDeps) welcomeMenuDeps {
	t.Helper()
	return welcomeMenuDeps{
		screen:  screen,
		doctor:  func() {},
		profile: func() {},
		login: func() (welcome.AuthState, string, string, *launchPreflight) {
			t.Fatal("menu ran login, which this test never asks for")
			return welcome.AuthState{}, "", "", nil
		},
		preflight: preflight,
	}
}

// Point 1: the identity actually reaches the screen, on the first iteration as
// a promise and on the next as a name. Deleting either half of the wiring —
// the enriched state or the channel — fails this.
func TestRunWelcomeMenuHandsTheScreenAnIdentityAndAChannel(t *testing.T) {
	withTempHome(t)
	clock := &preflightClock{now: time.Now()}
	unblock := make(chan struct{})
	balance := 12.5
	deps := launchPreflightDeps{
		now: clock.Now,
		auth: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			<-unblock
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
		},
		update:      func() string { return "" },
		newClient:   func() *http.Client { return &http.Client{} },
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
	p := startLaunchPreflight("tok", "https://auth.example", false, deps)

	var calls []menuScreenCall
	screen := scriptedScreen(t, &calls,
		func() welcome.RunResult {
			// The user reads the screen; meanwhile the server answers.
			close(unblock)
			waitForPreflightAuth(t, p)
			return welcome.RunDoctor // a menu action that comes back to the menu
		},
		func() welcome.RunResult { return welcome.Quit },
	)

	result, err := runWelcomeMenu(loggedInLocalState(), "tok", "https://auth.example", p, failingMenuDeps(t, screen, deps))
	if err != nil {
		t.Fatalf("menu returned %v", err)
	}
	if result != welcome.Quit {
		t.Fatalf("result = %v, want Quit", result)
	}
	if len(calls) != 2 {
		t.Fatalf("screen drawn %d times, want 2", len(calls))
	}

	if calls[0].late == nil {
		t.Fatal("first frame got no channel — an answer arriving after it would never reach the screen")
	}
	if calls[0].state.UpdateNudge != welcomeIdentityNudge {
		t.Fatalf("first frame UpdateNudge = %q, want %q", calls[0].state.UpdateNudge, welcomeIdentityNudge)
	}
	if calls[1].state.Identity != "fresh@example.com" {
		t.Fatalf("second frame Identity = %q, want %q — the answer never made it into the menu's state", calls[1].state.Identity, "fresh@example.com")
	}
}

// Point 4: the preflight answer is a snapshot of launch. "Top up" and "Open
// profile" are the two places where the money on screen can change while the
// menu is open, so a second frame may not keep claiming a verified balance that
// nobody re-checked. Either it is asked again, or it is shown unverified and
// without a number.
func TestRunWelcomeMenuDoesNotPresentAStaleBalanceAsVerified(t *testing.T) {
	withTempHome(t)
	clock := &preflightClock{now: time.Now()}
	balance := 12.5
	authCalls := 0
	deps := launchPreflightDeps{
		now: clock.Now,
		auth: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			authCalls++
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
		},
		update:      func() string { return "" },
		newClient:   func() *http.Client { return &http.Client{} },
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
	p := startLaunchPreflight("tok", "https://auth.example", false, deps)
	waitForPreflightAuth(t, p)

	callsBeforeSecondFrame := 0
	var calls []menuScreenCall
	screen := scriptedScreen(t, &calls,
		func() welcome.RunResult {
			callsBeforeSecondFrame = authCalls
			return welcome.RunProfile // the user goes off to spend money
		},
		func() welcome.RunResult { return welcome.Quit },
	)

	if _, err := runWelcomeMenu(loggedInLocalState(), "tok", "https://auth.example", p, failingMenuDeps(t, screen, deps)); err != nil {
		t.Fatalf("menu returned %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("screen drawn %d times, want 2", len(calls))
	}

	// Sanity: the launch answer is fresh, so the first frame may show money.
	if calls[0].state.BalanceUsd == nil || calls[0].state.IdentityUnverified {
		t.Fatalf("first frame should carry the just-checked balance, got %+v", calls[0].state)
	}

	// `Open profile` is the one live path where the balance can change while
	// the menu is open (the ShowTopUp branch is unreachable: the model keeps
	// that result to itself and never returns it to the loop). So the probe
	// must actually be re-run, not merely hedged with an "unverified" label.
	if authCalls <= callsBeforeSecondFrame {
		t.Fatalf("the server was not asked again after `Open profile` (auth calls: %d before, %d after) — the balance on the next frame is a snapshot of launch", callsBeforeSecondFrame, authCalls)
	}
	second := calls[1].state
	if second.BalanceUsd != nil && !second.IdentityUnverified && authCalls == callsBeforeSecondFrame {
		t.Fatalf("second frame shows $%.2f as verified after `Open profile`, but the server was never asked again (auth calls: %d) — the number may be stale", *second.BalanceUsd, authCalls)
	}
	// Whatever it chose, it may not forget who is logged in.
	if second.Identity != "fresh@example.com" {
		t.Fatalf("second frame Identity = %q, want %q", second.Identity, "fresh@example.com")
	}
}

// B1. Signing in from the menu has to survive into the next frame. The loop
// rebuilds its state every iteration, so a login result that lands anywhere but
// the variable the next iteration reads is lost the moment the iteration ends —
// and the user who just typed their password is shown a logged-out screen.
func TestRunWelcomeMenuKeepsTheIdentityGainedByLoggingIn(t *testing.T) {
	withTempHome(t) // empty home: a name in the second frame can only come from login
	loggedIn := welcome.AuthState{LoggedIn: true, Identity: "signed-in@example.com", IdentityUnverified: true}

	var calls []menuScreenCall
	screen := scriptedScreen(t, &calls,
		func() welcome.RunResult { return welcome.RunLogin },
		func() welcome.RunResult { return welcome.Quit },
	)
	deps := failingMenuDeps(t, screen, inertPreflightDeps(t))
	logins := 0
	deps.login = func() (welcome.AuthState, string, string, *launchPreflight) {
		logins++
		// A real login returns a fresh state, its token and host, and a probe
		// started for them; nil here keeps the test on the state alone.
		return loggedIn, "new-tok", "https://auth.example", nil
	}

	if _, err := runWelcomeMenu(welcome.AuthState{LoggedIn: false}, "", "https://auth.example", nil, deps); err != nil {
		t.Fatalf("menu returned %v", err)
	}
	if logins != 1 {
		t.Fatalf("login ran %d times, want 1", logins)
	}
	if len(calls) != 2 {
		t.Fatalf("screen drawn %d times, want 2", len(calls))
	}
	if !calls[1].state.LoggedIn {
		t.Fatalf("the frame after a successful login is logged out (%+v) — the login result never reached the next iteration", calls[1].state)
	}
	if calls[1].state.Identity != "signed-in@example.com" {
		t.Fatalf("frame after login Identity = %q, want %q", calls[1].state.Identity, "signed-in@example.com")
	}
}

// inertPreflightDeps is a probe that answers nothing: for menus whose subject is
// not the probe. It never reaches the network, and its goroutine is released
// before the test's temporary home goes away.
func inertPreflightDeps(t *testing.T) launchPreflightDeps {
	t.Helper()
	release := make(chan struct{})
	var wg sync.WaitGroup
	t.Cleanup(func() { close(release); wg.Wait() })
	return launchPreflightDeps{
		now: time.Now,
		auth: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			wg.Add(1)
			defer wg.Done()
			<-release
			return auth.MeResult{}, false, nil
		},
		update:      func() string { return "" },
		newClient:   func() *http.Client { return &http.Client{} },
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
}
