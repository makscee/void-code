package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/config"
)

func TestResolveDefaults(t *testing.T) {
	cfg := config.Resolve(func(string) string { return "" })
	if cfg.RelayHost != config.DefaultRelayHost {
		t.Errorf("RelayHost: want %q got %q", config.DefaultRelayHost, cfg.RelayHost)
	}
	if cfg.RelayScheme != config.DefaultRelayScheme {
		t.Errorf("RelayScheme: want %q got %q", config.DefaultRelayScheme, cfg.RelayScheme)
	}
	if cfg.RelayHost != "relay.makscee.ru:443" {
		t.Errorf("RelayHost default value: want relay.makscee.ru:443, got %q", cfg.RelayHost)
	}
	if cfg.RelayScheme != "https" {
		t.Errorf("RelayScheme default value: want https, got %q", cfg.RelayScheme)
	}
	if cfg.AuthHost != config.DefaultAuthHost {
		t.Errorf("AuthHost: want %q got %q", config.DefaultAuthHost, cfg.AuthHost)
	}
	if cfg.CAOverride != "" {
		t.Errorf("CAOverride should be empty by default, got %q", cfg.CAOverride)
	}
	if cfg.Lang != config.DefaultLang {
		t.Errorf("Lang default: want %q got %q", config.DefaultLang, cfg.Lang)
	}
}

func TestResolveRelayHostSchemePrefix(t *testing.T) {
	cases := []struct {
		name        string
		envVal      string
		wantHost    string
		wantScheme  string
	}{
		{
			name:       "http scheme prefix",
			envVal:     "http://relay.makscee.ru:8448",
			wantHost:   "relay.makscee.ru:8448",
			wantScheme: "http",
		},
		{
			name:       "https scheme prefix",
			envVal:     "https://somehost:9999",
			wantHost:   "somehost:9999",
			wantScheme: "https",
		},
		{
			name:       "no scheme prefix defaults to https",
			envVal:     "somehost:9999",
			wantHost:   "somehost:9999",
			wantScheme: "https",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{config.EnvRelayHost: c.envVal}
			cfg := config.Resolve(func(k string) string { return env[k] })
			if cfg.RelayHost != c.wantHost {
				t.Errorf("RelayHost: want %q got %q", c.wantHost, cfg.RelayHost)
			}
			if cfg.RelayScheme != c.wantScheme {
				t.Errorf("RelayScheme: want %q got %q", c.wantScheme, cfg.RelayScheme)
			}
		})
	}
}

func TestResolveOverrides(t *testing.T) {
	env := map[string]string{
		config.EnvRelayHost: "custom.host:9999",
		config.EnvAuthHost:  "https://my-auth.example.com",
		config.EnvRelayCA:   "/tmp/my-ca.pem",
		config.EnvLang:      "ru",
	}
	cfg := config.Resolve(func(k string) string { return env[k] })
	if cfg.RelayHost != "custom.host:9999" {
		t.Errorf("RelayHost override failed: got %q", cfg.RelayHost)
	}
	// No scheme prefix → RelayScheme stays at default "https".
	if cfg.RelayScheme != "https" {
		t.Errorf("RelayScheme without prefix: want https, got %q", cfg.RelayScheme)
	}
	if cfg.AuthHost != "https://my-auth.example.com" {
		t.Errorf("AuthHost override failed: got %q", cfg.AuthHost)
	}
	if cfg.CAOverride != "/tmp/my-ca.pem" {
		t.Errorf("CAOverride failed: got %q", cfg.CAOverride)
	}
	if cfg.Lang != "ru" {
		t.Errorf("Lang override failed: got %q", cfg.Lang)
	}
}

func TestResolveLangNormalisation(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"ru", "ru"},
		{"en", "en"},
		{"fr", "en"}, // unsupported → fallback
		{"RU", "en"}, // case-sensitive: only lowercase "ru" is valid
		{"", "en"},
	}
	for _, c := range cases {
		env := map[string]string{config.EnvLang: c.input}
		cfg := config.Resolve(func(k string) string { return env[k] })
		if cfg.Lang != c.want {
			t.Errorf("Lang(%q): want %q got %q", c.input, c.want, cfg.Lang)
		}
	}
}

