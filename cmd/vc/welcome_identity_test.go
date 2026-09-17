package main

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// The welcome screen must show who the logged-in user is. It is drawn from
// local state only (resolveLocalAuthStateWithSource fills LoggedIn and
// IdentityUnverified and nothing else), while the launch preflight that already
// asked the server is never consulted — awaitAuth has no production caller.
// welcomeStateFromPreflight is the seam that joins the two, so these tests pin
// its contract: fresh answer wins, last-known identity is the fallback, and the
// update nudge the preflight put into local state survives every branch.

const welcomeIdentityNudge = "update available: v9.9.9"

// preflightClock drives startLaunchPreflight/awaitAuth without sleeping: the
// test moves the clock instead of waiting for the 2s auth probe budget.
type preflightClock struct{ now time.Time }

func (c *preflightClock) Now() time.Time { return c.now }

func newIdentityPreflight(t *testing.T, clock *preflightClock, token string, authFn func(string, string, *http.Client) (auth.MeResult, bool, error)) *launchPreflight {
	t.Helper()
	deps := launchPreflightDeps{
		now:         clock.Now,
		auth:        authFn,
		update:      func() string { return "" },
		newClient:   func() *http.Client { return &http.Client{} },
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
	// withUpdate=false: the nudge reaches the screen through local state, which
	// is exactly what point 5 of the contract says must not be lost.
	return startLaunchPreflight(token, "https://auth.example", false, deps)
}

func loggedInLocalState() welcome.AuthState {
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true, UpdateNudge: welcomeIdentityNudge}
}

