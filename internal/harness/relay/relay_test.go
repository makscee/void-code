package relay_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/harness/relay"
)

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
