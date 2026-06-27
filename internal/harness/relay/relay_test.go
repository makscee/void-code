package relay_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/harness/relay"
)

// --- BuildEnv tests ---

// parentEnv returns a minimal parent env slice to use in table tests.
func parentEnv() []string {
	return []string{
		"HOME=/home/user",
		"PATH=/usr/bin:/bin",
		"HTTPS_PROXY=http://old-proxy:1234",       // must be stripped
		"NODE_EXTRA_CA_CERTS=/old/ca.pem",         // must be stripped
		"CLAUDE_CODE_OAUTH_TOKEN=old-oauth-token", // must be stripped (never re-emitted)
		"ANTHROPIC_AUTH_TOKEN=stale-bearer",       // must be stripped (re-set to relay token)
		"ANTHROPIC_API_KEY=sk-ant-old",            // must be stripped (re-set empty)
		"ANTHROPIC_BASE_URL=https://old-base.com", // must be stripped (re-set to relay)
		"USER_SET_VAR=keep-me",                    // must survive
	}
}

func findEnv(slice []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range slice {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

func TestBuildEnv_StripsParentKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok")

	// HTTPS_PROXY and NODE_EXTRA_CA_CERTS are no longer emitted at all: CC reaches
	// the relay via ANTHROPIC_BASE_URL, not a global proxy. They must be absent
	// from the output (stripped from the parent, never re-added).
	// CLAUDE_CODE_OAUTH_TOKEN is likewise stripped and never re-emitted — the
	// bearer is carried by ANTHROPIC_AUTH_TOKEN instead (VCD-060).
	for _, k := range []string{"HTTPS_PROXY", "NODE_EXTRA_CA_CERTS", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if val, found := findEnv(result, k); found {
			t.Errorf("%s: want absent, got %q", k, val)
		}
	}

	// The remaining relay keys must carry OUR values, never the parent's.
	checks := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "tok",                          // the relay bearer
		"ANTHROPIC_API_KEY":    "",                             // not "sk-ant-old"
		"ANTHROPIC_BASE_URL":   "https://relay.makscee.ru:443", // not "https://old-base.com"
	}
	for k, want := range checks {
		val, found := findEnv(result, k)
		if !found {
			t.Errorf("%s: not found in result", k)
			continue
		}
		if val != want {
			t.Errorf("%s: want %q, got %q", k, want, val)
		}
	}
}

func TestBuildEnv_PreservesUnrelatedKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok")

	val, found := findEnv(result, "USER_SET_VAR")
	if !found {
		t.Error("USER_SET_VAR was stripped but should have been preserved")
	}
	if val != "keep-me" {
		t.Errorf("USER_SET_VAR: want keep-me, got %q", val)
	}

	val, found = findEnv(result, "HOME")
	if !found || val != "/home/user" {
		t.Errorf("HOME not preserved: found=%v val=%q", found, val)
	}
}

func TestBuildEnv_SetsRequiredKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "my-token")

	tests := []struct {
		key  string
		want string
	}{
		{"ANTHROPIC_AUTH_TOKEN", "my-token"},
		{"ANTHROPIC_API_KEY", ""},
		{"ANTHROPIC_BASE_URL", "https://relay.makscee.ru:443"},
	}
	for _, tc := range tests {
		val, found := findEnv(result, tc.key)
		if !found {
			t.Errorf("%s: not found in result", tc.key)
			continue
		}
		if val != tc.want {
			t.Errorf("%s: want %q, got %q", tc.key, tc.want, val)
		}
	}

	// The forward-proxy keys are no longer emitted.
	for _, k := range []string{"HTTPS_PROXY", "NODE_EXTRA_CA_CERTS"} {
		if val, found := findEnv(result, k); found {
			t.Errorf("%s: want absent, got %q", k, val)
		}
	}
}

func TestBuildEnv_HttpScheme(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "http", "relay.makscee.ru:8448", "tok")

	val, found := findEnv(result, "ANTHROPIC_BASE_URL")
	if !found {
		t.Fatal("ANTHROPIC_BASE_URL not found")
	}
	if val != "http://relay.makscee.ru:8448" {
		t.Errorf("ANTHROPIC_BASE_URL: want http://relay.makscee.ru:8448, got %q", val)
	}
}

