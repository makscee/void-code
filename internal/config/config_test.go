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
