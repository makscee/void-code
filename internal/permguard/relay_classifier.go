package permguard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// forbiddenHost is the host the relay classifier must NEVER contact.
	forbiddenHost = "api.anthropic.com"

	defaultLLMPrompt = `You are a security classifier for Claude Code tool calls. Decide if this tool call should be auto-approved or denied.

Tool: {tool_name}
Input: {tool_input}
Working directory: {cwd}

Respond with ONLY a JSON object: {"decision": "allow" or "deny", "reason": "brief explanation"}`
)

// NewRelayClassifier returns a classifier func that calls the relay via HTTPS_PROXY.
// proxyURL must be the http://host:port from HTTPS_PROXY (e.g. "http://127.0.0.1:8888").
// token is the void-code bearer token (CLAUDE_CODE_OAUTH_TOKEN equivalent).
//
// The classifier REFUSES to contact api.anthropic.com — hard-coded invariant for
// the deepseek-only relay stack. On any error or timeout it fails open (allow).
func NewRelayClassifier(proxyURL, token string) func(ev Event, fb llmFallback) Decision {
	return func(ev Event, fb llmFallback) Decision {
		if proxyURL == "" {
			return Decision{Decision: "allow", Reason: "relay classifier: no proxy configured, fail-open"}
		}

		// Refuse Anthropic egress — hard invariant.
		if strings.Contains(strings.ToLower(proxyURL), forbiddenHost) {
			return Decision{Decision: "allow", Reason: "relay classifier: refused Anthropic egress, fail-open"}
		}

		timeout := 5 * time.Second
		if fb.TimeoutSeconds > 0 {
			timeout = time.Duration(fb.TimeoutSeconds) * time.Second
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		d, err := callRelay(ctx, proxyURL, token, ev, fb)
		if err != nil {
			// Fail open on any error.
			return Decision{Decision: "allow", Reason: fmt.Sprintf("relay classifier: %v, fail-open", err)}
		}
		return d
	}
}

// callRelay sends an LLM request via the HTTPS_PROXY MITM to classify the event.
// The request is addressed to a sentinel host (void-relay-classifier) — the proxy
// MITM intercepts all CONNECT tunnels and routes them to deepseek, so the exact
// host does not matter; what matters is that it is NOT api.anthropic.com.
func callRelay(ctx context.Context, proxyURL, token string, ev Event, fb llmFallback) (Decision, error) {
	prompt := fb.Prompt
	if prompt == "" {
		prompt = defaultLLMPrompt
	}
	inputJSON, _ := json.Marshal(map[string]string{
		"command":   ev.Command,
		"file_path": ev.FilePath,
	})
	prompt = strings.NewReplacer(
		"{tool_name}", ev.ToolName,
		"{tool_input}", string(inputJSON),
		"{cwd}", ev.Cwd,
	).Replace(prompt)

	body, _ := json.Marshal(map[string]any{
		"model":      "deepseek-chat",
		"max_tokens": 64,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	// Build transport that routes through the MITM proxy.
	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		return Decision{}, fmt.Errorf("parse proxy URL %q: %w", proxyURL, err)
	}

	// Additional hard-guard: if the proxy hostname resolves to api.anthropic.com, refuse.
	if strings.EqualFold(proxyParsed.Hostname(), forbiddenHost) {
		return Decision{}, fmt.Errorf("proxy host is %s — Anthropic egress refused", forbiddenHost)
	}

	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyParsed),
	}
	client := &http.Client{Transport: transport}

	// Target URL: the relay intercepts at proxy level; host is irrelevant.
	// Use a clearly internal sentinel so any accidental direct-dial fails fast.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://void-relay-classifier/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Decision{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if token != "" {
		req.Header.Set("x-api-key", token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("relay call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("relay returned %d", resp.StatusCode)
	}

	var apiResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return Decision{}, fmt.Errorf("decode response: %w", err)
	}
	if len(apiResp.Content) == 0 {
		return Decision{}, fmt.Errorf("empty content in response")
	}

	var result struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	text := strings.TrimSpace(apiResp.Content[0].Text)
	// Strip markdown code fence if present.
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return Decision{}, fmt.Errorf("parse LLM JSON %q: %w", text, err)
	}
	if result.Decision != "allow" && result.Decision != "deny" {
		return Decision{}, fmt.Errorf("unexpected decision %q", result.Decision)
	}
	return Decision{Decision: result.Decision, Reason: result.Reason}, nil
}
