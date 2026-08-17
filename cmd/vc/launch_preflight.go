package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

const launchPreflightFreshness = 5 * time.Minute

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

func (p *launchPreflight) reusable(token, authHost string) bool {
	return p != nil && token == p.token && authHost == p.authHost && p.deps.now().Sub(p.started) <= launchPreflightFreshness
}
func (p *launchPreflight) awaitAuth(token, authHost string) (auth.MeResult, bool, error, bool) {
	if !p.reusable(token, authHost) {
		return auth.MeResult{}, false, nil, false
	}
	remaining := authProbeTimeout - p.deps.now().Sub(p.started)
	if remaining > 0 {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-p.authDone:
		case <-timer.C:
			return auth.MeResult{}, false, nil, true
		}
	}
	select {
	case <-p.authDone:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.authResult.me, p.authResult.reached, p.authResult.err, true
	default:
		return auth.MeResult{}, false, nil, true
	}
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
