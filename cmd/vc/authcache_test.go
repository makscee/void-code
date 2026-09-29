package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// withTempHome points the home directory at a throwaway one, for tests that
// write VC state (~/.void-code/token) as a side effect.
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

	// An older vc cached the grant list on disk. Discovery must still ask the
	// server, and it removes the leftover file.
	stale, err := authCachePath("providers", srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"value":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale providers cache still present: %v", err)
	}
}
