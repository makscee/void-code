package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

func TestCurrentPiBootstrapUsesProtectedTokenAndCurrentExactGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer protected-token" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
			{"id": "chatgpt-granted", "name": "ChatGPT", "type": "openai-codex-oauth"},
			{"id": "chatgpt-other", "name": "Other", "type": "openai-codex-oauth"},
			{"id": "deepseek-granted", "name": "DeepSeek", "type": "deepseek"},
		}})
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_RELAY_HOST", "https://relay.test:9443")
	if err := auth.Save("protected-token"); err != nil {
		t.Fatal(err)
	}

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.RelayURL != "https://relay.test:9443" || got.AuthToken != "protected-token" {
		t.Fatalf("bootstrap metadata = %#v", got)
	}
	if len(got.Providers) != 3 || got.Providers[0].RelayProviderID != "chatgpt-granted" || got.Providers[1].RelayProviderID != "chatgpt-other" || got.Providers[2].RelayProviderID != "deepseek-granted" {
		t.Fatalf("providers = %#v", got.Providers)
	}
	wantCodex := []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra"}
	if !reflect.DeepEqual(got.Providers[0].Models, wantCodex) {
		t.Errorf("Codex bootstrap models = %q, want %q", got.Providers[0].Models, wantCodex)
	}
	wantDeepSeek := []string{"deepseek/deepseek-v4-pro", "deepseek/deepseek-v4-flash"}
	if !reflect.DeepEqual(got.Providers[2].Models, wantDeepSeek) {
		t.Errorf("DeepSeek bootstrap models = %q, want unchanged %q", got.Providers[2].Models, wantDeepSeek)
	}
	for _, path := range []string{
		filepath.Join(home, ".pi", "agent", "settings.json"),
		filepath.Join(home, ".pi", "agent", "extensions", "void-code.ts"),
	} {
		if data, err := os.ReadFile(path); err == nil && bytes.Contains(data, []byte("protected-token")) {
			t.Fatalf("token copied to %s", path)
		}
	}
}

func TestCurrentPiBootstrapRejectsUnsupportedCurrentGrant(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		grant string
		label string
	}{
		{name: "chatgpt id", id: "chatgpt-incompatible", grant: "Enterprise"},
		{name: "codex grant name", id: "opaque-name", grant: "Codex subscription"},
		{name: "chatgpt saved label", id: "opaque-label", grant: "Enterprise", label: "ChatGPT relay"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
					{"id": tc.id, "name": tc.grant, "type": "anthropic-api-key"},
				}})
			}))
			defer server.Close()
			t.Setenv("VC_AUTH_HOST", server.URL)
			if err := auth.Save("protected-token"); err != nil {
				t.Fatal(err)
			}

			if got, err := currentPiBootstrap(); err == nil {
				t.Fatalf("explicitly incompatible grant yielded bootstrap: %#v", got.Providers)
			}
		})
	}
}

func TestCurrentPiBootstrapRejectsRevokedActiveGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{}})
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	if err := auth.Save("protected-token"); err != nil {
		t.Fatal(err)
	}
	if _, err := currentPiBootstrap(); err == nil {
		t.Fatal("revoked active provider unexpectedly bootstrapped")
	}
}

func TestPiArgsPreserveDesktopSessionLifecycle(t *testing.T) {
	got := buildPiArgs([]string{"--session-id", "session-1"}, "/managed.ts")
	want := []string{"-e", "/managed.ts", "--session-id", "session-1"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
