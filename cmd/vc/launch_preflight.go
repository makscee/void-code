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

type launchPreflight struct {
	token, authHost string
	started         time.Time
	deps            launchPreflightDeps
	authDone        chan struct{}
	updateDone      chan struct{}
	mu              sync.RWMutex
	cache           probeCachePaths
	cacheOnce       sync.Once
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
	p := &launchPreflight{token: token, authHost: authHost, started: deps.now(), deps: deps, cache: newProbeCachePaths(authHost, token), authDone: make(chan struct{}), updateDone: make(chan struct{})}
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

// answerIfDone reports the probe's stored answer if it has already arrived.
// It neither waits nor looks at the clock: reusability is a separate question,
// asked once per frame by the caller, and asking it again here is how an answer
// that lands mid-frame gets counted as "too late" by one half and "already
// handled" by the other.
// fileAnswer files the answer in the me cache, exactly once per probe, before
// the answer is shown to anyone: a caller that sees it sees the cache it
// produced, never the one it replaced.
//
// Filed by the first reader rather than by the probe goroutine itself: an
// answer nobody ever reads belongs to a launch that is already over, and
// writing it there means creating files under a home the caller may have
// finished with.
func (p *launchPreflight) fileAnswer(answer launchAuthResult) {
	p.cacheOnce.Do(func() {
		recordProbeInMeCache(p.cache, p.token, answer.me, answer.reached, answer.err)
	})
}

func (p *launchPreflight) answerIfDone() (launchAuthResult, bool) {
	select {
	case <-p.authDone:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.authResult, true
	default:
		return launchAuthResult{}, false
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
