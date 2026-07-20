package auth

import (
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
		case "/identity-stage/api/device/start":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"deviceCode": secret, "userCode": "ABC12345", "verificationPath": "/device", "intervalSeconds": 1})
		case "/identity-stage/api/device/poll":
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
