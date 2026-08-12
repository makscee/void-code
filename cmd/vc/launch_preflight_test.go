package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

func testPreflightDeps(now func() time.Time) launchPreflightDeps {
	return launchPreflightDeps{
		now:       now,
		auth:      authGate,
		providers: cachedFetchProviders,
		update:    func() string { return "" },
		newClient: func() *http.Client { return &http.Client{Timeout: authProbeTimeout} },
	}
}

func TestLaunchPreflight_FirstRenderDoesNotWaitForNetwork(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	now := time.Unix(1, 0)
	deps := testPreflightDeps(func() time.Time { return now })
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-blocked
		return auth.MeResult{}, false, nil
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
		<-blocked
		return nil, nil
	}
	deps.update = func() string { <-blocked; return "" }

	started := time.Now()
	p := startLaunchPreflight("legacy", "https://auth.invalid", true, deps)
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("preflight startup blocked first render for %v", elapsed)
	}
	if _, ready := p.providerIfReady("legacy", "https://auth.invalid"); ready {
		t.Fatal("hanging provider unexpectedly ready")
	}
	if nudge, ready := p.updateIfReady(); ready || nudge != "" {
		t.Fatalf("hanging update = (%q, %v), want no nudge and not ready", nudge, ready)
	}
}

func TestLaunchPreflight_OneAuthAndProviderCallThroughSpawn(t *testing.T) {
	var meCalls, providerCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/vc/me":
			meCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user_id":"u"}`))
		case "/v1/vc/providers":
			providerCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	clearAuthCache("me", srv.URL, "legacy")
	clearAuthCache("providers", srv.URL, "legacy")

	deps := testPreflightDeps(time.Now)
	deps.newClient = srv.Client
	p := startLaunchPreflight("legacy", srv.URL, false, deps)
	if _, _, err, reused := p.awaitAuth("legacy", srv.URL); !reused || err != nil {
		t.Fatalf("await auth = reused %v, err %v", reused, err)
	}
	select {
	case <-p.providersDone:
	case <-time.After(time.Second):
		t.Fatal("provider request did not finish")
	}
	if _, ready := p.providerIfReady("legacy", srv.URL); !ready {
		t.Fatal("provider result not reusable")
	}
	if meCalls.Load() != 1 || providerCalls.Load() != 1 {
		t.Fatalf("calls me=%d providers=%d, want one each", meCalls.Load(), providerCalls.Load())
	}
}

func TestLaunchPreflight_StartAwaitsSameAuthoritativeProbe(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	deps := testPreflightDeps(time.Now)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		calls.Add(1)
		<-release
		return auth.MeResult{}, false, errors.New("revoked")
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) { return nil, nil }
	p := startLaunchPreflight("legacy", "host", false, deps)

	result := make(chan error, 1)
	go func() {
		_, _, err, reused := p.awaitAuth("legacy", "host")
		if !reused {
			result <- errors.New("probe not reused")
			return
		}
		result <- err
	}()
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	if err := <-result; err == nil || err.Error() != "revoked" {
		t.Fatalf("await error = %v, want revoked", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("auth calls = %d, want 1", calls.Load())
	}
}

func TestLaunchPreflight_TimeoutAndIdentityAuthority(t *testing.T) {
	now := time.Now()
	deps := testPreflightDeps(func() time.Time { return now })
	blocked := make(chan struct{})
	defer close(blocked)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		<-blocked
		return auth.MeResult{}, false, nil
	}
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) { return nil, nil }
	p := startLaunchPreflight("legacy", "host", false, deps)
	now = now.Add(authProbeTimeout)
	if _, reached, err, reused := p.awaitAuth("legacy", "host"); !reused || reached || err != nil {
		t.Fatalf("bounded transient = reused %v reached %v err %v", reused, reached, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	if _, _, err := authGate("legacy", srv.URL, srv.Client()); err == nil {
		t.Fatal("legacy 401 must block")
	}
	if _, reached, err := authGate("session.secret", srv.URL, srv.Client()); err != nil || reached {
		t.Fatalf("identity 401 must remain relay-authoritative: reached %v err %v", reached, err)
	}
}
