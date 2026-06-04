// Package relay provides the relay-env helpers: FetchCA and BuildEnv.
// These are two of the five cv-inheritance whitelist items re-implemented fresh
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

// stripKeys lists the env variable names that must be removed from the parent
// environment before relay-specific values are injected.  Any value supplied
// by the parent for these keys would conflict with relay operation.
var stripKeys = map[string]bool{
	"CLAUDE_CODE_OAUTH_TOKEN": true,
	"HTTPS_PROXY":             true,
	"NODE_EXTRA_CA_CERTS":     true,
	"ANTHROPIC_API_KEY":       true,
	"ANTHROPIC_BASE_URL":      true,
}

// BuildEnv constructs a clean env slice suitable for passing to exec.Cmd.Env.
//
// It:
//  1. Copies every entry from parent, skipping the keys in stripKeys.
//  2. Appends the relay-specific variables:
//     - HTTPS_PROXY=<scheme>://<host>
//     - NODE_EXTRA_CA_CERTS=<caPath>
//     - ANTHROPIC_BASE_URL=          (empty — relay intercepts at proxy level)
//     - CLAUDE_CODE_OAUTH_TOKEN=<token>
//     - ANTHROPIC_API_KEY=           (empty — not used in full-feature mode)
//
// scheme should be "http" or "https"; callers pass Config.RelayScheme.
// The Bare mode from cv's BuildEnv is explicitly NOT ported (ADR-0002).
func BuildEnv(parent []string, scheme, host, token, caPath string) []string {
	out := make([]string, 0, len(parent)+5)
	for _, e := range parent {
		k, _, _ := strings.Cut(e, "=")
		if stripKeys[k] {
			continue
		}
		out = append(out, e)
	}

	out = append(out,
		fmt.Sprintf("HTTPS_PROXY=%s://%s", scheme, host),
		"NODE_EXTRA_CA_CERTS="+caPath,
		"ANTHROPIC_BASE_URL=",
		"CLAUDE_CODE_OAUTH_TOKEN="+token,
		"ANTHROPIC_API_KEY=",
	)
	return out
}

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
