package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAuthGate_NoToken verifies that an empty token returns a "Not logged in" error.
func TestAuthGate_NoToken(t *testing.T) {
	_, _, err := authGate("", "http://unused-host", &http.Client{})
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

// TestAuthGate_ValidToken verifies that a valid token returns nil error and reached=true.
func TestAuthGate_ValidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"u1","email":"u@example.com","subDaysLeft":7}`))
	}))
	defer srv.Close()

	_, reached, err := authGate("valid-token", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("expected nil for valid token, got %v", err)
	}
	if !reached {
		t.Error("expected reached=true for reachable server")
	}
}

func TestAuthGateIgnoresFreshCacheAfterRevocation(t *testing.T) {
	var revoked bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"before@example.test","pct":1}`))
	}))
	defer srv.Close()
	if _, err := cachedFetchMe(srv.URL, "same-token", srv.Client()); err != nil {
		t.Fatal(err)
	}
	revoked = true
	if _, _, err := authGate("same-token", srv.URL, srv.Client()); err == nil {
		t.Fatal("fresh cached identity admitted revoked token")
	}
}

// TestAuthGate_RejectedToken verifies that a 401 from the auth server returns
// a "Session token rejected" error and does not expose raw HTTP details.
func TestAuthGate_RejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, err := authGate("garbage-token", srv.URL, srv.Client())
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

func TestAuthGate_IdentityTokenFailsClosedOnLiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer srv.Close()
	if _, _, err := authGate("session-secret.verifier-secret", srv.URL, srv.Client()); err == nil {
		t.Fatal("live 401 must deny an identity-shaped token")
	}
}

func TestAuthGate_MalformedDottedTokenStillFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	for _, token := range []string{".secret", "session.", "one.two.three"} {
		if _, _, err := authGate(token, srv.URL, srv.Client()); err == nil {
			t.Fatalf("malformed token %q did not fail closed", token)
		}
	}
}

// TestAuthGate_NetworkAndServerErrorsDenyAdmission: without a live response,
// vc cannot safely admit a session based on stale identity or budget data.
func TestAuthGate_NetworkAndServerErrorsDenyAdmission(t *testing.T) {
	_, reached, err := authGate("any-token", "http://127.0.0.1:1", &http.Client{})
	if err == nil || reached {
		t.Fatalf("network result err=%v reached=%v, want deny", err, reached)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	defer srv.Close()
	_, reached, err = authGate("any-token", srv.URL, srv.Client())
	if err == nil || reached {
		t.Fatalf("server result err=%v reached=%v, want deny", err, reached)
	}
}

// TestAuthGate_ReachesServer verifies authGate sets reached=true and parses userId/email.
// VCD-65: SubDaysLeft removed; subDaysLeft in JSON is now ignored by the client.
func TestAuthGate_ReachesServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Server sends sentinel subDaysLeft=36500; client ignores it.
		w.Write([]byte(`{"userId":"u1","email":"u@example.com","subDaysLeft":36500}`))
	}))
	defer srv.Close()

	me, reached, err := authGate("valid-token", srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("expected nil err for reachable server, got %v", err)
	}
	if !reached {
		t.Error("expected reached=true for reachable server")
	}
	if me.UserID != "u1" {
		t.Errorf("UserID = %q, want u1", me.UserID)
	}
}
