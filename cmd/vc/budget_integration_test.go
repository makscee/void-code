package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBudgetGate_Integration_100Pct(t *testing.T) {
	// VCD-49 contract (2026-05-30): server returns only { pct, reset_at } — no dollar fields.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-1",
			"email":       "u@x.com",
			"subDaysLeft": 14,
			"pct":         100.0,
			"resetAt":     "2026-06-01T00:00:00.000Z",
		})
	}))
	defer srv.Close()

	me, reached, err := authGate("fake-token", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("authGate error: %v", err)
	}
	if !reached {
		t.Fatal("reached must be true when server responds")
	}
	if me.Pct == nil {
		t.Fatal("Pct must not be nil when returned by server")
	}
	if *me.Pct != 100.0 {
		t.Fatalf("Pct = %f, want 100.0", *me.Pct)
	}

	d := budgetGate(me.Pct, nil)
	if !d.Block {
		t.Error("budgetGate must block at pct=100")
	}
	// Operator constraint 2026-05-30: block message must NOT contain dollar sign.
	if strings.Contains(d.Message, "$") {
		t.Errorf("block message %q must NOT contain '$' (percentages only)", d.Message)
	}
}

func TestBudgetGate_Integration_83Pct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-1",
			"email":       "u@x.com",
			"subDaysLeft": 14,
			"pct":         83.3,
			"resetAt":     "2026-06-01T00:00:00.000Z",
		})
	}))
	defer srv.Close()

	me, reached, err := authGate("fake-token", srv.URL, srv.Client())
	if err != nil || !reached || me.Pct == nil {
		t.Fatalf("authGate: err=%v reached=%v pct=%v", err, reached, me.Pct)
	}

	d := budgetGate(me.Pct, nil)
	if d.Block {
		t.Error("budgetGate must NOT block at pct=83")
	}
	if !d.Warn {
		t.Error("budgetGate must warn at pct=83")
	}
	// Operator constraint 2026-05-30: warn message must NOT contain dollar sign.
	if strings.Contains(d.Message, "$") {
		t.Errorf("warn message %q must NOT contain '$' (percentages only)", d.Message)
	}
}

func TestBudgetGate_Integration_NoBudget(t *testing.T) {
	// Server returns no budget fields (older void-auth or no budget configured) → degrade gracefully.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-1",
			"email":       "u@x.com",
			"subDaysLeft": 14,
		})
	}))
	defer srv.Close()

	me, reached, err := authGate("fake-token", srv.URL, srv.Client())
	if err != nil || !reached {
		t.Fatalf("authGate: err=%v reached=%v", err, reached)
	}

	// pct is nil → budget gate must not block
	if me.Pct != nil {
		t.Fatalf("Pct must be nil when absent, got %v", *me.Pct)
	}
	d := budgetGate(me.Pct, nil)
	if d.Block || d.Warn {
		t.Error("budgetGate must be clean when pct nil (no budget)")
	}
}