func TestReadWriteConfigFile(t *testing.T) {
	// Point HOME to a temp dir so real ~/.void-code/config is never touched.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // Windows compat

	// Write some values.
	if err := config.WriteConfigFile(map[string]string{"lang": "ru", "foo": "bar"}); err != nil {
		t.Fatalf("WriteConfigFile: %v", err)
	}

	// Verify the file landed in the expected place.
	expectedPath := filepath.Join(tmpHome, ".void-code", "config")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("config file not created at %s: %v", expectedPath, err)
	}

	// Read them back.
	kv, err := config.ReadConfigFile()
	if err != nil {
		t.Fatalf("ReadConfigFile: %v", err)
	}
	if kv["lang"] != "ru" {
		t.Errorf("lang: want %q got %q", "ru", kv["lang"])
	}
	if kv["foo"] != "bar" {
		t.Errorf("foo: want %q got %q", "bar", kv["foo"])
	}

	// Update only lang; foo should survive.
	if err := config.WriteConfigFile(map[string]string{"lang": "en"}); err != nil {
		t.Fatalf("WriteConfigFile update: %v", err)
	}
	kv2, err := config.ReadConfigFile()
	if err != nil {
		t.Fatalf("ReadConfigFile after update: %v", err)
	}
	if kv2["lang"] != "en" {
		t.Errorf("lang after update: want %q got %q", "en", kv2["lang"])
	}
	if kv2["foo"] != "bar" {
		t.Errorf("foo after update: want %q got %q", "bar", kv2["foo"])
	}
}

func TestReadConfigFileMissing(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	kv, err := config.ReadConfigFile()
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(kv) != 0 {
		t.Errorf("expected empty map, got %v", kv)
	}
}

// --- UpdatePrefs round-trip ---

func TestUpdatePrefsRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// Defaults: nothing set.
	p := config.ReadUpdatePrefs()
	if p.AutoUpdate {
		t.Error("AutoUpdate should be false by default")
	}
	if p.LastPromptedVersion != "" {
		t.Errorf("LastPromptedVersion should be empty by default, got %q", p.LastPromptedVersion)
	}

	// Write auto_update=true + last_prompted_version.
	if err := config.WriteUpdatePrefs(config.UpdatePrefs{
		AutoUpdate:          true,
		LastPromptedVersion: "v0.1.3",
	}); err != nil {
		t.Fatalf("WriteUpdatePrefs: %v", err)
	}

	p2 := config.ReadUpdatePrefs()
	if !p2.AutoUpdate {
		t.Error("AutoUpdate should be true after write")
	}
	if p2.LastPromptedVersion != "v0.1.3" {
		t.Errorf("LastPromptedVersion: want v0.1.3 got %q", p2.LastPromptedVersion)
	}
}

func TestUpdatePrefsAutoUpdateFalse(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// Write true then false.
	_ = config.WriteUpdatePrefs(config.UpdatePrefs{AutoUpdate: true, LastPromptedVersion: "v0.1.3"})
	_ = config.WriteUpdatePrefs(config.UpdatePrefs{AutoUpdate: false, LastPromptedVersion: "v0.1.3"})

	p := config.ReadUpdatePrefs()
	if p.AutoUpdate {
		t.Error("AutoUpdate should be false after writing false")
	}
	if p.LastPromptedVersion != "v0.1.3" {
		t.Errorf("LastPromptedVersion survived: want v0.1.3 got %q", p.LastPromptedVersion)
	}
}

func TestUpdateCacheFilePath(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	path, err := config.UpdateCacheFilePath()
	if err != nil {
		t.Fatalf("UpdateCacheFilePath: %v", err)
	}
	if filepath.Base(path) != "last-update-check" {
		t.Errorf("unexpected basename: %q", filepath.Base(path))
	}
}

// ─── VCD-62: statusline prior-command store + skip sentinel ──────────────────

func TestStatusLinePriorPath(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	p, err := config.StatusLinePriorPath()
	if err != nil {
		t.Fatalf("StatusLinePriorPath: %v", err)
	}
	if filepath.Base(p) != "statusline-prior.json" {
		t.Errorf("unexpected basename: %q", filepath.Base(p))
	}
	// Must be under ~/.void-code/
	dir := filepath.Dir(p)
	if filepath.Base(dir) != ".void-code" {
		t.Errorf("not under .void-code: %q", dir)
	}
}

func TestStatusLineSkipSentinel(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// Initially not skipped.
	if config.IsStatusLineSkipped() {
		t.Fatal("should not be skipped initially")
	}

	// Mark skipped.
	if err := config.MarkStatusLineSkipped(); err != nil {
		t.Fatalf("MarkStatusLineSkipped: %v", err)
	}
	if !config.IsStatusLineSkipped() {
		t.Fatal("should be skipped after Mark")
	}

	// Clear.
	if err := config.ClearStatusLineSkipped(); err != nil {
		t.Fatalf("ClearStatusLineSkipped: %v", err)
	}
	if config.IsStatusLineSkipped() {
		t.Fatal("should not be skipped after Clear")
	}
}

