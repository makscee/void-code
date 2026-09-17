package main

import (
	"time"

	"github.com/makscee/void-code/internal/welcome"
)

// welcomeStateFromPreflight names who is logged in on the welcome screen.
//
// Local state (resolveLocalAuthStateWithSource) only knows that a token exists;
// the launch preflight is the one that asked the server. This joins the two:
//
//  1. a logged-out screen is returned untouched — neither a fresh answer nor a
//     cached identity may leak into it;
//  2. a fresh preflight answer names the identity, marks it verified and
//     carries the balance the server sent;
//  A. that answer is also written to the me cache: nothing else writes it any
//     more (cachedFetchMeState lost its production callers in 8ebfcef), so
//     without this write a "last known identity" never comes to exist;
//  B. anything read back from the cache is shown unverified and without money,
//     open fresh window or not — nobody asked the server on this launch;
//  C. an identity the caller already holds is never downgraded to an empty one.
//     The screen is rebuilt on every menu iteration, and after
//     launchPreflightFreshness the preflight has nothing left to rebuild from;
//  D. nothing here waits for the network: authIfReady answers only if the probe
//     is already done, so the first frame is not held behind it. The answer that
//     arrives later reaches the drawn screen through welcome.IdentityUpdate;
//  5. the update nudge already in local state survives every branch.
func welcomeStateFromPreflight(local welcome.AuthState, p *launchPreflight, token, authHost string) welcome.AuthState {
	if !local.LoggedIn {
		return local
	}
	if p != nil {
		if me, reached, err, ready := p.authIfReady(token, authHost); ready && err == nil && reached {
			writeMeCache(authHost, token, me, time.Now())
			return keepKnown(local, meResultToState(me))
		}
	}
	if cached, ok := readMeCache(authHost, token, time.Now()); ok {
		return keepKnown(local, staleMeResultToState(cached.Me))
	}
	return keepKnown(local, welcome.AuthState{LoggedIn: true, IdentityUnverified: true})
}

// keepKnown carries the parts of the screen the preflight has no opinion about:
// the update nudge, and a name that was already known when the new state does
// not carry one.
func keepKnown(local, next welcome.AuthState) welcome.AuthState {
	next.UpdateNudge = local.UpdateNudge
	if next.Identity == "" {
		next.Identity = local.Identity
	}
	return next
}

// watchLateIdentity waits for the preflight answer the first frame could not,
// and hands it to the already-drawn screen. A nil channel means there is
// nothing to wait for: no preflight of ours, or its answer is already in the
// state about to render.
func watchLateIdentity(local welcome.AuthState, p *launchPreflight, token, authHost string) <-chan welcome.IdentityUpdate {
	if p == nil || !local.LoggedIn || !p.reusable(token, authHost) {
		return nil
	}
	if _, _, _, ready := p.authIfReady(token, authHost); ready {
		return nil
	}
	late := make(chan welcome.IdentityUpdate, 1)
	go func() {
		defer close(late)
		<-p.authDone
		late <- welcome.IdentityUpdate{AuthState: welcomeStateFromPreflight(local, p, token, authHost)}
	}()
	return late
}
