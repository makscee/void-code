package main

import (
	"net/http"
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

	second := calls[1].state
	if second.BalanceUsd != nil && !second.IdentityUnverified && authCalls == callsBeforeSecondFrame {
		t.Fatalf("second frame shows $%.2f as verified after `Open profile`, but the server was never asked again (auth calls: %d) — the number may be stale", *second.BalanceUsd, authCalls)
	}
	// Whatever it chose, it may not forget who is logged in.
	if second.Identity != "fresh@example.com" {
		t.Fatalf("second frame Identity = %q, want %q", second.Identity, "fresh@example.com")
	}
}
