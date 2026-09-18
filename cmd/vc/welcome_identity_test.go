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
// asked the server is never consulted. welcomeScreenState joins the two, and it
// answers with a pair: the frame to draw now, and the channel a later answer
// arrives on — both decided by one poll of the preflight (v3, point 2).
//
// The contract these tests pin (v2, after the review panel):
//
//  1. a logged-out screen is returned untouched;
//  2. a fresh preflight answer names the identity, marks it verified and
//     carries the balance the server sent;
//  A. that fresh answer is also written to the me cache — nothing else writes
//     it any more (cachedFetchMeState lost its production callers in 8ebfcef),
//     so without this write a "last known identity" never comes to exist;
//  B. anything read back from the cache is shown unverified and without money,
//     whether or not its fresh window is still open;
//  C. an identity already known to the caller is never downgraded to an empty
//     one — the screen is rebuilt on every menu iteration, and once the
//     preflight goes stale there is nothing fresh left to rebuild it from;
//  D. building the state never waits for the network: the first render happens
//     before any optional request completes;
//  5. the update nudge already in local state survives every branch.
//
// Point E of the spec — a late answer catching up with an already-drawn screen —
// lives in internal/welcome, where the program and its model are.

const welcomeIdentityNudge = "update available: v9.9.9"

// preflightClock drives startLaunchPreflight without sleeping: the test moves
// the clock instead of waiting out the 2s auth probe budget.
type preflightClock struct{ now time.Time }

func (c *preflightClock) Now() time.Time { return c.now }

func newIdentityPreflight(t *testing.T, clock *preflightClock, token string, authFn func(string, string, *http.Client) (auth.MeResult, bool, error)) *launchPreflight {
	t.Helper()
	return newIdentityPreflightFor(t, clock, token, "https://auth.example", authFn)
}

// newIdentityPreflightFor is the same, for the cases that need the probe filed
// against a real host — a test server whose request count is the evidence.
func newIdentityPreflightFor(t *testing.T, clock *preflightClock, token, authHost string, authFn func(string, string, *http.Client) (auth.MeResult, bool, error)) *launchPreflight {
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
	return startLaunchPreflight(token, authHost, false, deps)
}

// waitForPreflightAuth blocks until the preflight has stored its auth result.
//
// Needed because point D forbids welcomeScreenState from waiting for the
// answer itself: a test that wants the fresh-answer branch has to know the
// answer already arrived, or it races the goroutine that stores it. The timeout
// is a failure guard, not a delay — these fixtures answer immediately.
func waitForPreflightAuth(t *testing.T, p *launchPreflight) {
	t.Helper()
	select {
	case <-p.authDone:
	case <-time.After(5 * time.Second):
		t.Fatal("preflight auth never completed")
	}
}

