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
		"HTTPS_PROXY=http://old-proxy:1234",          // must be stripped
		"NODE_EXTRA_CA_CERTS=/old/ca.pem",            // must be stripped
		"CLAUDE_CODE_OAUTH_TOKEN=old-oauth-token",    // must be stripped
		"ANTHROPIC_API_KEY=sk-ant-old",               // must be stripped (set empty)
		"ANTHROPIC_BASE_URL=https://old-base.com",    // must be stripped (set empty)
		"USER_SET_VAR=keep-me",                        // must survive
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
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok", "/tmp/ca.pem")

	stripped := []string{
		"HTTPS_PROXY",
		"NODE_EXTRA_CA_CERTS",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
	}
	// These keys must NOT survive from the parent (they are re-set below).
	// We verify the values are what WE set, not what parent had.
	for _, k := range stripped {
		val, found := findEnv(result, k)
		if !found {
			// ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL should appear as empty
			// HTTPS_PROXY and NODE_EXTRA_CA_CERTS should appear with new values
			// CLAUDE_CODE_OAUTH_TOKEN should appear with token
			continue
		}
		// old-proxy, sk-ant-old, old-oauth-token, https://old-base.com must NOT appear
		switch k {
		case "HTTPS_PROXY":
			if val != "https://relay.makscee.ru:443" {
				t.Errorf("HTTPS_PROXY: want https://relay.makscee.ru:443, got %q", val)
			}
		case "NODE_EXTRA_CA_CERTS":
			if val != "/tmp/ca.pem" {
				t.Errorf("NODE_EXTRA_CA_CERTS: want /tmp/ca.pem, got %q", val)
			}
		case "CLAUDE_CODE_OAUTH_TOKEN":
			if val != "tok" {
				t.Errorf("CLAUDE_CODE_OAUTH_TOKEN: want tok, got %q", val)
			}
		case "ANTHROPIC_API_KEY":
			if val != "" {
				t.Errorf("ANTHROPIC_API_KEY: want empty, got %q", val)
			}
		case "ANTHROPIC_BASE_URL":
			if val != "" {
				t.Errorf("ANTHROPIC_BASE_URL: want empty, got %q", val)
			}
		}
	}
}

func TestBuildEnv_PreservesUnrelatedKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok", "/tmp/ca.pem")

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
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "my-token", "/path/to/ca.pem")

	tests := []struct {
		key  string
		want string
	}{
		{"HTTPS_PROXY", "https://relay.makscee.ru:443"},
		{"NODE_EXTRA_CA_CERTS", "/path/to/ca.pem"},
		{"CLAUDE_CODE_OAUTH_TOKEN", "my-token"},
		{"ANTHROPIC_API_KEY", ""},
		{"ANTHROPIC_BASE_URL", ""},
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
}

func TestBuildEnv_HttpScheme(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "http", "relay.makscee.ru:8448", "tok", "/ca.pem")

	val, found := findEnv(result, "HTTPS_PROXY")
	if !found {
		t.Fatal("HTTPS_PROXY not found")
	}
	if val != "http://relay.makscee.ru:8448" {
		t.Errorf("HTTPS_PROXY: want http://relay.makscee.ru:8448, got %q", val)
	}
}

func TestBuildEnv_NoDuplicateKeys(t *testing.T) {
	result := relay.BuildEnv(parentEnv(), "https", "relay.makscee.ru:443", "tok", "/ca.pem")

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
	result := relay.BuildEnv(nil, "https", "relay.makscee.ru:443", "tok", "/ca.pem")
	if len(result) == 0 {
		t.Fatal("expected non-empty result with nil parent")
	}
	val, _ := findEnv(result, "HTTPS_PROXY")
	if val != "https://relay.makscee.ru:443" {
		t.Errorf("HTTPS_PROXY: got %q", val)
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
