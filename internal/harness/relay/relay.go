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
	"ANTHROPIC_AUTH_TOKEN":    true,
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
//     - HTTPS_PROXY=<scheme>://<host>     (route all HTTPS via the relay's MITM proxy)
//     - NODE_EXTRA_CA_CERTS=<caPath>      (trust the relay CA so the MITM TLS validates)
//     - ANTHROPIC_BASE_URL=               (empty — CC hits api.anthropic.com, the proxy intercepts)
//     - ANTHROPIC_AUTH_TOKEN=<token>      (CC sends it as Authorization: Bearer)
//     - ANTHROPIC_API_KEY=                (empty — OAuth-mode CC, no API key)
//
// Routing is via HTTPS_PROXY + relay CA rather than ANTHROPIC_BASE_URL: the proxy
// is an OS-standard mechanism that forwards traffic regardless of Claude Code
// internals, whereas ANTHROPIC_BASE_URL depends on CC behaviour that can change
// under us (VCD-061). The relay still serves the CONNECT/MITM ingress; this is a
// client-side transport choice only. ANTHROPIC_BASE_URL is emitted empty so CC
// resolves api.anthropic.com and the proxy intercepts at the TLS layer.
//
// The bearer is injected via ANTHROPIC_AUTH_TOKEN, not CLAUDE_CODE_OAUTH_TOKEN,
// because interactive CC lets the machine's stored OAuth account override
// CLAUDE_CODE_OAUTH_TOKEN — the relay then read a foreign account token from the
// Authorization header to api.anthropic.com and returned 401 (VCD-060).
// ANTHROPIC_AUTH_TOKEN is CC's gateway-bearer var and is not overridden;
// CLAUDE_CODE_OAUTH_TOKEN stays in stripKeys so a stale parent value can't leak
// and re-trigger the override.
//
// scheme should be "http" or "https"; callers pass Config.RelayScheme.
// caPath is the relay CA path resolved by the caller (resolveCA in cmd/vc).
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
		"ANTHROPIC_AUTH_TOKEN="+token,
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
