package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuthGate_NoToken verifies that an empty token returns a "Not logged in" error.
func TestAuthGate_NoToken(t *testing.T) {
	err := authGate("", "http://unused-host", &http.Client{})
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
	if !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("error = %q, want it to contain 'Not logged in'", err.Error())
	}
	if !strings.Contains(err.Error(), "vc login") {
		t.Errorf("error = %q, want it to mention 'vc login'", err.Error())
	}
}

// TestAuthGate_ValidToken verifies that a valid token returns nil (safe to spawn).
func TestAuthGate_ValidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"u1","email":"u@example.com","subDaysLeft":7}`))
	}))
	defer srv.Close()

	err := authGate("valid-token", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("expected nil for valid token, got %v", err)
	}
}

// TestAuthGate_RejectedToken verifies that a 401 from the auth server returns
// a "Session token rejected" error and does not expose raw HTTP details.
func TestAuthGate_RejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := authGate("garbage-token", srv.URL, srv.Client())
	if err == nil {
		t.Fatal("expected error for rejected token, got nil")
	}
	if !strings.Contains(err.Error(), "Session token rejected") {
		t.Errorf("error = %q, want it to contain 'Session token rejected'", err.Error())
	}
	if !strings.Contains(err.Error(), "vc login") {
		t.Errorf("error = %q, want it to mention 'vc login'", err.Error())
	}
}

// TestAuthGate_NetworkError verifies that a network error does NOT block spawn —
// transient auth-server blips must not lock the user out.
func TestAuthGate_NetworkError(t *testing.T) {
	// Point at a port that refuses connections.
	err := authGate("any-token", "http://127.0.0.1:1", &http.Client{})
	if err != nil {
		t.Fatalf("expected nil for network error (non-blocking), got %v", err)
	}
}

// TestAuthGate_ServerError verifies that a 5xx from the auth server does NOT
// block spawn — server-side errors must not lock the user out.
func TestAuthGate_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := authGate("any-token", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("expected nil for server error (non-blocking), got %v", err)
	}
}
