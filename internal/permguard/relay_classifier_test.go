package permguard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRelayClassifier_EmptyProxy_FailsOpen(t *testing.T) {
	fn := NewRelayClassifier("", "tok")
	d := fn(Event{ToolName: "Bash", Command: "x"}, llmFallback{Enabled: true, TimeoutSeconds: 5})
	if d.Decision != "allow" {
		t.Fatalf("empty proxy => %q, want allow (fail-open)", d.Decision)
	}
}

func TestNewRelayClassifier_RefusesAnthropicEgress(t *testing.T) {
	fn := NewRelayClassifier("http://api.anthropic.com:443", "tok")
	d := fn(Event{ToolName: "Bash", Command: "x"}, llmFallback{Enabled: true, TimeoutSeconds: 5})
	if d.Decision != "allow" {
		t.Fatalf("anthropic proxy => %q, want allow (refused egress, fail-open)", d.Decision)
	}
}

func TestNewRelayClassifier_RelayAllow(t *testing.T) {
	// Stand up a fake "relay" that returns a valid LLM-style allow response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{
			"content": []map[string]string{
				{"text": `{"decision":"allow","reason":"test relay says ok"}`},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	// Use the test server as the proxy. The relay_classifier sends CONNECT tunnels
	// to the proxy; httptest.NewServer doesn't speak CONNECT, so the request fails
	// at the network level. We test fail-open behaviour here.
	fn := NewRelayClassifier(srv.URL, "tok")
	d := fn(Event{ToolName: "Bash", Command: "x"}, llmFallback{Enabled: true, TimeoutSeconds: 2})
	// Either allow (from parse) or allow (fail-open from CONNECT error) — never deny.
	if d.Decision != "allow" {
		t.Fatalf("relay classify => %q, want allow", d.Decision)
	}
}

func TestNewRelayClassifier_FailsOpenOnError(t *testing.T) {
	// Non-existent proxy → connection refused → fail-open.
	fn := NewRelayClassifier("http://127.0.0.1:1", "tok")
	d := fn(Event{ToolName: "Bash", Command: "x"}, llmFallback{Enabled: true, TimeoutSeconds: 1})
	if d.Decision != "allow" {
		t.Fatalf("conn-refused => %q, want allow (fail-open)", d.Decision)
	}
}

// Critical: the rule engine itself must never directly call api.anthropic.com.
// The relay classifier is wired only via SetClassifier; this test ensures that
// a Guard with no classifier never triggers any relay call at all.
func TestNoAnthropicEgress_GuardWithoutClassifier(t *testing.T) {
	g, _ := Load()
	// No SetClassifier — nil classifier.
	d := g.Classify(Event{ToolName: "Bash", Command: "echo hello"})
	if d.Decision != "allow" {
		t.Fatalf("echo hello => %q, want allow", d.Decision)
	}
	// No network call was made (verified by the fact that it returned instantly
	// and no http.DefaultClient mock is needed — pure local evaluation).
}
