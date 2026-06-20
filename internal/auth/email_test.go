package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmailStart_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/auth/login/email/start" {
			t.Errorf("path = %s, want /v1/auth/login/email/start", r.URL.Path)
		}
		var body struct {
			Email string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Email != "test@example.com" {
			t.Errorf("email = %q, want test@example.com", body.Email)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"sent": true})
	}))
	defer srv.Close()

	if _, err := EmailStart(srv.URL, "test@example.com", srv.Client()); err != nil {
		t.Fatalf("EmailStart: %v", err)
	}
}

func TestEmailStart_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "rate_limited", "retryAfterMs": 30000})
	}))
	defer srv.Close()

	_, err := EmailStart(srv.URL, "test@example.com", srv.Client())
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
}

func TestEmailVerify_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/auth/login/email/verify" {
			t.Errorf("path = %s, want /v1/auth/login/email/verify", r.URL.Path)
		}
		var body struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Email != "test@example.com" {
			t.Errorf("email = %q", body.Email)
		}
		if body.Code != "123456" {
			t.Errorf("code = %q", body.Code)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token":     "session-tok-abc",
			"sessionId": "sess-123",
			"expiresAt": 9999999999,
		})
	}))
	defer srv.Close()

	res, err := EmailVerify(srv.URL, "test@example.com", "123456", srv.Client())
	if err != nil {
		t.Fatalf("EmailVerify: %v", err)
	}
	if res.Token != "session-tok-abc" {
		t.Errorf("Token = %q, want session-tok-abc", res.Token)
	}
}

func TestEmailVerify_WrongCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "wrong"})
	}))
	defer srv.Close()

	_, err := EmailVerify(srv.URL, "test@example.com", "000000", srv.Client())
	if !errors.Is(err, ErrOTPWrong) {
		t.Fatalf("expected ErrOTPWrong, got %v", err)
	}
}

func TestEmailVerify_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "internal_error"})
	}))
	defer srv.Close()

	_, err := EmailVerify(srv.URL, "test@example.com", "123456", srv.Client())
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}
