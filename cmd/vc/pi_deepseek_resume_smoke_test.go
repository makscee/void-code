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
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type resumeUpstreamCall struct {
	provider string
	path     string
	model    string
}

// A source-only checkout must reject the retired managed provider before the pinned runtime smoke can skip.
func TestPiLegacyDeepSeekResumeSourceDoesNotRegisterManagedDeepSeek(t *testing.T) {
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(piVoidCodexExtensionSource, "")
	code = regexp.MustCompile(`(?m)^[\t ]*//[^\n]*(?:\n|$)`).ReplaceAllString(code, "")

	retiredBindings := regexp.MustCompile(`(?m)^[\t ]*(?:const|let|var)[\t ]+([A-Za-z_$][A-Za-z0-9_$]*)[\t ]*=[\t ]*(?:"void-deepseek"|'void-deepseek')[\t ]*;`).FindAllStringSubmatch(code, -1)
	for _, binding := range retiredBindings {
		registration := regexp.MustCompile(`(?m)^[\t ]*pi[\t ]*\.[\t ]*registerProvider[\t ]*\([\t ]*` + regexp.QuoteMeta(binding[1]) + `[\t ]*,`)
		if registration.MatchString(code) {
			t.Fatalf("embedded Pi extension still registers retired managed DeepSeek through %s", binding[1])
		}
	}
	literalRegistration := regexp.MustCompile(`(?m)^[\t ]*pi[\t ]*\.[\t ]*registerProvider[\t ]*\([\t ]*(?:"void-deepseek"|'void-deepseek')[\t ]*,`)
	if literalRegistration.MatchString(code) {
		t.Fatal("embedded Pi extension still registers retired managed DeepSeek through a literal provider id")
	}
}

// The next turn in a legacy DeepSeek session must reach the OpenAI transport, never DeepSeek.
func TestPiLegacyDeepSeekResumeFallsBackToOpenAIDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the trusted bootstrap stub is a POSIX shell script; use the neighboring Windows RPC probe on win32")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	var (
		callsMu sync.Mutex
		calls   []resumeUpstreamCall
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		call := resumeUpstreamCall{provider: r.Header.Get("x-void-provider"), path: r.URL.Path, model: payload.Model}
		callsMu.Lock()
		calls = append(calls, call)
		callsMu.Unlock()
		if call.provider != "codex-local" {
			http.Error(w, "retired provider reached", http.StatusGone)
			return
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
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-terra"}`), 0600); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}

	bootstrapPayload, err := json.Marshal(map[string]any{
		"version":   1,
		"relayUrl":  upstream.URL,
		"authToken": "local-only",
		"providers": []map[string]any{
			{"kind": "codex", "relayProviderId": "codex-local", "models": []string{"gpt-5.6-terra"}},
			{"kind": "deepseek", "relayProviderId": "deepseek-local", "models": []string{"deepseek/deepseek-v4-pro", "deepseek/deepseek-v4-flash"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(work, "bootstrap.sh")
	bootstrapScript := "#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' " + shellQuote(string(bootstrapPayload)) + "\n"
	if err := os.WriteFile(bootstrap, []byte(bootstrapScript), 0700); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(work, "deepseek-session.jsonl")
	entries := []any{
		map[string]any{"type": "session", "version": 3, "id": "22222222-2222-4222-8222-222222222222", "timestamp": "2026-09-10T00:00:00.000Z", "cwd": work},
		map[string]any{"type": "message", "id": "message-1", "parentId": nil, "timestamp": "2026-09-10T00:00:01.000Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "persisted message"}}, "timestamp": int64(1788998401000)}},
		map[string]any{"type": "model_change", "id": "model-1", "parentId": "message-1", "timestamp": "2026-09-10T00:00:02.000Z", "provider": "void-deepseek", "modelId": "deepseek/deepseek-v4-pro"},
	}
	var persisted strings.Builder
	for _, entry := range entries {
		encoded, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		persisted.Write(encoded)
		persisted.WriteByte('\n')
	}
	if err := os.WriteFile(session, []byte(persisted.String()), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--no-tools", "--no-context-files", "--session", session, "-p", "PING")
	command.Dir = work
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TERM=dumb", "PI_CODING_AGENT_DIR=" + agentDir, "VC_BOOTSTRAP_EXECUTABLE=" + bootstrap}
	output, runErr := command.CombinedOutput()

	callsMu.Lock()
	observed := append([]resumeUpstreamCall(nil), calls...)
	callsMu.Unlock()
	for _, call := range observed {
		if call.provider == "deepseek-local" || call.path == "/v1/messages" {
			t.Fatalf("legacy DeepSeek resume dispatched to the retired transport: %#v\nPi output:\n%s", observed, output)
		}
	}
	if runErr != nil {
		t.Fatalf("legacy session did not complete on the OpenAI fallback: %v\nupstream calls: %#v\nPi output:\n%s", runErr, observed, output)
	}
	if len(observed) != 1 || observed[0].provider != "codex-local" || observed[0].path != "/codex/responses" || observed[0].model != "gpt-5.6-terra" {
		t.Fatalf("fallback upstream calls = %#v, want one gpt-5.6-terra call through codex-local /codex/responses", observed)
	}
	if !strings.Contains(string(output), "OPENAI_FALLBACK_OK") {
		t.Fatalf("OpenAI fallback response did not reach the resumed session:\n%s", output)
	}
}

const openAIFallbackSSE = `data: {"type":"response.created","response":{"id":"resp_fallback"}}

data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_fallback","role":"assistant","status":"in_progress","content":[]}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"OPENAI_FALLBACK_OK"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_fallback","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OPENAI_FALLBACK_OK","annotations":[]}]}}

data: {"type":"response.completed","response":{"id":"resp_fallback","status":"completed","output":[{"type":"message","id":"msg_fallback","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OPENAI_FALLBACK_OK","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}

data: [DONE]

`
