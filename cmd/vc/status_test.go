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
func statusTestServer(subDaysLeft int, budgetPct *float64, budgetUsd *float64, usedUsd *float64, remainingUsd *float64, resetAt string) *httptest.Server {
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
				"subDaysLeft": subDaysLeft,
			}
			if budgetPct != nil {
				resp["pct"] = *budgetPct
			}
			if budgetUsd != nil {
				resp["budgetUsd"] = *budgetUsd
			}
			if usedUsd != nil {
				resp["usedUsd"] = *usedUsd
			}
			if remainingUsd != nil {
				resp["remainingUsd"] = *remainingUsd
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
	budget := 45.0
	used := 12.34
	remaining := 32.66
	srv := statusTestServer(10, &pct, &budget, &used, &remaining, "2026-06-01T00:00:00.000Z")
	defer srv.Close()

	// Test formatBudgetLine directly.
	line := formatBudgetLine(used, budget, remaining, "2026-06-01T00:00:00.000Z")
	if !strings.Contains(line, "12.34") {
		t.Errorf("budget line %q must show used amount 12.34", line)
	}
	if !strings.Contains(line, "45.00") {
		t.Errorf("budget line %q must show budget amount 45.00", line)
	}
	if !strings.Contains(line, "32.66") {
		t.Errorf("budget line %q must show remaining amount 32.66", line)
	}
	if !strings.Contains(line, "reset") || !strings.Contains(line, "2026-06-01") {
		t.Errorf("budget line %q must show reset date 2026-06-01", line)
	}
}

func TestStatusBudgetLine_NoBudget(t *testing.T) {
	// When budget_usd=0 (no cap): remaining+pct nil — no budget line shown.
	line := formatBudgetLine(5.0, 0, -1, "")
	// budget=0 means "no cap" — we show "no budget cap" or similar, NOT a misleading $0.
	if strings.Contains(line, "$0.00 budget") {
		t.Errorf("budget line %q must not show '$0.00 budget' (no-cap case)", line)
	}
}

func TestStatusBudgetLine_ZeroUsed(t *testing.T) {
	line := formatBudgetLine(0.0, 45.0, 45.0, "2026-06-01T00:00:00.000Z")
	if !strings.Contains(line, "0.00") {
		t.Errorf("budget line %q must show $0.00 used", line)
	}
	if !strings.Contains(line, "45.00") {
		t.Errorf("budget line %q must show budget $45.00", line)
	}
}
