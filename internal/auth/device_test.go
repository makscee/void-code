package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeviceStart_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/auth/device/start" {
			t.Errorf("path = %s, want /v1/auth/device/start", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"device_code":               "dev-code-abc",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          "https://auth.makscee.ru/device",
			"verification_uri_complete": "https://auth.makscee.ru/device?code=ABCD-EFGH",
			"expires_in":                600,
			"interval":                  5,
		})
	}))
	defer srv.Close()

	res, err := DeviceStart(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("DeviceStart: %v", err)
	}
	if res.DeviceCode != "dev-code-abc" {
		t.Errorf("DeviceCode = %q", res.DeviceCode)
	}
	if res.UserCode != "ABCD-EFGH" {
		t.Errorf("UserCode = %q", res.UserCode)
	}
	if res.Interval != 5 {
		t.Errorf("Interval = %d, want 5", res.Interval)
	}
	if res.ExpiresIn != 600 {
		t.Errorf("ExpiresIn = %d, want 600", res.ExpiresIn)
	}
}

func TestDevicePoll_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/device/poll" {
			t.Errorf("path = %s, want /v1/auth/device/poll", r.URL.Path)
		}
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.DeviceCode != "dev-code-abc" {
			t.Errorf("device_code = %q", body.DeviceCode)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"session_token": "session-tok-xyz",
			"user_id":       "user-42",
		})
	}))
	defer srv.Close()

	res, err := DevicePoll(srv.URL, "dev-code-abc", srv.Client())
	if err != nil {
		t.Fatalf("DevicePoll: %v", err)
	}
	if res.Token != "session-tok-xyz" {
		t.Errorf("Token = %q, want session-tok-xyz", res.Token)
	}
	if res.UserID != "user-42" {
		t.Errorf("UserID = %q, want user-42", res.UserID)
	}
}

func TestDevicePoll_AuthorizationPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer srv.Close()

	_, err := DevicePoll(srv.URL, "dev-code", srv.Client())
	if err != ErrAuthPending {
		t.Fatalf("expected ErrAuthPending, got %v", err)
	}
}

func TestDevicePoll_ExpiredToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "expired_token"})
	}))
	defer srv.Close()

	_, err := DevicePoll(srv.URL, "dev-code", srv.Client())
	if err != ErrDeviceExpired {
		t.Fatalf("expected ErrDeviceExpired, got %v", err)
	}
}

func TestDevicePoll_AccessDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
	}))
	defer srv.Close()

	_, err := DevicePoll(srv.URL, "dev-code", srv.Client())
	if err != ErrDeviceDenied {
		t.Fatalf("expected ErrDeviceDenied, got %v", err)
	}
}

func TestDevicePoll_SlowDown(t *testing.T) {
	// slow_down is treated as pending — caller backs off.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
	}))
	defer srv.Close()

	_, err := DevicePoll(srv.URL, "dev-code", srv.Client())
	if err != ErrAuthPending {
		t.Fatalf("expected ErrAuthPending for slow_down, got %v", err)
	}
}

// TestDevicePoll_PollLoop simulates the authorization_pending loop:
// first two polls return pending, third returns success.
func TestDevicePoll_PollLoop(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount < 3 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"session_token": "tok-final",
			"user_id":       "user-loop",
		})
	}))
	defer srv.Close()

	var result DevicePollResult
	for i := 0; i < 5; i++ {
		r, err := DevicePoll(srv.URL, "dev-code", srv.Client())
		if err == ErrAuthPending {
			continue
		}
		if err != nil {
			t.Fatalf("poll %d unexpected error: %v", i+1, err)
		}
		result = r
		break
	}
	if result.Token != "tok-final" {
		t.Errorf("Token = %q, want tok-final", result.Token)
	}
	if callCount != 3 {
		t.Errorf("callCount = %d, want 3", callCount)
	}
}
