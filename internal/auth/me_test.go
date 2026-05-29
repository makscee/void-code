package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchMe_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/vc/me" {
			t.Errorf("path = %s, want /v1/vc/me", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-42",
			"email":       "user@example.com",
			"subDaysLeft": 14,
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "test-token", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "user-42" {
		t.Errorf("UserID = %q", res.UserID)
	}
	if res.Email != "user@example.com" {
		t.Errorf("Email = %q", res.Email)
	}
	if res.SubDaysLeft != 14 {
		t.Errorf("SubDaysLeft = %d, want 14", res.SubDaysLeft)
	}
}

func TestFetchMe_Unlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "admin-1",
			"email":       "admin@example.com",
			"subDaysLeft": -1,
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "admin-tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.SubDaysLeft != -1 {
		t.Errorf("SubDaysLeft = %d, want -1 (unlimited)", res.SubDaysLeft)
	}
}

func TestFetchMe_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "bad-token", srv.Client())
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn, got %v", err)
	}
}

func TestFetchMe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "tok", srv.Client())
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

// VCD-49: budget fields decoded from /v1/vc/me response.

func TestFetchMe_BudgetFields(t *testing.T) {
	used := 12.34
	budget := 45.0
	remaining := 32.66
	pct := 27.4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":       "user-1",
			"email":        "u@example.com",
			"subDaysLeft":  10,
			"usedUsd":      used,
			"budgetUsd":    budget,
			"remainingUsd": remaining,
			"pct":          pct,
			"resetAt":      "2026-06-01T00:00:00.000Z",
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct == nil {
		t.Fatal("Pct must not be nil when returned by server")
	}
	if *res.Pct != pct {
		t.Errorf("Pct = %f, want %f", *res.Pct, pct)
	}
	if res.UsedUsd == nil || *res.UsedUsd != used {
		t.Errorf("UsedUsd = %v, want %f", res.UsedUsd, used)
	}
	if res.BudgetUsd == nil || *res.BudgetUsd != budget {
		t.Errorf("BudgetUsd = %v, want %f", res.BudgetUsd, budget)
	}
	if res.RemainingUsd == nil || *res.RemainingUsd != remaining {
		t.Errorf("RemainingUsd = %v, want %f", res.RemainingUsd, remaining)
	}
	if res.ResetAt != "2026-06-01T00:00:00.000Z" {
		t.Errorf("ResetAt = %q", res.ResetAt)
	}
}

func TestFetchMe_BudgetFieldsAbsent(t *testing.T) {
	// Older server: no budget fields — must decode without error; budget fields nil.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-2",
			"email":       "u2@example.com",
			"subDaysLeft": 5,
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct != nil {
		t.Errorf("Pct must be nil when absent from response, got %v", *res.Pct)
	}
	if res.UsedUsd != nil {
		t.Errorf("UsedUsd must be nil when absent, got %v", *res.UsedUsd)
	}
}

func TestFetchMe_BudgetPctNull(t *testing.T) {
	// Server returns pct:null (no budget set / unlimited) — Pct field must be nil.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write raw JSON with explicit null for pct and remainingUsd.
		w.Write([]byte(`{"userId":"u3","email":"u3@x.com","subDaysLeft":10,"usedUsd":5.0,"budgetUsd":0,"remainingUsd":null,"pct":null,"resetAt":"2026-06-01T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct != nil {
		t.Errorf("Pct must be nil when budget=0 (no cap), got %v", *res.Pct)
	}
	if res.RemainingUsd != nil {
		t.Errorf("RemainingUsd must be nil when no cap, got %v", *res.RemainingUsd)
	}
}
