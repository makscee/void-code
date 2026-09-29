package config_test

import (
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

func relayBaseURL(cfg config.Config) string {
	return cfg.RelayScheme + "://" + cfg.RelayHost
}

// The default, with nothing set, must land on relay and NOT on auth. This is the
// bug: the access-request route lives on relay, and a default pointing at the
// sign-in host makes every fresh `vc access-request` 404.
func TestResolveAccessCheckHostDefaultsToRelay(t *testing.T) {
	cfg := config.Resolve(func(string) string { return "" })
	if want := relayBaseURL(cfg); cfg.AccessCheckHost != want {
		t.Errorf("AccessCheckHost = %q, want the resolved relay base URL %q — the access-request route lives on relay, not the sign-in host", cfg.AccessCheckHost, want)
	}
	if cfg.AccessCheckHost == cfg.AuthHost {
		t.Errorf("AccessCheckHost = %q must not equal AuthHost %q — that default is the 404 bug: POST auth/v1/vc/access-requests is not a route", cfg.AccessCheckHost, cfg.AuthHost)
	}
	if cfg.AccessCheckHost == config.DefaultAuthHost {
		t.Errorf("AccessCheckHost = %q, must not be the auth default %q", cfg.AccessCheckHost, config.DefaultAuthHost)
	}
	// Sign-in must stay on auth: device-login and the provider list have no route
	// on relay, so this fix must not drag AuthHost along with it.
	if cfg.AuthHost != config.DefaultAuthHost {
		t.Errorf("AuthHost = %q, want the sign-in default %q left untouched — the check moving to relay must not move login", cfg.AuthHost, config.DefaultAuthHost)
	}
}

// The default follows the RESOLVED relay host, not the compiled constant. An
// operator who points the whole CLI at a stand with VC_RELAY_HOST must not find
// the access check still talking to production relay — the same property the old
// comment demanded of VC_AUTH_HOST, now relative to relay, because relay is what
// the check follows. A scheme prefix on the override travels through too.
func TestResolveAccessCheckHostFollowsAnOverriddenRelayHost(t *testing.T) {
	cases := []struct{ name, relayEnv string }{
		{"host:port stand", "relay.stand.example:9443"},
		{"plaintext stand with scheme", "http://relay.stand.example:8448"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := map[string]string{config.EnvRelayHost: c.relayEnv}
			cfg := config.Resolve(func(name string) string { return env[name] })
			// The check must land wherever relay landed — computed from cfg, so
			// a change to VC_RELAY_HOST drags the check with it and a hard-coded
			// production host in the implementation would be caught here.
			if want := relayBaseURL(cfg); cfg.AccessCheckHost != want {
				t.Errorf("VC_RELAY_HOST=%q: AccessCheckHost = %q, want it to follow relay to %q, not stay on production", c.relayEnv, cfg.AccessCheckHost, want)
			}
			if cfg.AccessCheckHost == config.DefaultRelayScheme+"://"+config.DefaultRelayHost {
				t.Errorf("VC_RELAY_HOST=%q: AccessCheckHost stayed on the compiled relay %q — the check ignored the stand", c.relayEnv, cfg.AccessCheckHost)
			}
		})
	}
}

// Pointing only the sign-in host at a stand must NOT drag the check off relay:
// the check follows relay now, and login is configured independently. AuthHost
// still moves — login stays configurable — but the check does not follow it.
func TestResolveAccessCheckHostDoesNotFollowAuthHost(t *testing.T) {
	env := map[string]string{config.EnvAuthHost: "https://identity.stand.example"}
	cfg := config.Resolve(func(name string) string { return env[name] })
	if cfg.AuthHost != "https://identity.stand.example" {
		t.Errorf("AuthHost = %q, want VC_AUTH_HOST to still move login", cfg.AuthHost)
	}
	if cfg.AccessCheckHost == cfg.AuthHost {
		t.Errorf("AccessCheckHost = %q followed VC_AUTH_HOST — it must follow relay, not the sign-in host", cfg.AccessCheckHost)
	}
	if want := relayBaseURL(cfg); cfg.AccessCheckHost != want {
		t.Errorf("AccessCheckHost = %q, want it left on relay %q when only VC_AUTH_HOST moved", cfg.AccessCheckHost, want)
	}
}

// The explicit override is the highest priority: it wins over the relay default
// AND over a moved VC_RELAY_HOST, and it moves the check and nothing else.
func TestResolveAccessCheckHostExplicitOverrideWins(t *testing.T) {
	env := map[string]string{
		config.EnvAccessCheckHost: "https://check.example",
		config.EnvRelayHost:       "relay.stand.example:9443",
		config.EnvAuthHost:        "https://identity.stand.example",
	}
	cfg := config.Resolve(func(name string) string { return env[name] })
	if cfg.AccessCheckHost != "https://check.example" {
		t.Errorf("AccessCheckHost = %q, want the explicit VC_ACCESS_CHECK_HOST override to win over relay", cfg.AccessCheckHost)
	}
	if cfg.AuthHost != "https://identity.stand.example" {
		t.Errorf("AuthHost = %q, the check's override must not touch sign-in", cfg.AuthHost)
	}
	if cfg.RelayHost != "relay.stand.example:9443" {
		t.Errorf("RelayHost = %q, the check's override must not touch relay", cfg.RelayHost)
	}
}

// A blank value is not a choice anyone made. Every other VC_* override reads ""
// as unset, and reading it as a real host here would hand the check to an empty
// base URL instead of falling back to relay.
func TestResolveAccessCheckHostTreatsBlankAsUnset(t *testing.T) {
	for _, blank := range []string{"", "   ", "\t\n"} {
		env := map[string]string{config.EnvAccessCheckHost: blank}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if want := relayBaseURL(cfg); cfg.AccessCheckHost != want {
			t.Errorf("blank %q: AccessCheckHost = %q, want the relay fallback %q", blank, cfg.AccessCheckHost, want)
		}
	}
}

// The explicit override, when usable, is concatenated with "/v1/vc/..." paths, so
// a trailing slash would produce "//v1/vc/...". The field carries no trailing
// slash; Resolve is the only place every caller passes through.
func TestResolveAccessCheckHostTrimsTrailingSlash(t *testing.T) {
	for _, given := range []string{"https://check.example/", "https://check.example//"} {
		env := map[string]string{config.EnvAccessCheckHost: given}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if cfg.AccessCheckHost != "https://check.example" {
			t.Errorf("given %q: AccessCheckHost = %q, want the slash trimmed — vc would otherwise request //v1/vc/...", given, cfg.AccessCheckHost)
		}
	}
}

// A base URL carrying a path, a query or a fragment cannot be concatenated into
// a working request, and this one receives a bearer token. Falling back to relay
// is the safe reading of a value nobody can honour: refusing to send the
// credential somewhere unusable beats sending it there.
func TestResolveAccessCheckHostIgnoresUnusableValues(t *testing.T) {
	for _, given := range []string{"check.example", "https://check.example?x=1", "https://check.example#f", "://nonsense"} {
		env := map[string]string{config.EnvAccessCheckHost: given}
		cfg := config.Resolve(func(name string) string { return env[name] })
		if want := relayBaseURL(cfg); cfg.AccessCheckHost != want {
			t.Errorf("given %q: AccessCheckHost = %q, want the relay fallback %q — a bearer token must not be sent to a base URL vc cannot build a request from", given, cfg.AccessCheckHost, want)
		}
	}
}