// The access check ("who am I, and am I let in") and sign-in are the same host
// on paper and two different services in production: the check is honoured by
// Relay and refused by the legacy service behind the sign-in host, while the
// device-authorization routes exist only behind the sign-in host. One switch
// cannot serve both, so the check gets its own — defaulting to the sign-in host
// so that changing nothing changes nothing.
//
// The name states the role, not the route. /v1/vc/me is today's spelling of an
// access check, and ErrAccessNotGranted next door already made the same choice
// for the same reason: neither the protocol code nor the server-side mechanism
// is stable enough to name.

func TestResolveAccessCheckHostDefaultsToAuthHost(t *testing.T) {
	cfg := config.Resolve(func(string) string { return "" })
	if cfg.AccessCheckHost != cfg.AuthHost {
		t.Errorf("AccessCheckHost = %q, want it to fall back to AuthHost %q", cfg.AccessCheckHost, cfg.AuthHost)
	}
	if cfg.AccessCheckHost != config.DefaultAuthHost {
		t.Errorf("AccessCheckHost = %q, want the compiled default %q — an unset override must not change what every hand-run vc does", cfg.AccessCheckHost, config.DefaultAuthHost)
	}
}

// The fallback is to the RESOLVED sign-in host, not to the compiled constant.
// Someone who points the whole CLI at a stand with VC_AUTH_HOST alone must not
// find the access check still talking to production.
func TestResolveAccessCheckHostFollowsAnOverriddenAuthHost(t *testing.T) {
	env := map[string]string{config.EnvAuthHost: "https://identity.stand.example"}
	cfg := config.Resolve(func(name string) string { return env[name] })
	if cfg.AccessCheckHost != "https://identity.stand.example" {
		t.Errorf("AccessCheckHost = %q, want it to follow VC_AUTH_HOST to the stand", cfg.AccessCheckHost)
	}
}

// Setting the check's host moves the check and nothing else. Sign-in, the Pi
// bootstrap's provider list and the relay all read their own values, and none
// of them has a route on the host the check wants.
func TestResolveAccessCheckHostOverrideMovesNothingElse(t *testing.T) {
	env := map[string]string{config.EnvAccessCheckHost: "https://relay.makscee.ru"}
	cfg := config.Resolve(func(name string) string { return env[name] })
	if cfg.AccessCheckHost != "https://relay.makscee.ru" {
		t.Errorf("AccessCheckHost = %q, want the override", cfg.AccessCheckHost)
	}
	if cfg.AuthHost != config.DefaultAuthHost {
		t.Errorf("AuthHost = %q, want %q — sign-in and the provider list have no route on the check's host", cfg.AuthHost, config.DefaultAuthHost)
	}
	if cfg.RelayHost != config.DefaultRelayHost {
		t.Errorf("RelayHost = %q, want %q — model traffic has nothing to do with the access check", cfg.RelayHost, config.DefaultRelayHost)
	}
}

// A blank value is not a choice anyone made. Every other VC_* override reads ""
// as unset, and reading it as a real host here would hand the check to an empty
// base URL instead of falling back.
func TestResolveAccessCheckHostTreatsBlankAsUnset(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t\n"} {
		env := map[string]string{config.EnvAccessCheckHost: blank}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if cfg.AccessCheckHost != cfg.AuthHost {
			t.Errorf("blank %q: AccessCheckHost = %q, want the AuthHost fallback %q", blank, cfg.AccessCheckHost, cfg.AuthHost)
		}
	}
}

// FetchMe builds its URL by concatenation — host + "/v1/vc/me" — so a trailing
// slash produces "//v1/vc/me". The field documents itself as carrying no
// trailing slash; that has to be enforced somewhere, and Resolve is the only
// place every caller passes through.
func TestResolveAccessCheckHostTrimsTrailingSlash(t *testing.T) {
	for _, given := range []string{"https://relay.makscee.ru/", "https://relay.makscee.ru//"} {
		env := map[string]string{config.EnvAccessCheckHost: given}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if cfg.AccessCheckHost != "https://relay.makscee.ru" {
			t.Errorf("given %q: AccessCheckHost = %q, want the slash trimmed — vc would otherwise request //v1/vc/me", given, cfg.AccessCheckHost)
		}
	}
}

// A base URL carrying a path, a query or a fragment cannot be concatenated into
// a working request, and this one receives a bearer token. Falling back is the
// safe reading of a value nobody can honour: refusing to send the credential
// somewhere unusable beats sending it there.
func TestResolveAccessCheckHostIgnoresUnusableValues(t *testing.T) {
	for _, given := range []string{"relay.makscee.ru", "https://relay.makscee.ru?x=1", "https://relay.makscee.ru#f", "://nonsense"} {
		env := map[string]string{config.EnvAccessCheckHost: given}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if cfg.AccessCheckHost != cfg.AuthHost {
			t.Errorf("given %q: AccessCheckHost = %q, want the AuthHost fallback — a bearer token must not be sent to a base URL vc cannot build a request from", given, cfg.AccessCheckHost)
		}
	}
}
