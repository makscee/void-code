// Package config resolves runtime configuration for vc from env overrides and
// built-in defaults.  All env var names are the canonical VC_* namespace.
package config

import (
	"os"
	"path/filepath"
)

// Environment variable names — canonical, never change.
const (
	EnvRelayHost = "VC_RELAY_HOST" // host:port override for relay
	EnvRelayCA   = "VC_RELAY_CA"   // filesystem path override for relay CA
	EnvAuthHost  = "VC_AUTH_HOST"  // base URL override for void-auth
	EnvCode      = "VC_CODE"       // access code for Flow 1a (login --code)
)

// Defaults — DNS names, not raw IPs (grill decision A8/A10).
const (
	DefaultRelayHost = "relay.makscee.ru:8448"
	DefaultAuthHost  = "https://auth.makscee.ru"
)

// Config is the resolved runtime configuration.
type Config struct {
	RelayHost string // host:port
	AuthHost  string // base URL, no trailing slash
	CAOverride string // empty = use cached/embedded
}

// Resolve builds Config from env, falling back to compiled defaults.
// Pass os.Getenv in production; pass a stub in tests.
func Resolve(getenv func(string) string) Config {
	relayHost := getenv(EnvRelayHost)
	if relayHost == "" {
		relayHost = DefaultRelayHost
	}

	authHost := getenv(EnvAuthHost)
	if authHost == "" {
		authHost = DefaultAuthHost
	}

	return Config{
		RelayHost:  relayHost,
		AuthHost:   authHost,
		CAOverride: getenv(EnvRelayCA),
	}
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
