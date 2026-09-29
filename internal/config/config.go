// Package config resolves runtime configuration for vc from env overrides and
// built-in defaults.  All env var names are the canonical VC_* namespace.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Environment variable names — canonical, never change.
const (
	EnvRelayHost = "VC_RELAY_HOST" // host:port override for relay
	EnvRelayCA   = "VC_RELAY_CA"   // filesystem path override for relay CA
	EnvAuthHost  = "VC_AUTH_HOST"  // base URL override for void-auth
	EnvCode      = "VC_CODE"       // access code for Flow 1a (login --code)
	EnvLang      = "VC_LANG"       // UI language: "en" (default) or "ru"

	// EnvAccessCheckHost overrides the base URL of the service that answers
	// the access check — "who am I, and am I let in". Unset means the relay
	// base URL, where the access-request route actually lives; the check
	// follows relay, not the sign-in host.
	EnvAccessCheckHost = "VC_ACCESS_CHECK_HOST"
)

// Defaults — DNS names, not raw IPs (grill decision A8/A10).
const (
	DefaultRelayHost   = "relay.makscee.ru:443"
	DefaultRelayScheme = "https"
	DefaultAuthHost    = "https://auth.makscee.ru"
	DefaultLang        = "en"
)

// Config is the resolved runtime configuration.
type Config struct {
	RelayHost   string // host:port
	RelayScheme string // "http" or "https"
	AuthHost    string // base URL, no trailing slash
	CAOverride  string // empty = use cached/embedded
	Lang        string // "en" or "ru"

	// AccessCheckHost is the base URL asked "who am I, and am I let in"
	// (today: GET /v1/vc/me, and the access-request queue served next to it).
	// It defaults to the RELAY base URL, not to AuthHost, because in production
	// the two are different services: the check and the access-request queue
	// are honoured by Relay and 404'd behind the sign-in host, while the
	// device-authorization routes and the provider list exist only behind the
	// sign-in host. The name states the role, not the route — the same choice
	// ErrAccessNotGranted made next door, for the same reason.
	// Base URL, no trailing slash.
	AccessCheckHost string
}

// Resolve builds Config from env, falling back to compiled defaults.
// Pass os.Getenv in production; pass a stub in tests.
func Resolve(getenv func(string) string) Config {
	relayHost := getenv(EnvRelayHost)
	relayScheme := DefaultRelayScheme
	if relayHost == "" {
		relayHost = DefaultRelayHost
	} else {
		// If VC_RELAY_HOST carries a scheme prefix, strip it and record the scheme.
		switch {
		case strings.HasPrefix(relayHost, "https://"):
			relayScheme = "https"
			relayHost = strings.TrimPrefix(relayHost, "https://")
		case strings.HasPrefix(relayHost, "http://"):
			relayScheme = "http"
			relayHost = strings.TrimPrefix(relayHost, "http://")
		}
	}

	authHost := getenv(EnvAuthHost)
	if authHost == "" {
		authHost = DefaultAuthHost
	}

	// The check follows the RESOLVED relay host, not a compiled constant:
	// the access-request route lives on relay, so the default is relay's base
	// URL (RelayScheme://RelayHost, exactly as cmd/vc/pi_bootstrap.go forms
	// RelayURL), and pointing the whole CLI at a stand with VC_RELAY_HOST alone
	// drags the check along with it. Sign-in is left on auth: device-login and
	// the provider list have no route on relay.
	accessCheckHost := fmt.Sprintf("%s://%s", relayScheme, relayHost)
	if override, ok := usableBaseURL(getenv(EnvAccessCheckHost)); ok {
		accessCheckHost = override
	}

	lang := getenv(EnvLang)
	if lang == "" {
		lang = DefaultLang
	}
	// Normalise: only "en" and "ru" are supported; fall back to "en".
	if lang != "ru" {
		lang = "en"
	}

	return Config{
		RelayHost:   relayHost,
		RelayScheme: relayScheme,
		AuthHost:    authHost,
		CAOverride:  getenv(EnvRelayCA),
		Lang:        lang,

		AccessCheckHost: accessCheckHost,
	}
}

// usableBaseURL reports whether value is a base URL a caller can concatenate a
// path onto, returning it without its trailing slash.
//
// Blank is not a choice anyone made — every other VC_* override reads "" as
// unset, and so does this one. Everything else is refused rather than repaired:
// the request built from this host carries a bearer token, and falling back to
// a host we know works beats sending the credential at a URL vc cannot build a
// request from. Callers concatenate ("host" + "/v1/vc/me"), so a trailing slash
// would produce "//v1/vc/me", and a path, a query or a fragment cannot survive
// the concatenation at all.
func usableBaseURL(value string) (string, bool) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	return trimmed, true
}

// OSResolve calls Resolve with os.Getenv.
func OSResolve() Config { return Resolve(os.Getenv) }

// CacheDir returns the absolute path to the vc cache directory (~/.void-code/).
// The directory is NOT created here — callers that need it must os.MkdirAll.
func CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".void-code"), nil
}

// UpdateCacheFilePath returns the path of the update-check mtime sentinel.
func UpdateCacheFilePath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "last-update-check"), nil
}
