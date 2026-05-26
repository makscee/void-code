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
