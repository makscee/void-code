package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPiVoidDeepSeekExtensionSmoke(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	extPath, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensure extension: %v", err)
	}

	type seenRequest struct {
		Path          string
		Authorization string
		Provider      string
		Body          map[string]any
	}
	seenCh := make(chan seenRequest, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected relay path: %s", r.URL.Path)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("relay body JSON: %v; raw=%s", err, string(bodyBytes))
		}
		seenCh <- seenRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Provider:      r.Header.Get("x-void-provider"),
			Body:          body,
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_smoke\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"deepseek/deepseek-v4-pro\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"deepseek smoke ok\"}}\n\n")
		_, _ = io.WriteString(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}\n\n")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBin,
		"--no-extensions", "-e", extPath,
		"--provider", "void-deepseek",
		"--model", "deepseek/deepseek-v4-pro",
		"--no-context-files",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-tools",
		"--no-session",
		"--no-approve",
		"-p", "Say exactly: smoke",
	)
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"USERPROFILE="+tmp,
		"VC_RELAY_URL="+relay.URL,
		"VC_AUTH_TOKEN=smoke-token",
		"VC_RELAY_PROVIDER_ID=deepseek",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pi deepseek smoke failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if ctx.Err() != nil {
		t.Fatalf("pi deepseek smoke timed out; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "deepseek smoke ok") {
		t.Fatalf("pi stdout missing streamed text; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	select {
	case seen := <-seenCh:
		if seen.Path != "/v1/messages" {
			t.Fatalf("relay path = %q", seen.Path)
		}
		if seen.Authorization != "Bearer smoke-token" {
			t.Fatalf("Authorization = %q", seen.Authorization)
		}
		if seen.Provider != "deepseek" {
			t.Fatalf("x-void-provider = %q", seen.Provider)
		}
		if seen.Body["model"] != "deepseek/deepseek-v4-pro" || seen.Body["stream"] != true {
			t.Fatalf("unexpected anthropic body: %#v", seen.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not receive request")
	}
}

func TestPiVoidCodexExtensionSmoke(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	extPath, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensure extension: %v", err)
	}

	type seenRequest struct {
		Path          string
		Authorization string
		Provider      string
		ChatGPTAcct   string
		Body          map[string]any
	}
	seenCh := make(chan seenRequest, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/codex/responses" {
			t.Fatalf("unexpected relay path: %s", r.URL.Path)
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("relay body JSON: %v; raw=%s", err, string(bodyBytes))
		}
		seenCh <- seenRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Provider:      r.Header.Get("x-void-provider"),
			ChatGPTAcct:   r.Header.Get("chatgpt-account-id"),
			Body:          body,
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"void smoke ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBin,
		"--no-extensions", "-e", extPath,
		"--provider", "void-codex",
		"--model", "gpt-5.5",
		"--no-context-files",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-tools",
		"--no-session",
		"--no-approve",
		"-p", "Say exactly: smoke",
	)
	cmd.Env = append(os.Environ(),
		"HOME="+tmp,
		"USERPROFILE="+tmp,
		"VC_RELAY_URL="+relay.URL,
		"VC_AUTH_TOKEN=smoke-token",
		"VC_RELAY_PROVIDER_ID=prov-smoke",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pi smoke failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if ctx.Err() != nil {
		t.Fatalf("pi smoke timed out; stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "void smoke ok") {
		t.Fatalf("pi stdout missing streamed text; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	select {
	case seen := <-seenCh:
		if seen.Path != "/codex/responses" {
			t.Fatalf("relay path = %q", seen.Path)
		}
		if seen.Authorization != "Bearer smoke-token" {
			t.Fatalf("Authorization = %q", seen.Authorization)
		}
		if seen.Provider != "prov-smoke" {
			t.Fatalf("x-void-provider = %q", seen.Provider)
		}
		if seen.ChatGPTAcct != "" {
			t.Fatalf("chatgpt-account-id header leaked: %q", seen.ChatGPTAcct)
		}
		if seen.Body["model"] != "gpt-5.5" || seen.Body["stream"] != true {
			t.Fatalf("unexpected codex body: %#v", seen.Body)
		}
		if _, ok := seen.Body["chatgpt-account-id"]; ok {
			t.Fatalf("body leaked chatgpt-account-id: %#v", seen.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not receive request")
	}
}
