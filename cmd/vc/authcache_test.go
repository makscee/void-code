package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
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

func TestCachedFetchProvidersReusesFreshDiskCache(t *testing.T) {
	withTempHome(t)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/vc/providers" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"providers":[{"id":"plat-2","name":"ChatGPT Sub","type":"openai-codex-oauth"}]}`))
	}))
	defer srv.Close()

	first, err := cachedFetchProviders(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := cachedFetchProviders(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	if len(first) != 1 || len(second) != 1 || second[0].ID != "plat-2" {
		t.Fatalf("cached providers mismatch: first=%+v second=%+v", first, second)
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

	_, err = cachedFetchMe(srv.URL, "tok", srv.Client())
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
	if _, statErr := os.Stat(newPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale cache was not cleared, stat err = %v", statErr)
	}
	if _, err := os.Stat(filepath.Dir(newPath)); err != nil {
		t.Fatalf("cache dir should remain: %v", err)
	}
}