func TestBuildEnv_NoDuplicateKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok")

	seen := map[string]int{}
	for _, e := range result {
		k, _, _ := strings.Cut(e, "=")
		seen[k]++
	}
	for k, count := range seen {
		if count > 1 {
			t.Errorf("key %q appears %d times (want 1)", k, count)
		}
	}
}

func TestBuildEnv_EmptyParent(t *testing.T) {
	result := relay.BuildEnv(nil, "https", "relay.makscee.ru:443", "tok")
	if len(result) == 0 {
		t.Fatal("expected non-empty result with nil parent")
	}
	val, _ := findEnv(result, "ANTHROPIC_BASE_URL")
	if val != "https://relay.makscee.ru:443" {
		t.Errorf("ANTHROPIC_BASE_URL: got %q", val)
	}
}

// TestBuildEnv_StaleOAuthTokenStripped is a focused regression for VCD-060: a
// parent env carrying a stale CLAUDE_CODE_OAUTH_TOKEN must never appear in the
// child env (it would let the stored account override our bearer and 401 the
// relay). The bearer must be carried solely by ANTHROPIC_AUTH_TOKEN=<relay token>.
func TestBuildEnv_StaleOAuthTokenStripped(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"CLAUDE_CODE_OAUTH_TOKEN=parentval",
		"ANTHROPIC_AUTH_TOKEN=parent-bearer",
	}
	result := relay.BuildEnv(parent, "https", "relay.makscee.ru:443", "relaytoken")

	if val, found := findEnv(result, "CLAUDE_CODE_OAUTH_TOKEN"); found {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN must be absent, got %q", val)
	}
	val, found := findEnv(result, "ANTHROPIC_AUTH_TOKEN")
	if !found {
		t.Fatal("ANTHROPIC_AUTH_TOKEN not found in result")
	}
	if val != "relaytoken" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN: want relaytoken, got %q", val)
	}
}

// --- FetchCA tests ---

func TestFetchCA_WritesToCacheDir(t *testing.T) {
	certContent := "-----BEGIN CERTIFICATE-----\nfake-cert\n-----END CERTIFICATE-----\n"

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/vc/relay-ca.pem" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write([]byte(certContent))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	caPath, err := relay.FetchCA(srv.Client(), srv.URL, cacheDir)
	if err != nil {
		t.Fatalf("FetchCA error: %v", err)
	}

	expected := filepath.Join(cacheDir, "relay-ca.pem")
	if caPath != expected {
		t.Errorf("caPath: want %q, got %q", expected, caPath)
	}

	data, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("cannot read cached file: %v", err)
	}
	if string(data) != certContent {
		t.Errorf("cached content: want %q, got %q", certContent, string(data))
	}
}

func TestFetchCA_ReturnsCachedFile(t *testing.T) {
	certContent := "-----BEGIN CERTIFICATE-----\ncached-cert\n-----END CERTIFICATE-----\n"

	// Server that counts requests — should be called 0 times if cache hits.
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(certContent))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	// Pre-populate the cache.
	cachedPath := filepath.Join(cacheDir, "relay-ca.pem")
	if err := os.WriteFile(cachedPath, []byte(certContent), 0600); err != nil {
		t.Fatal(err)
	}

	caPath, err := relay.FetchCA(srv.Client(), srv.URL, cacheDir)
	if err != nil {
		t.Fatalf("FetchCA error: %v", err)
	}
	if caPath != cachedPath {
		t.Errorf("caPath: want %q, got %q", cachedPath, caPath)
	}
	if calls != 0 {
		t.Errorf("expected 0 server calls (cache hit), got %d", calls)
	}
}

func TestFetchCA_ServerError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	_, err := relay.FetchCA(srv.Client(), srv.URL, cacheDir)
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
}

func TestFetchCA_NetworkError(t *testing.T) {
	// Use a URL that will refuse connection.
	cacheDir := t.TempDir()
	_, err := relay.FetchCA(http.DefaultClient, "https://127.0.0.1:19999", cacheDir)
	if err == nil {
		t.Fatal("expected error on network failure, got nil")
	}
}
