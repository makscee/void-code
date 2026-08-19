package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

func TestDesktopAuthStatusIsValueFree(t *testing.T) {
	for _, test := range []struct{ token, want string }{{"", "sign_in_required"}, {"session.secret", "ready"}} {
		var out bytes.Buffer
		deps := desktopAuthDeps{load: func() (string, bool, error) { return test.token, false, nil }, verify: func(string) error { return nil }}
		if err := runDesktopAuthStatus(&out, deps); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"state":"`+test.want+`"`) {
			t.Fatalf("event=%q", out.String())
		}
		if test.token != "" && strings.Contains(out.String(), test.token) {
			t.Fatal("status exposed credential")
		}
	}
}

func TestDesktopAuthStatusRejectsRevokedCredentialButDegradesOnNetworkFailure(t *testing.T) {
	for _, test := range []struct {
		verify error
		want   string
	}{{auth.ErrNotLoggedIn, "sign_in_required"}, {errors.New("network unavailable"), "ready"}} {
		var out bytes.Buffer
		deps := desktopAuthDeps{load: func() (string, bool, error) { return "session.secret", false, nil }, verify: func(string) error { return test.verify }}
		if err := runDesktopAuthStatus(&out, deps); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), `"state":"`+test.want+`"`) {
			t.Fatalf("event=%q", out.String())
		}
		if strings.Contains(out.String(), "session.secret") {
			t.Fatal("status exposed credential")
		}
	}
}

func TestDesktopAuthStartEmitsOnlyAuthorizationThenCompletion(t *testing.T) {
	var out, warnings bytes.Buffer
	stored := "old-session.old-secret"
	deps := desktopAuthDeps{
		start: func(string, string, *http.Client) (auth.DeviceStartResult, error) {
			return auth.DeviceStartResult{DeviceCode: "private-device-code", UserCode: "ABCD-EFGH", VerificationPath: "/device", ExpiresIn: 600, Interval: 1}, nil
		},
		poll: func(string, string, *http.Client) (auth.DevicePollResult, error) {
			return auth.DevicePollResult{Token: "new-session.new-secret"}, nil
		},
		load: func() (string, bool, error) { return stored, false, nil },
		save: func(value string) error { stored = value; return nil },
		revoke: func(value string) error {
			if value != "old-session.old-secret" {
				t.Fatalf("revoked=%q", value)
			}
			return nil
		},
		wait: func(context.Context, time.Duration) error { return nil }, client: &http.Client{},
	}
	if err := runDesktopAuthStart(context.Background(), &out, &warnings, "https://auth.example", deps); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `"type":"authorization"`) || !strings.Contains(got, `"verificationUrl":"https://auth.example/device"`) || !strings.Contains(got, `"userCode":"ABCD-EFGH"`) || !strings.Contains(got, `"type":"complete"`) {
		t.Fatalf("events=%q", got)
	}
	for _, secret := range []string{"private-device-code", "old-session.old-secret", "new-session.new-secret"} {
		if strings.Contains(got, secret) || strings.Contains(warnings.String(), secret) {
			t.Fatalf("output exposed %q", secret)
		}
	}
	if stored != "new-session.new-secret" {
		t.Fatalf("stored=%q", stored)
	}
}

func TestDesktopAuthCancellationDoesNotPollOrChangeCredential(t *testing.T) {
	polled := false
	deps := desktopAuthDeps{
		start: func(string, string, *http.Client) (auth.DeviceStartResult, error) {
			return auth.DeviceStartResult{DeviceCode: "device", UserCode: "CODE", VerificationPath: "/device", ExpiresIn: 600, Interval: 1}, nil
		},
		poll: func(string, string, *http.Client) (auth.DevicePollResult, error) {
			polled = true
			return auth.DevicePollResult{}, nil
		},
		load: func() (string, bool, error) { return "existing.token", false, nil },
		save: func(string) error { t.Fatal("saved after cancellation"); return nil }, revoke: func(string) error { return nil },
		wait: func(context.Context, time.Duration) error { return context.Canceled }, client: &http.Client{},
	}
	err := runDesktopAuthStart(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, "https://auth.example", deps)
	if err == nil || !strings.Contains(err.Error(), "cancelled") || polled {
		t.Fatalf("err=%v polled=%v", err, polled)
	}
}

func TestDesktopAuthPendingRetries(t *testing.T) {
	polls := 0
	deps := desktopAuthDeps{
		start: func(string, string, *http.Client) (auth.DeviceStartResult, error) {
			return auth.DeviceStartResult{DeviceCode: "device", UserCode: "CODE", VerificationPath: "/device", ExpiresIn: 600, Interval: 1}, nil
		},
		poll: func(string, string, *http.Client) (auth.DevicePollResult, error) {
			polls++
			if polls == 1 {
				return auth.DevicePollResult{}, auth.ErrAuthPending
			}
			return auth.DevicePollResult{}, errors.New("stop")
		},
		load: func() (string, bool, error) { return "", false, auth.ErrNotLoggedIn }, save: func(string) error { return nil }, revoke: func(string) error { return nil },
		wait: func(context.Context, time.Duration) error { return nil }, client: &http.Client{},
	}
	err := runDesktopAuthStart(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, "https://auth.example", deps)
	if err == nil || polls != 2 {
		t.Fatalf("err=%v polls=%d", err, polls)
	}
}
