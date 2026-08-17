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
	if _, _, err, reused := p.awaitAuth("t", "h"); !reused || err != nil {
		t.Fatalf("auth result reused=%v err=%v", reused, err)
	}
}
