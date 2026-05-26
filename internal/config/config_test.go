package config_test

import (
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
}

func TestResolveOverrides(t *testing.T) {
	env := map[string]string{
		config.EnvRelayHost: "custom.host:9999",
		config.EnvAuthHost:  "https://my-auth.example.com",
		config.EnvRelayCA:   "/tmp/my-ca.pem",
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
}
