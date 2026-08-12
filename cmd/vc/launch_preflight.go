package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/welcome"
)

const launchPreflightFreshness = 5 * time.Minute

type launchAuthResult struct {
	me      auth.MeResult
	reached bool
	err     error
}

type launchProviderResult struct {
	rows   []welcome.ProviderRowInfo
	grants []compat.Grant
	err    error
}

type launchPreflightDeps struct {
	now       func() time.Time
	auth      func(string, string, *http.Client) (auth.MeResult, bool, error)
	providers func(string, string, *http.Client) ([]auth.ProviderInfo, error)
	update    func() string
	newClient func() *http.Client
}

var currentLaunchPreflight *launchPreflight

type launchPreflight struct {
	token, authHost string
	started         time.Time
	deps            launchPreflightDeps

	authDone       chan struct{}
	providersDone  chan struct{}
	updateDone     chan struct{}
	mu             sync.RWMutex
	authResult     launchAuthResult
	providerResult launchProviderResult
	updateNudge    string
}

func defaultLaunchPreflightDeps() launchPreflightDeps {
	return launchPreflightDeps{
		now:       time.Now,
		auth:      authGate,
		providers: cachedFetchProviders,
		update:    launchUpdateCheck,
		newClient: func() *http.Client { return &http.Client{Timeout: authProbeTimeout} },
	}
}

func startLaunchPreflight(token, authHost string, withUpdate bool, deps launchPreflightDeps) *launchPreflight {
	p := &launchPreflight{
		token: token, authHost: authHost, started: deps.now(), deps: deps,
		authDone: make(chan struct{}), providersDone: make(chan struct{}), updateDone: make(chan struct{}),
	}
	go func() {
		me, reached, err := deps.auth(token, authHost, deps.newClient())
		p.mu.Lock()
		p.authResult = launchAuthResult{me: me, reached: reached, err: err}
		p.mu.Unlock()
		close(p.authDone)
	}()
	go func() {
		result := launchProviderResult{}
		if token != "" {
			infos, err := deps.providers(authHost, token, deps.newClient())
			result.err = err
			if err == nil {
				result.rows = make([]welcome.ProviderRowInfo, 0, len(infos))
				result.grants = make([]compat.Grant, 0, len(infos))
				for _, info := range infos {
					result.rows = append(result.rows, welcome.ProviderRowInfo{ID: info.ID, Name: info.Name, Type: info.Type})
					result.grants = append(result.grants, compat.Grant{ID: info.ID, Name: info.Name, Type: info.Type})
				}
			}
		}
		p.mu.Lock()
		p.providerResult = result
		p.mu.Unlock()
		close(p.providersDone)
	}()
	if withUpdate {
		go func() {
			nudge := deps.update()
			p.mu.Lock()
			p.updateNudge = nudge
			p.mu.Unlock()
			close(p.updateDone)
		}()
	} else {
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
			// The existing transient-network policy is non-blocking.
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

func (p *launchPreflight) providerIfReady(token, authHost string) (launchProviderResult, bool) {
	if !p.reusable(token, authHost) {
		return launchProviderResult{}, false
	}
	select {
	case <-p.providersDone:
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.providerResult, true
	default:
		return launchProviderResult{}, false
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
