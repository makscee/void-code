package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

// runStatusWithServer invokes runStatus logic against a fake server.
// It captures stdout by directly calling the budget display helper.
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
