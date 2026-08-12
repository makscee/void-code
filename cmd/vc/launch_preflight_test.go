package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/compat"
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

func TestLaunchPreflight_ProviderOrderIsAuthoritativeAndSingleCall(t *testing.T) {
	for _, authFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "auth-first", false: "providers-first"}[authFirst], func(t *testing.T) {
			piDir := t.TempDir()
			t.Setenv("PI_CODING_AGENT_DIR", piDir)
			t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "1")
			packagePath := managedWebSearchPackagePath()
			dependencyPath := filepath.Join(packagePath, "node_modules", "@mozilla", "readability")
			if err := os.MkdirAll(dependencyPath, 0700); err != nil {
				t.Fatal(err)
			}
			packageJSON := `{"name":"@void-code/pi-web-access","version":"0.13.0-void.1","voidCodeFork":{"patch":"VC-10 managed void-codex seam v1"}}`
			if err := os.WriteFile(filepath.Join(packagePath, "package.json"), []byte(packageJSON), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dependencyPath, "package.json"), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}

			authRelease := make(chan struct{})
			providerRelease := make(chan struct{})
			var authCalls, providerCalls atomic.Int32
			deps := testPreflightDeps(time.Now)
			deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) {
				authCalls.Add(1)
				<-authRelease
				return auth.MeResult{UserID: "u"}, true, nil
			}
			deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
				providerCalls.Add(1)
				<-providerRelease
				return []auth.ProviderInfo{
					{ID: "forged-name", Name: "ChatGPT", Type: "deepseek"},
					{ID: "granted", Name: "Unbranded", Type: "openai-codex-oauth"},
				}, nil
			}
			p := startLaunchPreflight("legacy", "host", false, deps)
			if authFirst {
				close(authRelease)
				if _, _, err, reused := p.awaitAuth("legacy", "host"); err != nil || !reused {
					t.Fatalf("await auth: reused=%v err=%v", reused, err)
				}
				close(providerRelease)
			} else {
				close(providerRelease)
				close(authRelease)
			}
			result, reused := p.awaitProvider("legacy", "host")
			if !reused || result.err != nil {
				t.Fatalf("await provider: reused=%v err=%v", reused, result.err)
			}
			selected, _, eligible := compat.FirstChatGPT(result.grants)
			if !eligible || selected.ID != "granted" {
				t.Fatalf("authoritative eligible provider = %#v, %v", selected, eligible)
			}
			if state, err := reconcileManagedWebSearch(eligible); err != nil || state != managedWebSearchReady {
				t.Fatalf("managed tool registration = %q, %v", state, err)
			}
			if registered, err := inspectManagedPackageSetting(packagePath); err != nil || !registered {
				t.Fatalf("managed tools registered=%v err=%v", registered, err)
			}
			if _, _, err, reused := p.awaitAuth("legacy", "host"); err != nil || !reused {
				t.Fatalf("await auth: reused=%v err=%v", reused, err)
			}
			if authCalls.Load() != 1 || providerCalls.Load() != 1 {
				t.Fatalf("calls auth=%d providers=%d", authCalls.Load(), providerCalls.Load())
			}
		})
	}
}

func TestLaunchPreflight_ConfirmedEmptyIsSuccessfulOutcome(t *testing.T) {
	deps := testPreflightDeps(time.Now)
	deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) { return auth.MeResult{}, true, nil }
	deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) { return []auth.ProviderInfo{}, nil }
	result, reused := startLaunchPreflight("legacy", "host", false, deps).awaitProvider("legacy", "host")
	if !reused || !result.successful() || result.err != nil || result.grants == nil {
		t.Fatalf("confirmed empty outcome = %#v, reused=%v", result, reused)
	}
}

func TestLaunchPreflight_ProviderFailureAndTimeoutAreExplicit(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		deps := testPreflightDeps(time.Now)
		deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) { return auth.MeResult{}, true, nil }
		deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) {
			return nil, errors.New("provider unavailable")
		}
		result, reused := startLaunchPreflight("legacy", "host", false, deps).awaitProvider("legacy", "host")
		if !reused || result.err == nil || result.err.Error() != "provider unavailable" || len(result.grants) != 0 {
			t.Fatalf("provider failure = %#v, reused=%v", result, reused)
		}
	})
	t.Run("bounded-timeout", func(t *testing.T) {
		now := time.Now()
		blocked := make(chan struct{})
		defer close(blocked)
		deps := testPreflightDeps(func() time.Time { return now })
		deps.auth = func(string, string, *http.Client) (auth.MeResult, bool, error) { return auth.MeResult{}, true, nil }
		deps.providers = func(string, string, *http.Client) ([]auth.ProviderInfo, error) { <-blocked; return nil, nil }
		p := startLaunchPreflight("legacy", "host", false, deps)
		now = now.Add(authProbeTimeout)
		result, reused := p.awaitProvider("legacy", "host")
		if !reused || !errors.Is(result.err, errProviderProbeTimeout) || len(result.grants) != 0 {
			t.Fatalf("provider timeout = %#v, reused=%v", result, reused)
		}
	})
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
