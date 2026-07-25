package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	cmd.Env = append(withoutVCEnv(os.Environ()),
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

func TestPiVoidCodexExtensionShrinksLargeImagePayload(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	imgPath := filepath.Join(t.TempDir(), "large.png")
	writeNoisyPNG(t, imgPath, 2600, 1800)

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	extPath, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensure extension: %v", err)
	}

	seenCh := make(chan map[string]any, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(bodyBytes, &body); err != nil {
			t.Fatalf("relay body JSON: %v; raw prefix=%s", err, string(bodyBytes[:min(len(bodyBytes), 200)]))
		}
		seenCh <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_image\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"image smoke ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"msg_image\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"image smoke ok\"}]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBin,
		"--no-extensions", "-e", extPath,
		"--provider", "void-codex",
		"--model", "gpt-5.6-sol",
		"--no-context-files",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-tools",
		"--no-session",
		"--no-approve",
		"-p", "@"+imgPath, "Reply exactly: image smoke",
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
		t.Fatalf("pi image smoke failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	select {
	case body := <-seenCh:
		var urls []string
		collectImageURLs(body, &urls)
		if len(urls) == 0 {
			t.Fatalf("request body had no image_url: %#v", body)
		}
		var deliveredWidth, deliveredHeight int
		var deliveredFormat string
		for _, url := range urls {
			prefix := "base64,"
			idx := strings.Index(url, prefix)
			if idx < 0 {
				t.Fatalf("image_url is not a base64 data URL: %.80s", url)
			}
			got := len(url[idx+len(prefix):])
			if got > int(1.5*1024*1024) {
				t.Fatalf("image base64 size = %d, want <= 1.5MiB", got)
			}
			decoded, err := base64.StdEncoding.DecodeString(url[idx+len(prefix):])
			if err != nil {
				t.Fatalf("decode image: %v", err)
			}
			cfg, format, err := image.DecodeConfig(bytes.NewReader(decoded))
			if err != nil {
				t.Fatalf("decode image config: %v", err)
			}
			deliveredWidth, deliveredHeight, deliveredFormat = cfg.Width, cfg.Height, "image/"+format
		}
		var texts []string
		collectInputTexts(body, &texts)
		joinedText := strings.Join(texts, "\n")
		wantNote := fmt.Sprintf("delivered %dx%d %s", deliveredWidth, deliveredHeight, deliveredFormat)
		if !strings.Contains(joinedText, wantNote) {
			t.Fatalf("image note missing delivered dimensions/format %q in %q", wantNote, joinedText)
		}
		if strings.Contains(joinedText, "displayed at 2000x1385") {
			t.Fatalf("image note kept stale Pi dimensions after second resize: %q", joinedText)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not receive image request")
	}
}

func writeNoisyPNG(t *testing.T, path string, width int, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+0] = byte(rng.Intn(256))
		img.Pix[i+1] = byte(rng.Intn(256))
		img.Pix[i+2] = byte(rng.Intn(256))
		img.Pix[i+3] = 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func collectImageURLs(value any, out *[]string) {
	switch v := value.(type) {
	case map[string]any:
		if v["type"] == "input_image" {
			if url, ok := v["image_url"].(string); ok {
				*out = append(*out, url)
			}
		}
		for _, child := range v {
			collectImageURLs(child, out)
		}
	case []any:
		for _, child := range v {
			collectImageURLs(child, out)
		}
	}
}

func collectInputTexts(value any, out *[]string) {
	switch v := value.(type) {
	case map[string]any:
		if v["type"] == "input_text" {
			if text, ok := v["text"].(string); ok {
				*out = append(*out, text)
			}
		}
		for _, child := range v {
			collectInputTexts(child, out)
		}
	case []any:
		for _, child := range v {
			collectInputTexts(child, out)
		}
	}
}

func TestPiVoidCodexNestedSSEErrorPreservesCodeAndMessage(t *testing.T) {
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

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"Private backend context window exceeded\"}}\n\n")
	}))
	defer relay.Close()

	stdout, stderr, runErr := runPiCodexFixture(t, piBin, extPath, tmp, relay.URL, "", "nested-error")
	if runErr != nil {
		t.Fatalf("pi nested error fixture failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout, stderr)
	}
	output := stdout + "\n" + stderr
	for _, want := range []string{"context_length_exceeded", "Private backend context window exceeded"} {
		if !strings.Contains(output, want) {
			t.Fatalf("Pi output missing nested SSE error field %q; output=%q", want, output)
		}
	}
	if strings.Contains(output, "undefined: undefined") {
		t.Fatalf("Pi discarded nested SSE error fields: %q", output)
	}
}

