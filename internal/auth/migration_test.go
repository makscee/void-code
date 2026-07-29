package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMigrationProtocolPreservesExactLegacyProofAndValidatesIdentity(t *testing.T) {
	legacy := "legacy-secret-byte-exact\n"
	subject := "88888888-8888-4888-8888-888888888888"
	exchange := "opaque-exchange"
	identity := "identity-session.identity-secret"
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["client_id"] != "vc" || body["legacy_token"] != legacy {
			t.Fatalf("request did not preserve exact legacy proof: %#v", body)
		}
		switch r.URL.Path {
		case "/v1/public/migration/start":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true,"exchange":"` + exchange + `"}`))
		case "/v1/public/migration/complete":
			if body["exchange"] != exchange || body["code"] != "246810" {
				t.Fatalf("wrong completion proof: %#v", body)
			}
			_, _ = w.Write([]byte(`{"ok":true,"token":"` + identity + `","subject_id":"` + subject + `","audience":"vc","expires_at":` + strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10) + `}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	started, err := MigrationStart(server.URL, legacy, server.Client())
	if err != nil || started.Exchange != exchange {
		t.Fatalf("start=%+v err=%v", started, err)
	}
	completed, err := MigrationComplete(server.URL, started.Exchange, legacy, "246810", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if completed.Token != identity || completed.SubjectID != subject || completed.Audience != "vc" {
		t.Fatalf("complete=%+v", completed)
	}
	if got := strings.Join(calls, ","); got != "POST /v1/public/migration/start,POST /v1/public/migration/complete" {
		t.Fatalf("calls=%q", got)
	}
}

func TestMigrationAndRelayResponsesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		call   func(string, *http.Client) error
		want   error
	}{
		{"start trailing JSON", http.StatusCreated, `{"ok":true,"exchange":"x"}{}`, func(host string, c *http.Client) error { _, err := MigrationStart(host, "legacy", c); return err }, ErrMigrationUnavailable},
		{"complete wrong audience", http.StatusOK, `{"ok":true,"token":"new","subject_id":"s","audience":"other","expires_at":9999999999999}`, func(host string, c *http.Client) error {
			_, err := MigrationComplete(host, "x", "legacy", "246810", c)
			return err
		}, ErrMigrationUnavailable},
		{"complete replay", http.StatusConflict, `{"error":{"code":"replayed"}}`, func(host string, c *http.Client) error {
			_, err := MigrationComplete(host, "x", "legacy", "246810", c)
			return err
		}, ErrMigrationConflict},
		{"relay inactive entitlement", http.StatusPaymentRequired, `{}`, func(host string, c *http.Client) error { _, err := FetchRelayMe(host, "new", c); return err }, ErrEntitlementDenied},
		{"relay trailing JSON", http.StatusOK, `{"subject_id":"s"}{}`, func(host string, c *http.Client) error { _, err := FetchRelayMe(host, "new", c); return err }, ErrMigrationSource},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			if err := tc.call(server.URL, server.Client()); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
		})
	}
}
