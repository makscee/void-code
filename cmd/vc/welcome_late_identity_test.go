package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

// Point 2 as far as it is observable from outside: while the answer is still
// in flight the frame says so — the remembered name, unverified, no money —
// and the answer itself arrives on the channel presented as checked.
//
// What this does NOT pin is the two-poll shape that lost an answer landing
// mid-call: welcomeScreenState consults the clock once and the stored answer
// once, and a second read of the same stored answer is invisible from here.
// See the report accompanying this test for the seam that would close it.
func TestWelcomeScreenStatePendingAnswerIsRememberedThenDelivered(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	// A different name in the cache, so "the frame shows the answer" cannot be
	// confused with "the frame shows the last known identity".
	writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
	clock := &preflightClock{now: base}
	unblock := make(chan struct{})
	balance := 12.5
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock
		return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
	})

	frame, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")

	if late == nil {
		t.Fatal("no channel while the answer is still in flight")
	}
	if frame.Identity != "cached@example.com" {
		t.Fatalf("frame Identity = %q, want the last known %q while the answer is still in flight", frame.Identity, "cached@example.com")
	}
	if !frame.IdentityUnverified || frame.BalanceUsd != nil {
		t.Fatalf("frame presents a remembered name as checked (%+v)", frame)
	}

	close(unblock)
	got := receiveLateIdentity(t, late)
	if got.Identity != "fresh@example.com" {
		t.Fatalf("late Identity = %q, want %q", got.Identity, "fresh@example.com")
	}
	if got.IdentityUnverified || got.BalanceUsd == nil {
		t.Fatalf("the delivered answer is not presented as checked (%+v)", got.AuthState)
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

// A probe that failed on the network leaves a transient marker behind it, so
// that the next lookup does not walk into the same timeout.
//
// This observes it through cachedFetchMeState — and that reader has no
// production callers: it is reachable only from cachedFetchMe, which has none
// either (measured: `grep -rn "cachedFetchMeState\|cachedFetchMe(" --include=*.go
// . | grep -v _test` lists definitions only). So the marker is written by live
// code and read by nobody: today nothing in production is faster or gentler for
// it existing. The observation is kept because it is the only one there is, and
// because it does pin the write; what it cannot do is prove the invariant earns
// its keep. The live consumer this is waiting for is the menu's own re-probe
// after `Open profile`, which today starts a fresh probe into whatever failure
// the last one just hit. Either that path learns to consult the marker, or the
// branch that writes it should go — a decision for the owner, not for a test.
func TestFailedProbeIsRememberedSoTheNextLookupDoesNotRetryIntoIt(t *testing.T) {
	withTempHome(t)
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"userId":"u-server","email":"server@example.com"}`))
	}))
	defer srv.Close()

	clock := &preflightClock{now: time.Now()}
	p := newIdentityPreflightFor(t, clock, "tok", srv.URL, func(string, string, *http.Client) (auth.MeResult, bool, error) {
		return auth.MeResult{}, false, errors.New("dial tcp: i/o timeout")
	})
	waitForPreflightAuth(t, p)

	// Drawing the frame is what files the answer, good or bad.
	welcomeScreenState(loggedInLocalState(), p, "tok", srv.URL)

	state, err := cachedFetchMeState(srv.URL, "tok", srv.Client())
	if !errors.Is(err, errAuthTemporarilyUnavailable) {
		t.Fatalf("next lookup returned (%+v, %v), want %v — the failed probe was not remembered", state, err, errAuthTemporarilyUnavailable)
	}
	if requests != 0 {
		t.Fatalf("next lookup made %d request(s) to the server, want 0 — it retried into the failure the probe had just hit", requests)
	}
}

// Hole 3. The answer that arrives after the frame must be filed too. Otherwise
// a user whose answer is late and who starts Pi straight away never acquires a
// "last known identity" at all, and the next launch has nothing to show.
func TestLateAnswerIsFiledInTheMeCache(t *testing.T) {
	withTempHome(t) // empty home: anything in the cache afterwards came from the late answer
	clock := &preflightClock{now: time.Now()}
	unblock := make(chan struct{})
	balance := 12.5
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-unblock
		return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
	})

	_, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")
	if late == nil {
		t.Fatal("no channel while the answer is still in flight")
	}
	if cached, ok := readMeCache("https://auth.example", "tok", time.Now()); ok {
		t.Fatalf("me cache already holds %+v before any answer arrived", cached.Me)
	}

	close(unblock)
	if got := receiveLateIdentity(t, late); got.Identity != "fresh@example.com" {
		t.Fatalf("late Identity = %q, want %q", got.Identity, "fresh@example.com")
	}

	cached, ok := readMeCache("https://auth.example", "tok", time.Now())
	if !ok {
		t.Fatal("me cache holds nothing after the late answer — a launch that answers slowly leaves no last known identity behind")
	}
	if cached.Me.Email != "fresh@example.com" || cached.Me.UserID != "u-fresh" {
		t.Fatalf("cached identity = %+v, want email fresh@example.com and userID u-fresh", cached.Me)
	}
}

// Hole 2, as a fixed scene rather than a race: the cache says one name and the
// answer another. A frame built while the answer was available must show the
// answer — verified and with the balance — not the cached name it supersedes.
func TestFrameShowsTheAvailableAnswerRatherThanTheCachedName(t *testing.T) {
	withTempHome(t)
	base := time.Now()
	writeMeCache("https://auth.example", "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, base.Add(-10*time.Minute))
	clock := &preflightClock{now: base}
	balance := 12.5
	p := newIdentityPreflight(t, clock, "tok", func(string, string, *http.Client) (auth.MeResult, bool, error) {
		return auth.MeResult{UserID: "u-fresh", Email: "fresh@example.com", BalanceUsd: &balance}, true, nil
	})
	waitForPreflightAuth(t, p)

	frame, late := welcomeScreenState(loggedInLocalState(), p, "tok", "https://auth.example")

	if frame.Identity != "fresh@example.com" {
		t.Fatalf("frame Identity = %q, want the answer %q rather than the cached name", frame.Identity, "fresh@example.com")
	}
	if frame.IdentityUnverified {
		t.Fatalf("frame marked unverified while the answer was available (%+v)", frame)
	}
	if frame.BalanceUsd == nil || *frame.BalanceUsd != balance {
		t.Fatalf("frame BalanceUsd = %v, want %v — the answer carried it", frame.BalanceUsd, balance)
	}
	if late != nil {
		t.Fatal("a channel was opened for an answer that is already in the frame")
	}
}

// B2. Every other test here hands the probe a fake auth function, which is
// below authGate — the layer that actually runs in production. So this one
// starts the probe the way defaultLaunchPreflightDeps does, with authGate
// itself, and lets a real 401 travel the whole way: server → FetchMe →
// authGate → the probe's stored answer → the frame. If the rejection loses its
// identity along that path, the screen keeps naming a user whose session is
// gone, and the cached name outlives the session that earned it.
func TestRejectedTokenStopsNamingTheUserThroughTheRealAuthGate(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	// The name a previous, working session left behind.
	writeMeCache(srv.URL, "tok", auth.MeResult{UserID: "u-cached", Email: "cached@example.com"}, time.Now())

	deps := launchPreflightDeps{
		now:         time.Now,
		auth:        authGate, // the production probe, not a stand-in
		update:      func() string { return "" },
		newClient:   srv.Client,
		diagnostics: newLaunchDiagnostics(false, time.Now, nil),
	}
	p := startLaunchPreflight("tok", srv.URL, false, deps)
	waitForPreflightAuth(t, p)

	local := welcome.AuthState{LoggedIn: true, Identity: "cached@example.com", IdentityUnverified: true, UpdateNudge: welcomeIdentityNudge}
	frame, late := welcomeScreenState(local, p, "tok", srv.URL)
	if late != nil {
		t.Fatal("a channel was opened for an answer that already arrived")
	}

	if frame.LoggedIn {
		t.Fatalf("frame still logged in after the server rejected the token (%+v)", frame)
	}
	if frame.Identity != "" {
		t.Fatalf("frame Identity = %q after the server rejected the token, want the screen to stop naming anyone", frame.Identity)
	}
	if frame.UpdateNudge != welcomeIdentityNudge {
		t.Fatalf("UpdateNudge = %q, want %q — losing the session says nothing about updates", frame.UpdateNudge, welcomeIdentityNudge)
	}
	if cached, ok := readMeCache(srv.URL, "tok", time.Now()); ok {
		t.Fatalf("me cache still holds %+v — the next launch would name a user who has no session", cached.Me)
	}

	// And the frame after it, built when the probe is no longer reusable, has
	// nothing left to resurrect the name from either.
	next, _ := welcomeScreenState(frame, nil, "tok", srv.URL)
	if next.Identity != "" {
		t.Fatalf("the next frame names %q again after the rejection", next.Identity)
	}
}
