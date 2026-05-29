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
	// VCD-49 contract (2026-05-30): server returns only { pct, reset_at, status }.
	// Dollar fields (usedUsd, budgetUsd, remainingUsd) are NOT returned for privacy.
	pct := 27.4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-1",
			"email":       "u@example.com",
			"subDaysLeft": 10,
			"pct":         pct,
			"resetAt":     "2026-06-01T00:00:00.000Z",
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
	// Dollar fields are not part of MeResult contract (2026-05-30 privacy change).
}

func TestFetchMe_BudgetPctNull(t *testing.T) {
	// Server returns pct:null (no budget set / unlimited) — Pct field must be nil.
	// VCD-49 contract (2026-05-30): only { pct, reset_at } in budget portion.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write raw JSON with explicit null for pct (no budget configured).
		w.Write([]byte(`{"userId":"u3","email":"u3@x.com","subDaysLeft":10,"pct":null,"resetAt":"2026-06-01T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct != nil {
		t.Errorf("Pct must be nil when no budget configured, got %v", *res.Pct)
	}
}
