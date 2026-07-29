package auth

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

// NewHTTPClient builds an identity client using system trust plus an explicitly
// configured deployment CA. The CA is process-local: it is never installed in
// the operating-system trust store and invalid input fails closed.
func NewHTTPClient(timeout time.Duration, additionalCA string) (*http.Client, error) {
	client := &http.Client{Timeout: timeout}
	if additionalCA == "" {
		return client, nil
	}
	contents, err := os.ReadFile(additionalCA)
	if err != nil {
		return nil, fmt.Errorf("reading configured CA")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("loading system trust")
	}
	if !roots.AppendCertsFromPEM(contents) {
		return nil, fmt.Errorf("configured CA is invalid")
	}
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	return client, nil
}
