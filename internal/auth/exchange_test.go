package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExchange_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/auth/access-codes/exchange" {
			t.Errorf("path = %s, want /v1/auth/access-codes/exchange", r.URL.Path)
		}
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Code != "ABCD-EFG2" {
			t.Errorf("code = %q, want ABCD-EFG2", body.Code)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"token":  "tok-xyz",
			"userId": "user-1",
		})
	}))
	defer srv.Close()

	res, err := Exchange(srv.URL, "ABCD-EFG2", srv.Client())
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if res.Token != "tok-xyz" {
		t.Errorf("Token = %q, want tok-xyz", res.Token)
	}
	if res.UserID != "user-1" {
		t.Errorf("UserID = %q, want user-1", res.UserID)
	}
}

func TestExchange_400_Invalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	_, err := Exchange(srv.URL, "AAAA-BBBB", srv.Client())
	if err != ErrCodeInvalid {
		t.Fatalf("expected ErrCodeInvalid, got %v", err)
	}
}

func TestExchange_410_Expired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"expired"}`, http.StatusGone)
	}))
	defer srv.Close()

	_, err := Exchange(srv.URL, "AAAA-BBBB", srv.Client())
	if err != ErrCodeExpired {
		t.Fatalf("expected ErrCodeExpired, got %v", err)
	}
}

func TestExchange_InvalidFormat(t *testing.T) {
	// Should be rejected locally before any network call.
	_, err := Exchange("http://unused", "bad-code", nil)
	if err == nil {
		t.Fatal("expected error for bad format")
	}
}

func TestExchange_InvalidFormat_Lowercase(t *testing.T) {
	_, err := Exchange("http://unused", "abcd-efgh", nil)
	if err == nil {
		t.Fatal("expected error for lowercase code")
	}
}

func TestExchange_ValidFormats(t *testing.T) {
	// These codes match the regex — server call would proceed (not rejected locally).
	// We use a 400-returning server just to confirm the regex passed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	cases := []string{"AAAA-BBBB", "2222-9999", "HHHH-ZZZZ"}
	for _, c := range cases {
		_, err := Exchange(srv.URL, c, srv.Client())
		if err != ErrCodeInvalid {
			t.Errorf("code %q: expected ErrCodeInvalid (passed regex), got %v", c, err)
		}
	}
}
