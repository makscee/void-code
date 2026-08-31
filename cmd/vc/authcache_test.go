package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// withTempHome points the home directory at a throwaway one, for tests that
// write VC state (~/.void-code/token, the auth cache) as a side effect.
//
// Both variables, because os.UserHomeDir does not read the same one everywhere:
// HOME on unix, USERPROFILE on Windows. Setting only HOME leaves the Windows
// run resolving the real profile, so every caller of this helper wrote a live
// token into the developer's own ~/.void-code — silently, since on the platform
// the author was using it worked. The package's HOME guard
// (home_isolation_test.go) is what catches it, and it caught exactly this.
func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestCachedFetchMeReusesFreshDiskCache(t *testing.T) {
	withTempHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/vc/me" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"userId":"u1","email":"maks@example.com","pct":42,"resetAt":"2026-08-01T00:00:00Z","balanceUsd":7.5}`))
	}))
	defer srv.Close()

	first, err := cachedFetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := cachedFetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if first.Email != "maks@example.com" || second.Email != first.Email {
		t.Fatalf("cached result mismatch: first=%+v second=%+v", first, second)
	}
	path, err := authCacheDebugPath("me", srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cache stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("cache mode = %o, want 600", info.Mode().Perm())
	}
}

func TestFetchProvidersLive_IgnoresCachedEmptyGrantList(t *testing.T) {
	withTempHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/vc/providers" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"providers":[{"id":"chatgpt-sub","name":"ChatGPT","type":"openai-codex-oauth"}]}`))
	}))
	defer srv.Close()

	// Seed the cache under the server's actual host, then prove discovery still
	// performs a live request and returns the newly issued grant.
	writeAuthCache("providers", srv.URL, "tok", []auth.ProviderInfo{}, time.Now())
	providers, err := fetchProvidersLive(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("fetch providers: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 live request", calls)
	}
	if len(providers) != 1 || providers[0].ID != "chatgpt-sub" {
		t.Fatalf("providers = %+v, want live chatgpt-sub grant", providers)
	}
}

func TestCachedFetchMeBacksOffTransientFailure(t *testing.T) {
	withTempHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "temporarily down", http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := cachedFetchMe(srv.URL, "tok", srv.Client()); err == nil {
		t.Fatal("first fetch unexpectedly succeeded")
	}
	if _, err := cachedFetchMe(srv.URL, "tok", srv.Client()); err == nil {
		t.Fatal("second fetch unexpectedly succeeded")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 transient backoff hit", calls)
	}
}

func TestCachedFetchMeTransientFailurePreservesLastKnownIdentity(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		code          int
		delay         time.Duration
		clientTimeout time.Duration
	}{
		{name: "timeout", code: http.StatusOK, delay: 50 * time.Millisecond, clientTimeout: 5 * time.Millisecond},
		{name: "malformed response", code: http.StatusOK, body: `{not-json`},
		{name: "missing identity", code: http.StatusOK, body: `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withTempHome(t)
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				time.Sleep(tc.delay)
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client := srv.Client()
			client.Timeout = tc.clientTimeout

			writeMeCache(srv.URL, "cache-key", auth.MeResult{UserID: "user-old"}, time.Now().Add(-authCacheTTL-time.Second))
			state, err := cachedFetchMeState(srv.URL, "cache-key", client)
			if err == nil {
				t.Fatal("transient fetch unexpectedly succeeded")
			}
			if !state.Stale || state.Me.UserID != "user-old" {
				t.Fatal("last-known identity was not returned as stale")
			}

			state, err = cachedFetchMeState(srv.URL, "cache-key", client)
			if err == nil || !state.Stale || calls.Load() != 1 {
				t.Fatal("transient backoff did not retain stale state without another request")
			}
		})
	}
}

func TestCachedFetchMeTransientFailureWithoutHistoryIsNeutral(t *testing.T) {
	withTempHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "diagnostic-sentinel", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	for range 2 {
		state, err := cachedFetchMeState(srv.URL, "cache-key", srv.Client())
		if err == nil || state.Me.Email != "" || state.Me.UserID != "" {
			t.Fatal("failure without history must return no attributed identity")
		}
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want bounded backoff", calls)
	}
	path, err := authCacheDebugPath("me-transient", srv.URL, "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "diagnostic-sentinel") || strings.Contains(string(data), "cache-key") {
		t.Fatal("transient cache contains request or server diagnostic material")
	}
}

func TestCachedFetchMeConsumersDoNotUseStaleBudgetOrBalance(t *testing.T) {
	withTempHome(t)
	pct, balance := 90.0, 12.0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{not-json`))
	}))
	defer srv.Close()
	writeMeCache(srv.URL, "cache-key", auth.MeResult{
		UserID: "user-last", Pct: &pct, BalanceUsd: &balance,
	}, time.Now().Add(-authCacheTTL-time.Second))

	me, err := cachedFetchMe(srv.URL, "cache-key", srv.Client())
	if err == nil || me.Pct != nil || me.BalanceUsd != nil {
		t.Fatal("auth gate/statusline consumers treated stale non-identity fields as fresh")
	}
}

