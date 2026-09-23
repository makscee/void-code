package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This exercises the ordinary manual model choice against pinned Pi, the real
// managed extension, and the relay protocol. It intentionally does not depend
// on persisted-default migration.
func TestPiManualGPT6SelectionSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the trusted bootstrap stub is a POSIX shell script; pinned qualification runs on POSIX")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		t.Run(model, func(t *testing.T) {
			var requested string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode relay request: %v", err)
				}
				requested = payload.Model
				if r.URL.Path != "/codex/responses" || r.Header.Get("x-void-provider") != "codex-local" {
					t.Errorf("relay request = %s provider=%q", r.URL.Path, r.Header.Get("x-void-provider"))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, openAIFallbackSSE)
			}))
			defer upstream.Close()

			work := t.TempDir()
			home := filepath.Join(work, "home")
			agentDir := filepath.Join(home, ".pi", "agent")
			if err := os.MkdirAll(agentDir, 0700); err != nil {
				t.Fatal(err)
			}
			extension := filepath.Join(work, "void-code.ts")
			if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{
				"version": 1, "relayUrl": upstream.URL, "authToken": "local-only",
				"providers": []map[string]any{{"kind": "codex", "relayProviderId": "codex-local", "models": []string{"gpt-6-sol", "gpt-6-luna"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			bootstrap := filepath.Join(work, "bootstrap.sh")
			if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' "+shellQuote(string(payload))+"\n"), 0700); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
				"-e", extension, "--offline", "--no-tools", "--no-context-files",
				"--provider", "void-codex", "--model", model, "--no-session", "-p", "PING")
			command.Dir = work
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TERM=dumb", "PI_CODING_AGENT_DIR=" + agentDir, "VC_BOOTSTRAP_EXECUTABLE=" + bootstrap}
			output, runErr := command.CombinedOutput()
			if runErr != nil {
				t.Fatalf("manual %s selection failed: %v\n%s", model, runErr, output)
			}
			if requested != model {
				t.Fatalf("manual selection sent model %q, want %q", requested, model)
			}
			if !strings.Contains(string(output), "OPENAI_FALLBACK_OK") {
				t.Fatalf("manual %s response did not reach Pi:\n%s", model, output)
			}
		})
	}
}
