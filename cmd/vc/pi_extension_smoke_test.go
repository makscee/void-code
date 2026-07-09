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
		"--model", "gpt-5.5",
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
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_smoke\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"void smoke ok\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"msg_smoke\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"void smoke ok\"}]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":3,\"output_tokens_details\":{\"reasoning_tokens\":2}}}}\n\n")
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
		if instructions, ok := seen.Body["instructions"].(string); !ok || !strings.Contains(instructions, "expert coding assistant operating inside pi") {
			t.Fatalf("instructions do not include Pi system prompt: %#v", seen.Body["instructions"])
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
}