func TestPiVoidCodexExistingOversizedSessionCompactsBeforeContinuation(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	settingsDir := filepath.Join(tmp, ".pi", "agent")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"compaction":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	extPath, err := ensurePiVoidCodexExtension()
	if err != nil {
		t.Fatalf("ensure extension: %v", err)
	}

	var mu sync.Mutex
	requestKinds := make([]string, 0, 5)
	seedRequests := 0
	summarySeen := false
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read relay body: %v", readErr)
			return
		}
		isSummary := bytes.Contains(body, []byte("conversation to summarize"))
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if isSummary {
			requestKinds = append(requestKinds, "summary")
			summarySeen = true
			writeCodexSuccessSSE(w, "summary", "VC4 durable summary", 1000)
			return
		}
		if seedRequests < 3 {
			seedRequests++
			requestKinds = append(requestKinds, fmt.Sprintf("seed-%d", seedRequests))
			usage := 100
			if seedRequests == 3 {
				usage = 260000
			}
			writeCodexSuccessSSE(w, fmt.Sprintf("seed-%d", seedRequests), "seed ok", usage)
			return
		}
		requestKinds = append(requestKinds, "continuation")
		if !summarySeen {
			_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"oversized continuation reached backend before compaction\"}}\n\n")
			return
		}
		writeCodexSuccessSSE(w, "continuation", "VC4_CONTINUATION_OK", 1200)
	}))
	defer relay.Close()

	sessionPath := filepath.Join(tmp, "oversized-session.jsonl")
	seedPrompts := []string{"seed one", strings.Repeat("oversized-history ", 5000), "seed three"}
	if len(seedPrompts[1]) <= 80000 {
		t.Fatalf("large seed prompt is only %d chars", len(seedPrompts[1]))
	}
	for i, prompt := range seedPrompts {
		stdout, stderr, runErr := runPiCodexFixture(t, piBin, extPath, tmp, relay.URL, sessionPath, prompt)
		if runErr != nil {
			t.Fatalf("seed turn %d failed: %v\nstdout:\n%s\nstderr:\n%s", i+1, runErr, stdout, stderr)
		}
	}

	if err := os.WriteFile(settingsPath, []byte(`{"compaction":{"enabled":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, runErr := runPiCodexFixture(t, piBin, extPath, tmp, relay.URL, sessionPath, "continue after oversized history")
	if runErr != nil {
		t.Fatalf("continuation failed: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout, stderr)
	}
	if !strings.Contains(stdout, "VC4_CONTINUATION_OK") {
		t.Fatalf("continuation did not return marker; stdout=%q stderr=%q", stdout, stderr)
	}

	mu.Lock()
	gotKinds := append([]string(nil), requestKinds...)
	mu.Unlock()
	wantKinds := []string{"seed-1", "seed-2", "seed-3", "summary", "continuation"}
	if strings.Join(gotKinds, ",") != strings.Join(wantKinds, ",") {
		t.Fatalf("relay request order = %v, want %v", gotKinds, wantKinds)
	}
	sessionData, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read durable session: %v", err)
	}
	if !bytes.Contains(sessionData, []byte(`"type":"compaction"`)) || !bytes.Contains(sessionData, []byte("VC4 durable summary")) {
		t.Fatalf("session lacks durable compaction entry; tail=%q", tailString(string(sessionData), 2000))
	}
}

func runPiCodexFixture(t *testing.T, piBin, extPath, home, relayURL, sessionPath, prompt string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"--no-extensions", "-e", extPath,
		"--provider", "void-codex",
		"--model", "gpt-5.6-sol",
		"--no-context-files",
		"--no-skills",
		"--no-prompt-templates",
		"--no-themes",
		"--no-tools",
		"--no-approve",
		"--mode", "json",
	}
	if sessionPath == "" {
		args = append(args, "--no-session")
	} else {
		args = append(args, "--session", sessionPath)
	}
	args = append(args, "-p", prompt)
	cmd := exec.CommandContext(ctx, piBin, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"VC_RELAY_URL="+relayURL,
		"VC_AUTH_TOKEN=fixture-token",
		"VC_RELAY_PROVIDER_ID=fixture-provider",
		"HERDR_PI_AUTO_NAME=0",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("pi fixture timed out: %w", ctx.Err())
	}
	return stdout.String(), stderr.String(), runErr
}

func writeCodexSuccessSSE(w io.Writer, id, text string, inputTokens int) {
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n", id)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":%q}\n\n", text)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n\n", id, text)
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":%d,\"output_tokens\":3}}}\n\n", inputTokens)
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

func tailString(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}

func TestPiVoidCodexExtensionSmoke(t *testing.T) {
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}

	hostHome := os.Getenv("HOME")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(tmp, ".pi", "agent"))
	t.Setenv("VC_PI_MANAGED_PROVIDER", "1")
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "1")

	type seenRequest struct {
		Path          string
		Authorization string
		Provider      string
		ChatGPTAcct   string
		Body          map[string]any
	}
	seenCh := make(chan seenRequest, 2)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vc/relay-ca.pem":
			w.Header().Set("Content-Type", "application/x-pem-file")
			_, _ = w.Write(relayCA)
			return
		case "/v1/vc/me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"userId":"smoke-user","email":"smoke@example.com"}`)
			return
		case "/v1/vc/providers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"providers":[{"id":"prov-smoke","name":"ChatGPT","type":"openai-codex-oauth"}]}`)
			return
		case "/codex/responses":
		default:
			t.Errorf("unexpected relay path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
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
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_smoke\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"void smoke ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"msg_smoke\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"void smoke ok\"}]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":3,\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	vcBin := filepath.Join(tmp, "vc")
	build := exec.CommandContext(ctx, "go", "build", "-o", vcBin, ".")
	build.Dir = "."
	build.Env = append(os.Environ(), "HOME="+hostHome)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fresh vc: %v\n%s", err, out)
	}
	vcDir := filepath.Join(tmp, ".void-code")
	if err := os.MkdirAll(vcDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vcDir, "token"), []byte("smoke-token"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vcDir, "config"), []byte("active_harness=pi\nactive_provider=prov:prov-smoke\nactive_provider_label=ChatGPT relay\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, vcBin, "--",
		"--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes",
		"--tools", "web_search,fetch_content,get_search_content", "--no-session", "--no-approve", "-p", "Say exactly: smoke",
	)
	cmd.Env = append(withoutVCEnv(os.Environ()),
		"HOME="+tmp,
		"USERPROFILE="+tmp,
		"PI_CODING_AGENT_DIR="+filepath.Join(tmp, ".pi", "agent"),
		"VC_AUTH_HOST="+relay.URL,
		"VC_RELAY_HOST="+relay.URL,
		"VC_PI_MANAGED_PROVIDER=1",
		"VC_PI_MANAGED_WEB_SEARCH=1",
		"VC_DISABLE_UPDATE_CHECK=1",
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
		if seen.Body["stream"] != true {
			t.Fatalf("unexpected codex body: %#v", seen.Body)
		}
		instructions, ok := seen.Body["instructions"].(string)
		if !ok || !strings.Contains(instructions, "expert coding assistant operating inside pi") || !strings.Contains(instructions, "multiple queries") || !strings.Contains(instructions, "primary sources") || !strings.Contains(instructions, "cite links") {
			t.Fatalf("instructions do not include Pi and managed web-search guidance: %#v", seen.Body["instructions"])
		}
		if !hasExactManagedWebTools(seen.Body["tools"]) {
			t.Fatalf("managed web tools missing from request: %#v", seen.Body["tools"])
		}
		if _, ok := seen.Body["reasoning"]; !ok {
			t.Fatalf("codex body missing native reasoning block: %#v", seen.Body)
		}
		if _, ok := seen.Body["chatgpt-account-id"]; ok {
			t.Fatalf("body leaked chatgpt-account-id: %#v", seen.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not receive request")
	}

	// A later literal Pi process loads the same user package and provider.
	if runtime.GOOS != "windows" {
		binDir := filepath.Join(tmp, "bin")
		if err := os.MkdirAll(binDir, 0700); err != nil {
			t.Fatal(err)
		}
		fakeVC := filepath.Join(binDir, "vc")
		fixture := "#!/bin/sh\nprintf '%s\\n' '" + fmt.Sprintf(`{"version":1,"relayUrl":%q,"authToken":"smoke-token","providers":[{"kind":"codex","relayProviderId":"prov-smoke","models":["gpt-5.6-sol"]}]}`, relay.URL) + "'\n"
		if err := os.WriteFile(fakeVC, []byte(fixture), 0700); err != nil {
			t.Fatal(err)
		}
		direct := exec.CommandContext(ctx, piBin,
			"--provider", "void-codex", "--model", "gpt-5.6-sol",
			"--no-context-files", "--no-skills", "--no-prompt-templates", "--no-themes",
			"--tools", "web_search,fetch_content,get_search_content", "--no-session", "--no-approve", "-p", "Say exactly: smoke",
		)
		direct.Env = append(withoutVCEnv(os.Environ()), "HOME="+tmp, "USERPROFILE="+tmp, "PI_CODING_AGENT_DIR="+filepath.Join(tmp, ".pi", "agent"), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "PI_SKIP_VERSION_CHECK=1", "PI_TELEMETRY=0")
		var directOut, directErr bytes.Buffer
		direct.Stdout, direct.Stderr = &directOut, &directErr
		if err := direct.Run(); err != nil || !strings.Contains(directOut.String(), "void smoke ok") {
			t.Fatalf("direct Pi managed web smoke failed: %v\nstdout=%s\nstderr=%s", err, directOut.String(), directErr.String())
		}
		select {
		case seen := <-seenCh:
			if !hasExactManagedWebTools(seen.Body["tools"]) {
				t.Fatalf("direct Pi tools = %#v", seen.Body["tools"])
			}
			if instructions, _ := seen.Body["instructions"].(string); !strings.Contains(instructions, "primary sources") {
				t.Fatalf("direct Pi instructions = %q", instructions)
			}
		case <-time.After(time.Second):
			t.Fatal("direct Pi did not reach relay")
		}
	}
}

func hasExactManagedWebTools(raw any) bool {
	tools, ok := raw.([]any)
	if !ok || len(tools) != 3 {
		return false
	}
	seen := map[string]bool{}
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			return false
		}
		name, _ := tool["name"].(string)
		seen[name] = true
	}
	return seen["web_search"] && seen["fetch_content"] && seen["get_search_content"]
}
