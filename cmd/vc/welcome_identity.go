package main

import (
	"time"

	"github.com/makscee/void-code/internal/welcome"
)

// welcomeStateFromPreflight names who is logged in on the welcome screen.
//
// Local state (resolveLocalAuthStateWithSource) only knows that a token exists;
// the launch preflight has already asked the server. This joins the two:
//
//  1. a logged-out screen is returned untouched — neither a fresh answer nor a
//     cached identity may leak into it;
//  2. a fresh preflight answer wins: it names the identity, marks it verified
//     and carries the balance the server sent;
//  3. without a fresh answer the last known identity from the me cache is shown
//     and marked unverified, with no balance — that would be a guess;
//  4. with neither, the identity stays empty and the screen says so;
//  5. the update nudge already in local state survives every branch.
func welcomeStateFromPreflight(local welcome.AuthState, p *launchPreflight, token, authHost string) welcome.AuthState {
	if !local.LoggedIn {
		return local
	}
	if p != nil {
		if me, reached, err, reused := p.awaitAuth(token, authHost); reused && err == nil && reached {
			state := meResultToState(me)
			state.UpdateNudge = local.UpdateNudge
			return state
		}
	}
	if cached, ok := readMeCache(authHost, token, time.Now()); ok {
		var state welcome.AuthState
		if cached.Stale {
			state = staleMeResultToState(cached.Me)
		} else {
			state = meResultToState(cached.Me)
		}
		state.UpdateNudge = local.UpdateNudge
		return state
	}
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true, UpdateNudge: local.UpdateNudge}
}
