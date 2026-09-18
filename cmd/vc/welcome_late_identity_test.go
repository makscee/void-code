package main

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// The frame is drawn before the preflight has an answer (point D), so the
// answer has to reach the screen afterwards. welcomeScreenState decides both
// halves — what to draw now, and where a later answer will arrive — from a
// single poll, because two polls lose the answer that lands between them:
// the first says "not ready" and the second says "already had it", and nobody
// shows it (v3, point 2).
//
// Three outcomes are possible for a pending answer and only two are allowed:
// in the frame, or on the channel. Neither is the bug.

// receiveLateIdentity takes the one update the channel owes us, or says which
// way it failed: nothing sent, or closed empty. The deadline is a failure
// guard — the fixtures answer as soon as they are released.
func receiveLateIdentity(t *testing.T, late <-chan welcome.IdentityUpdate) welcome.IdentityUpdate {
	t.Helper()
	select {
	case got, ok := <-late:
		if !ok {
			t.Fatal("late identity channel closed without delivering the answer")
		}
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("late identity never arrived")
	}
	return welcome.IdentityUpdate{}
}

func TestWelcomeScreenStateDeliversTheAnswerTheFirstFrameMissed(t *testing.T) {
	withTempHome(t)
	clock := &preflightClock{now: time.Now()}
	unblock := make(chan struct{})
	balance := 12.5
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock // still in flight while the first frame is drawn
		return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
	})

	frame, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")
	if frame.Identity != "" {
		t.Fatalf("frame already names %q, but the server has not answered yet", frame.Identity)
	}
	if late == nil {
		t.Fatal("no channel while the answer is still in flight — the screen would never learn who is logged in")
	}

	close(unblock) // the server answers after the screen is already up
	got := receiveLateIdentity(t, late)

	if got.Identity != "fresh@example.com" {
		t.Fatalf("late Identity = %q, want %q", got.Identity, "fresh@example.com")
	}
	if !got.LoggedIn {
		t.Fatalf("late LoggedIn = false, want true (%+v)", got.AuthState)
	}
	if got.IdentityUnverified {
		t.Fatalf("late IdentityUnverified = true on a fresh server answer (%+v)", got.AuthState)
	}
	if got.BalanceUsd == nil || *got.BalanceUsd != balance {
		t.Fatalf("late BalanceUsd = %v, want %v", got.BalanceUsd, balance)
	}
	if got.UpdateNudge != welcomeIdentityNudge {
		t.Fatalf("late UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
	}

	// One update, then done — the goroutine is gone rather than parked on a
	// send nobody reads.
	select {
	case _, ok := <-late:
		if ok {
			t.Fatal("a second update was sent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late identity channel was never closed — the watcher goroutine is still alive")
	}
}

// A failed late answer still has to reach the screen: it is what turns a
// verified line into an unverified one, and the merge rule keeps the name.
func TestWelcomeScreenStateDeliversAFailedAnswerToo(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
	clock := &preflightClock{now: base}
	unblock := make(chan struct{})
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock
		return auth.MeResult{}, false, errors.New("session verification unavailable")
	})

	_, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")
	if late == nil {
		t.Fatal("no channel while the answer is still in flight")
	}
	close(unblock)
	got := receiveLateIdentity(t, late)

	if got.Identity != "cached@example.com" {
		t.Fatalf("late Identity = %q, want the last known %q", got.Identity, "cached@example.com")
	}
	if !got.IdentityUnverified {
		t.Fatalf("late IdentityUnverified = false after a failed check (%+v)", got.AuthState)
	}
	if got.BalanceUsd != nil {
		t.Fatalf("late BalanceUsd = %v, want nil after a failed check", *got.BalanceUsd)
	}
}

// Point 2, stated as the invariant rather than as a timing: an answer this
// preflight will produce is never lost. The clock is the trap — it fires the
// server's answer in the middle of the call, between a first look at the
// preflight and any second one. A single poll cannot lose it; two polls do.
func TestWelcomeScreenStatePendingAnswerIsNeverLostBetweenPolls(t *testing.T) {
	withTempHome(t) // empty home: an identity in the result can only be the answer
	unblock := make(chan struct{})
	now := time.Now()
	var p *launchPreflight
	var (
		mu       sync.Mutex
		looks    int
		release  sync.Once
		released bool
	)
	fire := func() {
		release.Do(func() { close(unblock) })
	}

	// Every look at the preflight goes through reusable(), which asks the clock.
	// Look 1 is the preflight's own start; a second look decides the frame. The
	// answer is fired on the third — the look a two-poll implementation takes
	// after the frame is already decided, measured on the two-call version this
	// replaces: releasing there left the answer in neither half.
	releaseOnThirdLook := func() time.Time {
		mu.Lock()
		looks++
		fireNow := looks == 3 && !released
		if fireNow {
			released = true
		}
		mu.Unlock()
		if fireNow {
			fire()
			<-p.authDone // the answer is stored before this look returns
		}
		return now
	}

	deps := launchPreflightDeps{
		now: releaseOnThirdLook,
		auth: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			<-unblock
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com"}, true, nil
		},
		update:      func() string { return "" },
		newClient:   func() *http.Client { return &http.Client{} },
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
	p = startLaunchPreflight("tok", "https://auth.example", false, deps)

	frame, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")

	if frame.Identity == "fresh@example.com" {
		return // the answer made it into the frame: nothing was lost
	}
	if late == nil {
		t.Fatalf("the answer is neither in the frame (Identity=%q) nor on a channel — it was dropped between two polls of the same preflight", frame.Identity)
	}
	fire() // a single-poll implementation may never reach the third look
	if got := receiveLateIdentity(t, late); got.Identity != "fresh@example.com" {
		t.Fatalf("late Identity = %q, want %q", got.Identity, "fresh@example.com")
	}
}