// welcomeScreenStateOnly keeps the subtests that are about the drawn frame
// readable: welcomeScreenState answers with the pair (frame, late channel) from
// a single poll, and these cases assert on the frame half.
func welcomeScreenStateOnly(local welcome.AuthState, p *launchPreflight, token, authHost string) welcome.AuthState {
	state, _ := welcomeScreenState(local, p, token, authHost)
	return state
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
		waitForPreflightAuth(t, p)

		local := welcome.AuthState{LoggedIn: false, UpdateNudge: welcomeIdentityNudge}
		got := welcomeScreenStateOnly(local, p, "tok", "https://auth.example")

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
		waitForPreflightAuth(t, p)

		got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

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
		waitForPreflightAuth(t, p)

		got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

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

	// Point A. Nothing else writes the me cache any more, so the identity shown
	// after the preflight goes stale can only come from here.
	t.Run("FreshPreflightAnswerIsWrittenToTheMeCache", func(t *testing.T) {
		withTempHome(t) // empty home: whatever ends up in the cache was put there by this call
		base := time.Now()
		clock := &preflightClock{now: base}
		balance := 12.5
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
		})
		waitForPreflightAuth(t, p)

		welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

		cached, ok := readMeCache("https://auth.example", "tok", time.Now())
		if !ok {
			t.Fatal("me cache holds nothing after a fresh preflight answer")
		}
		if cached.Me.Email != "fresh@example.com" || cached.Me.UserID != "u-fresh" {
			t.Fatalf("cached identity = %+v, want email fresh@example.com and userID u-fresh", cached.Me)
		}
	})

	// Point B, the half no fixture used to reach: the cache window is still open,
	// so readMeCache answers Stale=false and hands back the balance it stored.
	// Neither may be believed — nobody asked the server on this launch.
	t.Run("CachedIdentityIsUnverifiedAndMoneylessWhileCacheWindowIsOpen", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		cachedBalance := 77.77
		writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com", BalanceUsd: &cachedBalance}, base)
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{}, false, errors.New("session verification unavailable")
		})
		waitForPreflightAuth(t, p)

		got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

		if got.Identity != "cached@example.com" {
			t.Fatalf("Identity = %q, want %q", got.Identity, "cached@example.com")
		}
		if !got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = false on an unverified cache read (%+v)", got)
		}
		if got.BalanceUsd != nil {
			t.Fatalf("BalanceUsd = %v, want nil — the cached amount was never re-checked", *got.BalanceUsd)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})

	// No fresh answer, but the last known identity is on disk: show it, mark it
	// unverified, and say nothing about money — the balance would be a guess.
	noFreshAnswer := map[string]func(t *testing.T, clock *preflightClock) *launchPreflight{
		"preflightNotReusable": func(t *testing.T, clock *preflightClock) *launchPreflight {
			// Started for another token: the preflight's answer is not ours.
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
			waitForPreflightAuth(t, p)

			got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

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
		waitForPreflightAuth(t, p)

		got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

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

	// Point C, stated directly: with nothing fresh and nothing cached, an
	// identity the caller already holds stays on the screen.
	t.Run("KnownLocalIdentityIsNotDowngradedToEmpty", func(t *testing.T) {
		withTempHome(t) // empty home: no cache to fall back on
		base := time.Now()
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "a-different-token", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-other", Email: "other@example.com"}, true, nil
		})
		waitForPreflightAuth(t, p)

		local := welcome.AuthState{LoggedIn: true, Identity: "known@example.com", IdentityUnverified: true, UpdateNudge: welcomeIdentityNudge}
		got := welcomeScreenStateOnly(local, p, "tok", "https://auth.example")

		if got.Identity != "known@example.com" {
			t.Fatalf("Identity = %q, want %q — a known identity must not be replaced by nothing", got.Identity, "known@example.com")
		}
		if !got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = false without a fresh answer (%+v)", got)
		}
		if got.BalanceUsd != nil {
			t.Fatalf("BalanceUsd = %v, want nil", *got.BalanceUsd)
		}
		if got.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", got.UpdateNudge, welcomeIdentityNudge)
		}
	})

	// Point C as the user meets it: the screen is rebuilt on every menu
	// iteration, so after five minutes in the menu (launchPreflightFreshness)
	// the preflight is no longer reusable. Coming back from `Run doctor` must
	// not turn a named user back into "identity temporarily unavailable".
	t.Run("IdentitySurvivesReturningToTheMenuAfterThePreflightWentStale", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		clock := &preflightClock{now: base}
		balance := 12.5
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
		})
		waitForPreflightAuth(t, p)

		first := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")
		if first.Identity != "fresh@example.com" {
			t.Fatalf("first render Identity = %q, want %q", first.Identity, "fresh@example.com")
		}

		// Five minutes pass in the menu; the preflight stops being reusable.
		clock.now = clock.now.Add(launchPreflightFreshness + time.Minute)
		second := welcomeScreenStateOnly(first, p, "tok", "https://auth.example")

		if second.Identity != "fresh@example.com" {
			t.Fatalf("Identity after the preflight went stale = %q, want %q", second.Identity, "fresh@example.com")
		}
		if !second.LoggedIn {
			t.Fatalf("LoggedIn = false on the second render (%+v)", second)
		}
		if second.UpdateNudge != welcomeIdentityNudge {
			t.Fatalf("UpdateNudge = %q, want %q", second.UpdateNudge, welcomeIdentityNudge)
		}
	})

	// The empty identity is still the right answer when there is no identity
	// anywhere — the one case in which the screen may say so.
	t.Run("NoIdentityAnywhereLeavesItUnknown", func(t *testing.T) {
		withTempHome(t) // empty home: no me cache at all
		base := time.Now()
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			return auth.MeResult{}, false, errors.New("unavailable")
		})
		waitForPreflightAuth(t, p)

		got := welcomeScreenStateOnly(loggedInLocalState(), p, "tok", "https://auth.example")

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

	// Point D. The landing screen is designed to render before any optional
	// network request completes (ae7b0a5); a state builder that waits for the
	// auth probe puts the whole 2s budget in front of the first frame.
	t.Run("StateIsBuiltWithoutWaitingForTheNetwork", func(t *testing.T) {
		withTempHome(t)
		base := time.Now()
		writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
		// Released and drained inside the subtest, not from t.Cleanup: cleanups
		// run last-registered-first, so a release registered after withTempHome
		// wakes the probe goroutine while the temporary home is being removed,
		// and it files its answer into a directory mid-deletion. That made this
		// subtest fail at random and, worse, made a mutant's red
		// indistinguishable from noise.
		blocked := make(chan struct{})
		clock := &preflightClock{now: base}
		p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
			<-blocked // the server never answers while this test runs
			return auth.MeResult{UserID: "u-late", Email: "late@example.com"}, true, nil
		})

		start := time.Now()
		got, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")
		elapsed := time.Since(start)

		// Well under authProbeTimeout (2s): the point is that nothing is waited
		// on, and a generous ceiling keeps the check about blocking rather than
		// about how fast this machine happens to be.
		if limit := 200 * time.Millisecond; elapsed > limit {
			t.Fatalf("building the state took %v, want under %v — the first render must not wait for the auth probe", elapsed, limit)
		}
		if got.Identity != "cached@example.com" {
			t.Fatalf("Identity = %q, want %q from the cache while the answer is still in flight", got.Identity, "cached@example.com")
		}
		if !got.IdentityUnverified {
			t.Fatalf("IdentityUnverified = false while the answer is still in flight (%+v)", got)
		}

		// Waiting for the answer is not enough: the watcher files it in the
		// cache after that, under the home this subtest is about to delete.
		// Draining the channel to close is what proves the watcher is finished.
		close(blocked)
		waitForPreflightAuth(t, p)
		for range late {
		}
	})
}
