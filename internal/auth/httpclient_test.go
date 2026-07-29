package auth

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewHTTPClientTrustsOnlyExplicitAdditionalCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()

	plain, err := NewHTTPClient(time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Get(server.URL); err == nil {
		t.Fatal("system roots unexpectedly trusted fixture CA")
	}

	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	trusted, err := NewHTTPClient(time.Second, caPath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestNewHTTPClientFailsClosedForInvalidCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHTTPClient(time.Second, path); err == nil {
		t.Fatal("invalid CA accepted")
	}
	if _, err := NewHTTPClient(time.Second, filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing CA accepted")
	}
}