func TestWelcomeScreenStateReturnsNoChannelWhenThereIsNothingToWaitFor(t *testing.T) {
	cases := map[string]struct {
		local func() welcome.AuthState
		build func(t *testing.T, clock *preflightClock) *launchPreflight
		why   string
	}{
		"noPreflight": {
			local: loggedInLocalState,
			build: func(*testing.T, *preflightClock) *launchPreflight { return nil },
			why:   "there is no preflight at all",
		},
		"loggedOut": {
			local: func() welcome.AuthState {
				return welcome.AuthState{LoggedIn: false, UpdateNudge: welcomeIdentityNudge}
			},
			build: func(t *testing.T, clock *preflightClock) *launchPreflight {
				unblock := make(chan struct{})
				t.Cleanup(func() { close(unblock) })
				return newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
					<-unblock
					return auth.MeResult{Email: "fresh@example.com"}, true, nil
				})
			},
			why: "a logged-out screen takes no identity, late or not",
		},
		"foreignPreflight": {
			local: loggedInLocalState,
			build: func(t *testing.T, clock *preflightClock) *launchPreflight {
				unblock := make(chan struct{})
				t.Cleanup(func() { close(unblock) })
				return newIdentityPreflight(t, clock, "a-different-token", func(string, string, *http.Client) (auth.MeResult, bool, error) {
					<-unblock
					return auth.MeResult{Email: "other@example.com"}, true, nil
				})
			},
			why: "the pending answer belongs to another token",
		},
		"stalePreflight": {
			local: loggedInLocalState,
			build: func(t *testing.T, clock *preflightClock) *launchPreflight {
				unblock := make(chan struct{})
				t.Cleanup(func() { close(unblock) })
				p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
					<-unblock
					return auth.MeResult{Email: "fresh@example.com"}, true, nil
				})
				// Long enough in the menu that the preflight is no longer ours
				// to reuse (launchPreflightFreshness).
				clock.now = clock.now.Add(launchPreflightFreshness + time.Minute)
				return p
			},
			why: "the preflight went stale while the menu was open",
		},
		"answerAlreadyInTheFrame": {
			local: loggedInLocalState,
			build: func(t *testing.T, clock *preflightClock) *launchPreflight {
				p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
					return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com"}, true, nil
				})
				waitForPreflightAuth(t, p) // answered already, so the frame carries it
				return p
			},
			why: "the answer was ready before the frame was built",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withTempHome(t)
			clock := &preflightClock{now: time.Now()}
			p := tc.build(t, clock)

			if _, late := welcomeScreenState(tc.local(), p, "tok", "https://auth.example"); late != nil {
				t.Fatalf("got a channel to wait on, want nil — %s", tc.why)
			}
		})
	}
}

// Point 5. A revoked or expired token makes the screen's name a lie, and the
// cached copy of it outlives the session that earned it: readMeCache's
// LastKnown branch has no expiry at all. The probe answering ErrNotLoggedIn is
// the moment that has to end both.
func TestWelcomeScreenStateForgetsTheUserWhenTheTokenIsRejected(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base)
	clock := &preflightClock{now: base}
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		return auth.MeResult{}, false, auth.ErrNotLoggedIn
	})
	waitForPreflightAuth(t, p)

	// The caller still believes in the old name; the answer must overrule it.
	local := welcome.AuthState{LoggedIn: true, Identity: "cached@example.com", IdentityUnverified: true, UpdateNudge: welcomeIdentityNudge}
	got, _ := welcomeScreenState(local, p, "tok", "https://auth.example")

	if got.Identity != "" {
		t.Fatalf("Identity = %q after the token was rejected, want the screen to stop naming anyone", got.Identity)
	}
	if got.LoggedIn {
		t.Fatalf("LoggedIn = true after the token was rejected (%+v)", got)
	}
	if got.BalanceUsd != nil {
		t.Fatalf("BalanceUsd = %v after the token was rejected, want nil", *got.BalanceUsd)
	}
	if got.UpdateNudge != welcomeIdentityNudge {
		t.Fatalf("UpdateNudge = %q, want %q — the update check is unrelated to the token", got.UpdateNudge, welcomeIdentityNudge)
	}
	if cached, ok := readMeCache("https://auth.example", "tok", time.Now()); ok {
		t.Fatalf("me cache still holds %+v after the token was rejected — the next launch would name a user who no longer has a session", cached.Me)
	}
}
