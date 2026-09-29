// Package relay provides FetchCA, the relay CA download and cache.
// It is one of the five cv-inheritance whitelist items re-implemented fresh
// for void-code (ADR-0002).  No code is imported from the claudev repo.
package relay

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FetchCA retrieves the relay CA certificate from <authBase>/vc/relay-ca.pem
// and caches it to <cacheDir>/relay-ca.pem.  If the cached file already
// exists, it is returned immediately without a network call.
//
// The caller supplies an http.Client so tests can inject a TLS-configured
// client pointing at a test server.  Production callers pass http.DefaultClient
// (or a client with system roots for HTTPS against auth.makscee.ru).
//
// Returns the absolute path of the cached file on success.
func FetchCA(client *http.Client, authBase, cacheDir string) (string, error) {
	cachedPath := filepath.Join(cacheDir, "relay-ca.pem")

	// Return cached file if it already exists.
	if _, err := os.Stat(cachedPath); err == nil {
		return cachedPath, nil
	}

	url := strings.TrimRight(authBase, "/") + "/vc/relay-ca.pem"
	resp, err := client.Get(url) //nolint:noctx // single-use helper; ctx added in VCD-6 if needed
	if err != nil {
		return "", fmt.Errorf("relay: fetch CA: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relay: fetch CA: server returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("relay: fetch CA: read body: %w", err)
	}

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", fmt.Errorf("relay: fetch CA: mkdir cache dir: %w", err)
	}

	if err := os.WriteFile(cachedPath, data, 0600); err != nil {
		return "", fmt.Errorf("relay: fetch CA: write cache: %w", err)
	}

	return cachedPath, nil
}
