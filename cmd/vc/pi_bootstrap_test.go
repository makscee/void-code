package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/provider"
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
		}})
	}))
	defer server.Close()
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_RELAY_HOST", "https://relay.test:9443")
	if err := auth.Save("protected-token"); err != nil {
		t.Fatal(err)
	}
	if err := provider.Save(provider.Provider{Kind: provider.RelayProvider, ID: "chatgpt-granted"}); err != nil {
		t.Fatal(err)
	}

	got, err := currentPiBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.RelayURL != "https://relay.test:9443" || got.AuthToken != "protected-token" {
		t.Fatalf("bootstrap metadata = %#v", got)
	}
	if len(got.Providers) != 1 || got.Providers[0].Kind != "codex" || got.Providers[0].RelayProviderID != "chatgpt-granted" {
		t.Fatalf("providers = %#v", got.Providers)
	}
	if len(got.Providers[0].Models) != len(piVoidCodexModels) {
		t.Fatalf("models = %#v", got.Providers[0].Models)
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
	if err := provider.Save(provider.Provider{Kind: provider.RelayProvider, ID: "revoked-chatgpt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := currentPiBootstrap(); err == nil {
		t.Fatal("revoked active provider unexpectedly bootstrapped")
	}
}

func TestManagedLaunchArgsDoNotExplicitlyDoubleLoadProvider(t *testing.T) {
	got := buildPiVoidCodexArgs([]string{"-p", "hello"}, "")
	for _, arg := range got {
		if arg == "-e" {
			t.Fatalf("managed standard extension was also passed explicitly: %#v", got)
		}
	}
}