func TestCachedFetchMeSuccessfulRefreshReplacesLastKnownAtomically(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"userId":"user-new"}`))
	}))
	defer srv.Close()
	writeMeCache(srv.URL, "cache-key", auth.MeResult{UserID: "user-old"}, time.Now().Add(-authCacheTTL-time.Second))

	state, err := cachedFetchMeState(srv.URL, "cache-key", srv.Client())
	if err != nil || state.Stale || state.Me.UserID != "user-new" {
		t.Fatal("refresh did not return the new fresh identity")
	}
	path, err := authCacheDebugPath("me", srv.URL, "cache-key")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("identity cache must exist with mode 0600")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cache-key") || strings.Contains(string(data), "Authorization") {
		t.Fatal("identity cache contains credential material")
	}
	cached, ok := readMeCache(srv.URL, "cache-key", time.Now().Add(authCacheTTL+time.Second))
	if !ok || !cached.Stale || cached.Me.UserID != "user-new" {
		t.Fatal("last-known record was not updated with the successful refresh")
	}
}

func TestCachedFetchMeExpiredCacheDoesNotMaskUnauthorized(t *testing.T) {
	withTempHome(t)
	writeAuthCache("me", "https://auth.example", "tok", auth.MeResult{Email: "stale@example.com"}, time.Now().Add(-authCacheTTL-1*time.Second))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// Move the expired cache under the real server URL key to prove the stale value is ignored.
	oldPath, err := authCacheDebugPath("me", "https://auth.example", "tok")
	if err != nil {
		t.Fatal(err)
	}
	newPath, err := authCacheDebugPath("me", srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename stale cache: %v", err)
	}
	writeAuthCache("me-transient", srv.URL, "tok", "temporarily unavailable", time.Now().Add(-authCacheTTL-time.Second))
	transientPath, err := authCacheDebugPath("me-transient", srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}

	_, err = cachedFetchMe(srv.URL, "tok", srv.Client())
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
	if _, statErr := os.Stat(newPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale cache was not cleared, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(transientPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("transient cache was not cleared, stat err = %v", statErr)
	}
	if _, err := os.Stat(filepath.Dir(newPath)); err != nil {
		t.Fatalf("cache dir should remain: %v", err)
	}
}

func TestCachedFetchMeIdentityTokenRejectionRetainsLastKnown(t *testing.T) {
	withTempHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	token := "session-part.secret-part"
	writeMeCache(srv.URL, token, auth.MeResult{UserID: "user-last"}, time.Now().Add(-authCacheTTL-time.Second))

	_, err := cachedFetchMeState(srv.URL, token, srv.Client())
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatal("legacy endpoint rejection must remain visible to the relay-boundary caller")
	}
	cached, ok := readMeCache(srv.URL, token, time.Now())
	if !ok || !cached.Stale || cached.Me.UserID != "user-last" {
		t.Fatal("legacy endpoint erased identity-token last-known state")
	}
}
