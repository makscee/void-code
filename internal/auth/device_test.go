package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicDeviceStartAndPollContract(t *testing.T) {
	secret := "device-poll-secret-value"
	token := "opaque-vc-audience-token"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("distributed client sent authorization secret")
		}
		if strings.Contains(r.URL.RawQuery, secret) {
			t.Error("device code leaked in URL")
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != "vc" {
			t.Errorf("client_id=%q", body["client_id"])
		}
		switch r.URL.Path {
		case "/v1/public/device/start":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"deviceCode": secret, "userCode": "ABC12345", "verificationPath": "/device", "intervalSeconds": 1})
		case "/v1/public/device/poll":
			if body["device_code"] != secret {
				t.Error("poll secret missing from body")
			}
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusAccepted)
				json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "redeemed", "token": token, "audience": "vc", "expiresAt": int64(2_000_000_000_000)})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	start, err := DeviceStart(srv.URL, "Laptop", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if start.DeviceCode != secret || start.UserCode == "" || start.Interval != 1 {
		t.Fatal("malformed start result")
	}
	if _, err := DevicePoll(srv.URL, secret, srv.Client()); !errors.Is(err, ErrAuthPending) {
		t.Fatalf("pending=%v", err)
	}
	result, err := DevicePoll(srv.URL, secret, srv.Client())
	if err != nil || result.Token != token || result.Audience != "vc" {
		t.Fatalf("redeem=%v", err)
	}
}

func TestCanaryDeviceClientAndAudienceAreExplicitlyIsolated(t *testing.T) {
	t.Setenv("VC_IDENTITY_CLIENT_ID", "vc-canary")
	t.Setenv("VC_IDENTITY_AUDIENCE", "vc-canary")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != "vc-canary" {
			t.Fatalf("client_id=%q", body["client_id"])
		}
		if strings.HasSuffix(r.URL.Path, "/start") {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"deviceCode": "poll", "userCode": "ABC12345", "verificationPath": "/device", "intervalSeconds": 1})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "session.secret", "audience": "vc-canary", "expiresAt": int64(2_000_000_000_000)})
	}))
	defer srv.Close()
	if _, err := DeviceStart(srv.URL, "Void Code canary", srv.Client()); err != nil {
		t.Fatal(err)
	}
	result, err := DevicePoll(srv.URL, "poll", srv.Client())
	if err != nil || result.Audience != "vc-canary" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDevicePollTruthfulTerminalStates(t *testing.T) {
	cases := map[string]error{"slow_down": ErrDeviceSlowDown, "expired": ErrDeviceExpired, "denied": ErrDeviceDenied, "consumed": ErrDeviceConsumed, "invalid": ErrDeviceInvalid}
	for status, want := range cases {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"status": status})
			}))
			defer srv.Close()
			_, err := DevicePoll(srv.URL, "poll-secret", srv.Client())
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
}

func TestDeviceRejectsMissingMalformedAndStaleExpiryWithoutInstallingValue(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	cases := []string{
		`{"token":"opaque","audience":"vc"}`,
		`{"token":"opaque","audience":"vc","expiresAt":"later"}`,
		`{"token":"opaque","audience":"vc","expiresAt":2000000000000}`,
		`{"token":"opaque","audience":"vc","expiresAt":1999999999999}`,
	}
	for _, payload := range cases {
		t.Run(payload, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(payload)) }))
			defer srv.Close()
			result, err := devicePollAt(srv.URL, "poll-secret", srv.Client(), func() time.Time { return now })
			if !errors.Is(err, ErrDeviceMalformed) || result.Token != "" || strings.Contains(err.Error(), "opaque") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestDecodeOneRequiresExactlyOneBoundedJSONObject(t *testing.T) {
	for _, payload := range []string{
		``,
		`[]`,
		`null`,
		`{"ok":true}{"extra":true}`,
		`{"ok":true} trailing`,
	} {
		var target struct {
			OK bool `json:"ok"`
		}
		if err := decodeOne(strings.NewReader(payload), &target); err == nil {
			t.Fatalf("accepted %q", payload)
		}
	}
	oversized := append([]byte(`{"value":"`), bytes.Repeat([]byte("x"), (64<<10)+1)...)
	oversized = append(oversized, []byte(`"}`)...)
	var target map[string]string
	if err := decodeOne(bytes.NewReader(oversized), &target); err == nil {
		t.Fatal("accepted oversized response")
	}
}

func TestDeviceRejectsMalformedAndWrongAudienceWithoutEcho(t *testing.T) {
	material := "sensitive-material"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token": material, "audience": "arena", "expiresAt": int64(1)})
	}))
	defer srv.Close()
	_, err := DevicePoll(srv.URL, material, srv.Client())
	if !errors.Is(err, ErrDeviceMalformed) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), material) {
		t.Fatal("sensitive material leaked in error")
	}
}
