package main

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// The first frame is drawn before the preflight has an answer (point D), so
// something has to carry that answer to the screen afterwards. watchLateIdentity
// is that something, and these tests pin that it actually delivers: the screen
// knowing how to accept a welcome.IdentityUpdate is worth nothing if cmd/vc
// never sends one — the same shape of hole as awaitAuth having no caller.
//
// The channel is nil whenever there is nothing to wait for, so the caller can
// tell "no news coming" from "news later" without a goroutine of its own.

// receiveLateIdentity takes the one update the watcher owes us, or says which
// way it failed: nothing sent, or the channel closed empty. The deadline is a
// failure guard — the fixtures answer as soon as they are released.
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

func TestWatchLateIdentityDeliversTheAnswerTheFirstFrameMissed(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	clock := &preflightClock{now: base}
	unblock := make(chan struct{})
	balance := 12.5
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock // still in flight while the first frame is drawn
		return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
	})

	late := watchLateIdentity(loggedInLocalState(), p, "tok", "https://auth.example")
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

	// Point 3: one update, then the channel is done — the goroutine is gone
	// rather than parked on a send nobody reads.
	select {
	case _, ok := <-late:
		if ok {
			t.Fatal("watcher sent a second update")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late identity channel was never closed — the watcher goroutine is still alive")
	}
}

// A failed late answer still has to reach the screen: it is what turns a
// verified line into an unverified one, and the merge rule keeps the name.
func TestWatchLateIdentityDeliversAFailedAnswerToo(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
	clock := &preflightClock{now: base}
	unblock := make(chan struct{})
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock
		return auth.MeResult{}, false, errors.New("session verification unavailable")
	})

	late := watchLateIdentity(loggedInLocalState(), p, "tok", "https://auth.example")
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

func TestWatchLateIdentityReturnsNilWhenThereIsNothingToWaitFor(t *testing.T) {
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
				waitForPreflightAuth(t, p) // already answered, so the first frame has it
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

			if late := watchLateIdentity(tc.local(), p, "tok", "https://auth.example"); late != nil {
				t.Fatalf("got a channel to wait on, want nil — %s", tc.why)
			}
		})
	}
}
