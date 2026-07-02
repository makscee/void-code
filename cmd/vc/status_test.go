package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/provider"
)

// statusTestServer creates a fake auth server for status tests.
// It serves both /v1/auth/me and /v1/vc/me.
// VCD-65: subDaysLeft is sentinel (36500) and ignored by client.
// Per VCD-49 contract (2026-05-30): /v1/vc/me returns only { pct, reset_at, status } —
// no dollar fields.
func statusTestServer(budgetPct *float64, _ *float64, _ *float64, _ *float64, resetAt string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/me":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"slug":  "testuser",
				"email": "test@example.com",
			})
		case "/v1/vc/me":
			resp := map[string]interface{}{
				"userId":      "user-1",
				"email":       "test@example.com",
				"subDaysLeft": 36500, // sentinel (VCD-65): ignored by new client
			}
			if budgetPct != nil {
				resp["pct"] = *budgetPct
			}
			if resetAt != "" {
				resp["resetAt"] = resetAt
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	runErr := fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out), runErr
}

func TestStatusPrintsActiveHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Pi}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

	out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !strings.Contains(out, "provider:") {
		t.Fatalf("status output missing provider line:\n%s", out)
	}
	if !strings.Contains(out, "harness:") || !strings.Contains(out, "Pi") {
		t.Fatalf("status output missing active Pi harness:\n%s", out)
	}
}

func TestStatusPrintsFriendlyProviderLabelAndMatrix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := provider.Save(provider.Provider{Kind: provider.RelayProvider, ID: "plat-chatgpt"}); err != nil {
		t.Fatalf("save active provider: %v", err)
	}
	if err := provider.SaveLabel("ChatGPT relay"); err != nil {
		t.Fatalf("save active provider label: %v", err)
	}

	out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if !strings.Contains(out, "provider:") || !strings.Contains(out, "ChatGPT relay") {
		t.Fatalf("status output missing friendly provider label:\n%s", out)
	}
	if strings.Contains(out, "plat-chatgpt") {
		t.Fatalf("status output used raw provider id instead of friendly label:\n%s", out)
	}
	wantMatrix := "Claude=DeepSeek/ChatGPT, Codex=ChatGPT, Pi=DeepSeek/ChatGPT"
	if !strings.Contains(out, wantMatrix) {
		t.Fatalf("status output missing compatibility matrix %q:\n%s", wantMatrix, out)
	}
}

func TestStatusBudgetLine_Present(t *testing.T) {
	pct := 27.4
	srv := statusTestServer(&pct, nil, nil, nil, "2026-06-01T00:00:00.000Z")
	defer srv.Close()

	// Test formatBudgetLine directly — percentages only, no dollar values.
	line := formatBudgetLine(27.4, "2026-06-01T00:00:00.000Z")
	if strings.Contains(line, "$") {
		t.Errorf("budget line %q must NOT contain dollar sign", line)
	}
	if !strings.Contains(line, "27") || !strings.Contains(line, "%") {
		t.Errorf("budget line %q must show percentage (27%%)", line)
	}
	if !strings.Contains(line, "reset") || !strings.Contains(line, "Jun") {
		t.Errorf("budget line %q must show reset date as 'Jun 1'", line)
	}
}

func TestStatusBudgetLine_NoDollarValues(t *testing.T) {
	// Operator constraint 2026-05-30: user sees ONLY percentages — NO dollar amounts.
	for _, resetAt := range []string{"2026-06-01T00:00:00.000Z", "", "2026-07-01T00:00:00.000Z"} {
		line := formatBudgetLine(50.0, resetAt)
		if strings.Contains(line, "$") {
			t.Errorf("formatBudgetLine(%q) = %q must NOT contain '$'", resetAt, line)
		}
	}
}

func TestStatusBudgetLine_ZeroUsed(t *testing.T) {
	line := formatBudgetLine(0.0, "2026-06-01T00:00:00.000Z")
	if strings.Contains(line, "$") {
		t.Errorf("budget line %q must not contain dollar sign", line)
	}
	if !strings.Contains(line, "0%") || !strings.Contains(line, "%") {
		t.Errorf("budget line %q must show 0%% used", line)
	}
}
