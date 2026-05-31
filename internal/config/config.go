// Package config resolves runtime configuration for vc from env overrides and
// built-in defaults.  All env var names are the canonical VC_* namespace.
package config

import (
	"bufio"
	"fmt"
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
)

// Defaults — DNS names, not raw IPs (grill decision A8/A10).
const (
	DefaultRelayHost = "relay.makscee.ru:8448"
	DefaultAuthHost  = "https://auth.makscee.ru"
	DefaultLang      = "en"
)

// Config is the resolved runtime configuration.
type Config struct {
	RelayHost  string // host:port
	AuthHost   string // base URL, no trailing slash
	CAOverride string // empty = use cached/embedded
	Lang       string // "en" or "ru"
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

	lang := getenv(EnvLang)
	if lang == "" {
		lang = DefaultLang
	}
	// Normalise: only "en" and "ru" are supported; fall back to "en".
	if lang != "ru" {
		lang = "en"
	}

	return Config{
		RelayHost:  relayHost,
		AuthHost:   authHost,
		CAOverride: getenv(EnvRelayCA),
		Lang:       lang,
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

// ConfigFilePath returns the path to the vc config file (~/.void-code/config).
func ConfigFilePath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config"), nil
}

// ReadConfigFile reads key=value pairs from the vc config file.
// Returns an empty map if the file does not exist.
func ReadConfigFile() (map[string]string, error) {
	path, err := ConfigFilePath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		result[line[:idx]] = line[idx+1:]
	}
	return result, scanner.Err()
}

// WriteConfigFile writes key=value pairs to the vc config file, preserving
// existing keys that are not in the provided map.
func WriteConfigFile(updates map[string]string) error {
	path, err := ConfigFilePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	existing, err := ReadConfigFile()
	if err != nil {
		return err
	}
	for k, v := range updates {
		existing[k] = v
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("opening config file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for k, v := range existing {
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return w.Flush()
}

// PersistLang writes the lang key to the vc config file (best-effort).
// Errors are silently discarded — language is cosmetic, not auth-critical.
func PersistLang(lang string) {
	_ = WriteConfigFile(map[string]string{"lang": lang})
}

// UpdatePrefs holds auto-update preferences read from the config file.
type UpdatePrefs struct {
	// AutoUpdate is true when the user pressed 'a' (always-update).
	AutoUpdate bool
	// LastPromptedVersion is the latest version the user was prompted for.
	// Used to suppress repeated prompts for the same version after 'n'.
	LastPromptedVersion string
}

// ReadUpdatePrefs reads update preferences from the config file.
func ReadUpdatePrefs() UpdatePrefs {
	kv, err := ReadConfigFile()
	if err != nil {
		return UpdatePrefs{}
	}
	prefs := UpdatePrefs{}
	if kv["auto_update"] == "true" {
		prefs.AutoUpdate = true
	}
	prefs.LastPromptedVersion = kv["last_prompted_version"]
	return prefs
}

// WriteUpdatePrefs persists update preferences to the config file.
func WriteUpdatePrefs(prefs UpdatePrefs) error {
	autoVal := "false"
	if prefs.AutoUpdate {
		autoVal = "true"
	}
	return WriteConfigFile(map[string]string{
		"auto_update":           autoVal,
		"last_prompted_version": prefs.LastPromptedVersion,
	})
}

// UpdateCacheFilePath returns the path of the update-check mtime sentinel.
func UpdateCacheFilePath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "last-update-check"), nil
}

// CCUpdateCacheFilePath returns the sentinel path for the claude-code
// auto-update TTL check (~/.void-code/last-cc-update-check).
func CCUpdateCacheFilePath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "last-cc-update-check"), nil
}

// ─── VCD-62: statusline prior-command store + skip sentinel ──────────────────

// StatusLinePriorPath returns the path to the statusline prior-command file
// (~/.void-code/statusline-prior.json). The file is NOT created here.
func StatusLinePriorPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "statusline-prior.json"), nil
}

const statuslineSkippedKey = "statusline_skipped"

// MarkStatusLineSkipped records that the user chose to skip vc statusline install.
// Doctor will show this as informational (not a failure) and re-offer.
func MarkStatusLineSkipped() error {
	return WriteConfigFile(map[string]string{statuslineSkippedKey: "true"})
}

// IsStatusLineSkipped reports whether the user has previously skipped statusline install.
func IsStatusLineSkipped() bool {
	kv, err := ReadConfigFile()
	if err != nil {
		return false
	}
	return kv[statuslineSkippedKey] == "true"
}

// ClearStatusLineSkipped removes the skip sentinel (called after successful install).
func ClearStatusLineSkipped() error {
	return WriteConfigFile(map[string]string{statuslineSkippedKey: ""})
}

// OSResolveWithFile resolves Config from env but also reads the config file for
// keys absent from env (e.g. lang set by install.sh).
func OSResolveWithFile() Config {
	cfg := OSResolve()
	// If VC_LANG was not set in env, try the config file.
	if os.Getenv(EnvLang) == "" {
		if kv, err := ReadConfigFile(); err == nil {
			if v, ok := kv["lang"]; ok && v != "" {
				if v == "ru" {
					cfg.Lang = "ru"
				}
			}
		}
	}
	return cfg
}
