package main

import (
	"github.com/makscee/void-code/internal/auth"
	"net/http"
	"testing"
	"time"
)

func TestLaunchPreflightChecksAuthAndUpdateWithoutProviderDiscovery(t *testing.T) {
	done := make(chan struct{})
	deps := launchPreflightDeps{now: time.Now, auth: func(token, host string, _ *http.Client) (auth.MeResult, bool, error) {
		if token != "t" || host != "h" {
			t.Fatal("bad auth inputs")
		}
		close(done)
		return auth.MeResult{}, true, nil
	}, update: func() string { return "update" }, newClient: func() *http.Client { return &http.Client{} }, diagnostics: newLaunchDiagnostics(false, time.Now, nil)}
	p := startLaunchPreflight("t", "h", true, deps)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("auth did not start")
	}
	if r := awaitPreflightAuth(t, p); r.err != nil {
		t.Fatalf("auth result err=%v", r.err)
	}
}

// awaitPreflightAuth waits for the preflight's /v1/vc/me check to finish and
// returns what it got.
func awaitPreflightAuth(t *testing.T, p *launchPreflight) launchAuthResult {
	t.Helper()
	select {
	case <-p.authDone:
	case <-time.After(5 * time.Second):
		t.Fatal("preflight auth did not finish")
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.authResult
}