func TestWelcomeStateFromPreflight(t *testing.T) {
	t.Run("LoggedOutLocalStateIsReturnedUntouched", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		// A cached identity exists and the preflight has a good answer; neither
		// may leak into a logged-out screen.
		writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com"}, true, nil
		})

		local := welcome.AuthState{LoggedIn: false, UpdateNudge: welcomeIdentityNudge}
		got := welcomeStateFromPreflight(local, p, "tok", "https://auth.example")

		if got != local {
			t.Fatalf("logged-out state changed: got %+v, want %+v", got, local)
		}
	})

	t.Run("FreshPreflightAnswerShowsEmailAndBalance", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		clock := &preflightClock{now: base}
		balance := 12.5
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
		})

		got := welcomeStateFromPreflight(loggedInLocalState(), p, "tok", "https://auth.example")

		if !got.LoggedIn {
			t.Fatalf("LoggedIn = false, want true (%+v)", got)
		}
		if got.Identity != "fresh@example.com" {
			t.Fatalf("Identity = %q, want %q", got.Identity, "fresh@example.com")
		}
		if got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = true on a fresh server answer (%+v)", got)
		}
		if got.BalanceUsd == nil || *got.BalanceUsd != balance {
			t.Fatalf("BalanceUsd = %v, want %v", got.BalanceUsd, balance)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})

	t.Run("FreshPreflightAnswerWithoutEmailFallsBackToUserID", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-fresh"}, true, nil
		})

		got := welcomeStateFromPreflight(loggedInLocalState(), p, "tok", "https://auth.example")

		if got.Identity != "u-fresh" {
			t.Fatalf("Identity = %q, want %q", got.Identity, "u-fresh")
		}
		if got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = true on a fresh server answer (%+v)", got)
		}
		if got.BalanceUsd != nil {
			t.Fatalf("BalanceUsd = %v, want nil (server sent none)", *got.BalanceUsd)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})

	// No fresh answer, but the last known identity is on disk: show it, mark it
	// unverified, and say nothing about money — the balance would be a guess.
	noFreshAnswer := map[string]func(t *testing.T, clock *preflightClock) *launchPreflight{
		"preflightNotReusable": func(t *testing.T, clock *preflightClock) *launchPreflight {
			// Started for another token: awaitAuth reports reused=false.
			return newIdentityPreflight(t, clock, "a-different-token", func(string, string, *http.Client) (auth.MeResult, bool, error) {
				return auth.MeResult{UserID: "u-other", Email: "other@example.com"}, true, nil
			})
		},
		"authErrored": func(t *testing.T, clock *preflightClock) *launchPreflight {
			return newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
				return auth.MeResult{}, false, errors.New("session verification unavailable")
			})
		},
		// An error outranks reached: the server was spoken to and still failed
		// the check, so whatever body came back is not a fresh identity. Without
		// this fixture the err test can be dropped from the freshness condition
		// and every other case stays green — reached=false already hid it.
		"errorOutranksReachedAndItsPayload": func(t *testing.T, clock *preflightClock) *launchPreflight {
			return newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
				balance := 999.99
				return auth.MeResult{UserID: "u-broken", Email: "broken@example.com", BalanceUsd: &balance}, true, errors.New("session verification unavailable")
			})
		},
		"serverNotReached": func(t *testing.T, clock *preflightClock) *launchPreflight {
			return newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
				return auth.MeResult{}, false, nil
			})
		},
		"missedTheTimeout": func(t *testing.T, clock *preflightClock) *launchPreflight {
			blocked := make(chan struct{})
			t.Cleanup(func() { close(blocked) })
			p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
				<-blocked
				return auth.MeResult{UserID: "u-late", Email: "late@example.com"}, true, nil
			})
			// Budget spent: awaitAuth stops waiting and answers with nothing.
			clock.now = clock.now.Add(authProbeTimeout)
			return p
		},
	}

	for name, build := range noFreshAnswer {
		t.Run("CachedIdentityWhenNoFreshAnswer/"+name, func(t *testing.T) {
			withTempHome(t)
			base := time.Now()
			// Written in the past, so its fresh window has already closed and
			// readMeCache answers with the last known identity only.
			writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
			clock := &preflightClock{now: base}
			p := build(t, clock)

			got := welcomeStateFromPreflight(loggedInLocalState(), p, "tok", "https://auth.example")

			if !got.LoggedIn {
				t.Fatalf("LoggedIn = false, want true (%+v)", got)
			}
			if got.Identity != "cached@example.com" {
				t.Fatalf("Identity = %q, want %q", got.Identity, "cached@example.com")
			}
			if !got.IdentityUnverified {
				t.Fatalf("IdentityUnverified = false on a cached identity (%+v)", got)
			}
			if got.BalanceUsd != nil {
				t.Fatalf("BalanceUsd = %v, want nil (nothing fresh to show)", *got.BalanceUsd)
			}
			if got.UpdateNudge != welcomeIdentityNudge {
				t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
			}
		})
	}

	t.Run("CachedIdentityWithoutEmailFallsBackToUserID", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached"}, base.Add(-10*time.Minute))
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{}, false, errors.New("unavailable")
		})

		got := welcomeStateFromPreflight(loggedInLocalState(), p, "tok", "https://auth.example")

		if got.Identity != "u-cached" {
			t.Fatalf("Identity = %q, want %q", got.Identity, "u-cached")
		}
		if !got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = false on a cached identity (%+v)", got)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})

	t.Run("NeitherFreshNorCachedLeavesIdentityUnknown", func(t *testing.T) {
		withTempHome(t) // empty home: no me cache at all
		base := time.Now()
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{}, false, errors.New("unavailable")
		})

		got := welcomeStateFromPreflight(loggedInLocalState(), p, "tok", "https://auth.example")

		if !got.LoggedIn {
			t.Fatalf("LoggedIn = false, want true (%+v)", got)
		}
		if got.Identity != "" {
			t.Fatalf("Identity = %q, want empty (screen prints `identity temporarily unavailable`)", got.Identity)
		}
		if !got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = false with no identity at all (%+v)", got)
		}
		if got.BalanceUsd != nil {
			t.Fatalf("BalanceUsd = %v, want nil", *got.BalanceUsd)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})
}
