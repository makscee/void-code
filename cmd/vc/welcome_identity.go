package main

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// welcomeScreenState decides both halves of one welcome frame: the state to
// draw now, and the channel a still-pending answer will arrive on.
//
// Local state (resolveLocalAuthStateWithSource) only knows that a token exists;
// the launch preflight is the one that asked the server. Joining them:
//
//   - a logged-out screen is returned untouched — no identity, late or not;
//   - an answer that already arrived names the identity, marks it verified and
//     carries the balance the server sent;
//   - anything read back from the me cache is shown unverified and without
//     money, open fresh window or not: nobody re-checked it on this launch;
//   - an identity the caller already holds is never downgraded to an empty one
//     (welcome.MergeIdentity — the same rule the drawn screen repaints by);
//   - nothing here waits for the network, so the first frame is not held behind
//     the probe;
//   - the preflight is polled exactly once: an answer that lands mid-call is
//     either in the frame or on the channel, never dropped between two polls;
//   - the token being rejected is the one answer that erases the name instead
//     of keeping it — the cache behind it is dropped by the probe itself;
//   - the update nudge in local state survives every branch.
func welcomeScreenState(local welcome.AuthState, p *launchPreflight, token, authHost string) (welcome.AuthState, <-chan welcome.IdentityUpdate) {
	if !local.LoggedIn {
		return local, nil
	}
	if p == nil || !p.reusable(token, authHost) {
		return lastKnownScreenState(local, token, authHost), nil
	}
	if answer, done := p.answerIfDone(); done {
		p.fileAnswer(answer)
		return screenStateFromAnswer(local, answer, token, authHost), nil
	}
	late := make(chan welcome.IdentityUpdate, 1)
	go func() {
		defer close(late)
		<-p.authDone
		answer, _ := p.answerIfDone()
		p.fileAnswer(answer)
		late <- welcome.IdentityUpdate{AuthState: screenStateFromAnswer(local, answer, token, authHost)}
	}()
	return lastKnownScreenState(local, token, authHost), late
}

// screenStateFromAnswer turns one probe answer into the state to show. The
// preflight already owns what the answer means for the cache; this only decides
// what the user is told.
func screenStateFromAnswer(local welcome.AuthState, answer launchAuthResult, token, authHost string) welcome.AuthState {
	switch {
	case errors.Is(answer.err, auth.ErrNotLoggedIn):
		// The session is gone: the screen stops naming anyone, and the menu it
		// draws next is the logged-out one.
		return welcome.MergeIdentity(local, welcome.AuthState{LoggedIn: false})
	case answer.err == nil && answer.reached:
		return welcome.MergeIdentity(local, meResultToState(answer.me))
	default:
		// Reached without an answer, or not reached at all: the check failed, so
		// the last known identity is all there is — unverified and moneyless.
		return lastKnownScreenState(local, token, authHost)
	}
}

func lastKnownScreenState(local welcome.AuthState, token, authHost string) welcome.AuthState {
	next := welcome.AuthState{LoggedIn: true, IdentityUnverified: true}
	if cached, ok := readMeCache(authHost, token, time.Now()); ok {
		next = staleMeResultToState(cached.Me)
	}
	return welcome.MergeIdentity(local, next)
}

// probeCachePaths pins where the probe's answer will be filed. Resolved when
// the probe starts rather than when it finishes: the answer lands on a
// goroutine that may outlive whatever decided the home directory, and a path
// resolved there can name a different one.
type probeCachePaths struct {
	me, transient string
	ok            bool
}

func newProbeCachePaths(authHost, token string) probeCachePaths {
	if strings.TrimSpace(token) == "" {
		return probeCachePaths{}
	}
	me, err := authCachePath("me", authHost, token)
	if err != nil {
		return probeCachePaths{}
	}
	transient, err := authCachePath("me-transient", authHost, token)
	if err != nil {
		return probeCachePaths{}
	}
	return probeCachePaths{me: me, transient: transient, ok: true}
}

// recordProbeInMeCache carries the me-cache invariants that used to live in
// cachedFetchMeState, which lost its production callers in 8ebfcef: the launch
// probe is now the only thing that asks /me, so it is the only thing that can
// keep the cache honest. A good answer becomes the last known identity, a
// rejected token erases it, and a network failure is remembered as transient so
// the next caller does not retry into the same timeout.
func recordProbeInMeCache(paths probeCachePaths, token string, me auth.MeResult, reached bool, err error) {
	if !paths.ok {
		return
	}
	switch {
	case err == nil && reached:
		writeMeCacheAt(paths.me, me, time.Now())
		_ = os.Remove(paths.transient)
	case errors.Is(err, auth.ErrNotLoggedIn):
		// The identity-token exemption is cachedFetchMeState's, kept verbatim:
		// those tokens are re-minted rather than re-issued, so their cached
		// identity outlives a single rejection.
		if !isIdentityToken(token) {
			_ = os.Remove(paths.me)
			_ = os.Remove(paths.transient)
		}
	case err != nil:
		writeAuthTransientAt(paths.transient, err)
	}
}
