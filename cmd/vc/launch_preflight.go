package main

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

type launchAuthResult struct {
	me      auth.MeResult
	reached bool
	err     error
}

type launchPreflightDeps struct {
	now         func() time.Time
	auth        func(string, string, *http.Client) (auth.MeResult, bool, error)
	update      func() string
	newClient   func() *http.Client
	diagnostics *launchDiagnostics
}

var currentLaunchPreflight *launchPreflight

type launchPreflight struct {
	token, authHost string
	started         time.Time
	deps            launchPreflightDeps
	authDone        chan struct{}
	updateDone      chan struct{}
	mu              sync.RWMutex
	authResult      launchAuthResult
	updateNudge     string
}

func defaultLaunchPreflightDeps() launchPreflightDeps {
	return launchPreflightDeps{now: time.Now, auth: authGate, update: launchUpdateCheck, newClient: func() *http.Client { return &http.Client{Timeout: authProbeTimeout} }, diagnostics: currentLaunchDiagnostics}
}

// startLaunchPreflight admits authentication and checks for updates. Provider
// discovery is intentionally not a launch preflight: the managed Pi extension
// obtains the current subscription capabilities from pi-bootstrap when Pi starts.
func startLaunchPreflight(token, authHost string, withUpdate bool, deps launchPreflightDeps) *launchPreflight {
	p := &launchPreflight{token: token, authHost: authHost, started: deps.now(), deps: deps, authDone: make(chan struct{}), updateDone: make(chan struct{})}
	go func() {
		me, reached, err := deps.auth(token, authHost, deps.newClient())
		p.mu.Lock()
		p.authResult = launchAuthResult{me: me, reached: reached, err: err}
		p.mu.Unlock()
		outcome, source := outcomeComplete, sourceTransient
		if err != nil {
			outcome, source = outcomeRejected, sourceRejected
		} else if reached {
			source = sourceFresh
		}
		deps.diagnostics.record(phaseAuthComplete, outcome, source)
		close(p.authDone)
	}()
	if withUpdate {
		go func() {
			nudge := deps.update()
			p.mu.Lock()
			p.updateNudge = nudge
			p.mu.Unlock()
			deps.diagnostics.record(phaseUpdateComplete, outcomeComplete, sourceFresh)
			close(p.updateDone)
		}()
	} else {
		deps.diagnostics.record(phaseUpdateComplete, outcomeComplete, sourceLocal)
		close(p.updateDone)
	}
	return p
}

func (p *launchPreflight) updateIfReady() (string, bool) {
	select {
	case <-p.updateDone:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.updateNudge, true
	default:
		return "", false
	}
}

// accountIfReady is what this launch's own /v1/vc/me reported, for the
// welcome screen, once that answer is in (ready=false before): the verified
// identity (the email, else the user id) and the wallet rendered. A refusal or
// a failed check vouches for no one and no wallet: a failed check is
// AccountMsg{Failed: true}, so the screen stops saying it is checking; a
// refusal (auth.ErrAccessNotGranted) ran and answered, and is
// AccountMsg{Refused: true} — «доступ не выдан» (void-works#90). The balance is "" too when the answer
// carried no wallet. A rejected token (auth.ErrNotLoggedIn) signs the screen
// out, so the person is offered login instead of a launch that cannot pass.
func (p *launchPreflight) accountIfReady() (account welcome.AccountMsg, ready bool) {
	select {
	case <-p.authDone:
		p.mu.RLock()
		defer p.mu.RUnlock()
		if errors.Is(p.authResult.err, auth.ErrNotLoggedIn) {
			return welcome.AccountMsg{SignedOut: true}, true
		}
		if errors.Is(p.authResult.err, auth.ErrAccessNotGranted) {
			return welcome.AccountMsg{Refused: true}, true
		}
		if p.authResult.err != nil || !p.authResult.reached {
			return welcome.AccountMsg{Failed: true}, true
		}
		me := p.authResult.me
		identity := me.Email
		if identity == "" {
			identity = me.UserID
		}
		// An answer naming no one vouches for no one either.
		return welcome.AccountMsg{Identity: identity, Balance: formatAccount(me, p.deps.now()), Failed: identity == ""}, true
	default:
		return welcome.AccountMsg{}, false
	}
}
