package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// The desktop app has no terminal to show a human-formatted login to, and no way
// for its user to run `vc login` themselves. It needs the same device flow as a
// stream it can parse: one JSON object per line, so a code can be put on screen
// and a browser opened while the poll continues.
//
// These tests pin the contract the desktop will read. They inject every seam —
// start, poll, install, sleep, clock — because the flow is otherwise a network
// call and a sleep loop, and a test that needs either is a test nobody runs.

type fakeDeviceServer struct {
	start     auth.DeviceStartResult
	startErr  error
	pollErrs  []error // consumed in order before pollResult is returned
	pollRes   auth.DevicePollResult
	polls     int
	installed string
	slept     []time.Duration
}

func (f *fakeDeviceServer) deps() deviceLoginDeps {
	return deviceLoginDeps{
		start: func() (auth.DeviceStartResult, error) { return f.start, f.startErr },
		poll: func(string) (auth.DevicePollResult, error) {
			f.polls++
			if len(f.pollErrs) > 0 {
				err := f.pollErrs[0]
				f.pollErrs = f.pollErrs[1:]
				return auth.DevicePollResult{}, err
			}
			return f.pollRes, nil
		},
		install:    func(token string) error { f.installed = token; return nil },
		sleep:      func(d time.Duration) { f.slept = append(f.slept, d) },
		now:        func() time.Time { return time.Unix(0, 0) },
		browserURL: func(path string) string { return "https://auth.example.test" + path },
	}
}

func events(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var parsed []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line is not JSON: %q (%v)", line, err)
		}
		parsed = append(parsed, event)
	}
	return parsed
}

// The prompt carries the two things the user must see. Asserting their exact
// values, not the shape of the object: a version that printed a fixed code would
// satisfy any check that only looked for the field names.
func TestDeviceLoginJSONEmitsPromptFromStartResult(t *testing.T) {
	server := &fakeDeviceServer{
		start:   auth.DeviceStartResult{DeviceCode: "dev-code-1", UserCode: "HAPMGVUV", VerificationPath: "/device", ExpiresIn: 600, Interval: 5},
		pollRes: auth.DevicePollResult{Token: "session.token"},
	}
	var out bytes.Buffer

	if err := runDeviceLoginJSON(server.deps(), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := events(t, &out)
	if len(got) == 0 {
		t.Fatal("no events written")
	}
	prompt := got[0]
	if prompt["event"] != "prompt" {
		t.Errorf("first event = %v, want prompt", prompt["event"])
	}
	if prompt["userCode"] != "HAPMGVUV" {
		t.Errorf("userCode = %v, want HAPMGVUV", prompt["userCode"])
	}
	if prompt["verificationUrl"] != "https://auth.example.test/device" {
		t.Errorf("verificationUrl = %v, want https://auth.example.test/device", prompt["verificationUrl"])
	}
}

// The desktop re-enables its chat button on this event, and nothing else tells
// it the token landed. The token must be installed before it is emitted.
func TestDeviceLoginJSONEmitsAuthorizedAndInstallsToken(t *testing.T) {
	server := &fakeDeviceServer{
		start:   auth.DeviceStartResult{DeviceCode: "dev-code-1", UserCode: "AAAA1111", VerificationPath: "/device", ExpiresIn: 600, Interval: 5},
		pollRes: auth.DevicePollResult{Token: "session.token"},
	}
	var out bytes.Buffer

	if err := runDeviceLoginJSON(server.deps(), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := events(t, &out)
	last := got[len(got)-1]
	if last["event"] != "authorized" {
		t.Errorf("last event = %v, want authorized", last["event"])
	}
	if server.installed != "session.token" {
		t.Errorf("installed token = %q, want session.token", server.installed)
	}
}

// Pending is the normal case for as long as the person is typing the code into
// a browser. It must not produce a second prompt, and must not be reported as an
// error to a UI that would then show a failure mid-login.
func TestDeviceLoginJSONKeepsPollingWhileAuthorizationIsPending(t *testing.T) {
	server := &fakeDeviceServer{
		start:    auth.DeviceStartResult{DeviceCode: "dev-code-1", UserCode: "AAAA1111", VerificationPath: "/device", ExpiresIn: 600, Interval: 5},
		pollErrs: []error{auth.ErrAuthPending, auth.ErrAuthPending},
		pollRes:  auth.DevicePollResult{Token: "session.token"},
	}
	var out bytes.Buffer

	if err := runDeviceLoginJSON(server.deps(), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if server.polls != 3 {
		t.Errorf("polls = %d, want 3", server.polls)
	}
	prompts := 0
	for _, event := range events(t, &out) {
		if event["event"] == "prompt" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Errorf("prompt events = %d, want exactly 1", prompts)
	}
}

// slow_down is the server asking for a longer gap. Ignoring it earns a rate
// limit, which the user would see as a login that simply stops working.
func TestDeviceLoginJSONBacksOffWhenServerAsksToSlowDown(t *testing.T) {
	server := &fakeDeviceServer{
		start:    auth.DeviceStartResult{DeviceCode: "dev-code-1", UserCode: "AAAA1111", VerificationPath: "/device", ExpiresIn: 600, Interval: 5},
		pollErrs: []error{auth.ErrDeviceSlowDown},
		pollRes:  auth.DevicePollResult{Token: "session.token"},
	}
	var out bytes.Buffer

	if err := runDeviceLoginJSON(server.deps(), &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(server.slept) < 2 {
		t.Fatalf("slept %d times, want at least 2", len(server.slept))
	}
	if server.slept[1] <= server.slept[0] {
		t.Errorf("interval did not grow after slow_down: %v then %v", server.slept[0], server.slept[1])
	}
}

// A failure must reach the desktop on the same stream as everything else. A
// non-zero exit alone leaves the window showing a spinner with no explanation.
func TestDeviceLoginJSONReportsExpiryAsAnEventAndAnError(t *testing.T) {
	server := &fakeDeviceServer{
		start:    auth.DeviceStartResult{DeviceCode: "dev-code-1", UserCode: "AAAA1111", VerificationPath: "/device", ExpiresIn: 600, Interval: 5},
		pollErrs: []error{auth.ErrDeviceExpired},
	}
	var out bytes.Buffer

	err := runDeviceLoginJSON(server.deps(), &out)
	if err == nil {
		t.Fatal("expected an error when the pairing code expires")
	}

	got := events(t, &out)
	last := got[len(got)-1]
	if last["event"] != "error" {
		t.Fatalf("last event = %v, want error", last["event"])
	}
	if reason, _ := last["reason"].(string); reason != "expired" {
		t.Errorf("reason = %q, want expired", reason)
	}
	if server.installed != "" {
		t.Errorf("token installed despite expiry: %q", server.installed)
	}
}

// A start that fails must not leave the caller waiting: no prompt, no poll.
func TestDeviceLoginJSONDoesNotPollWhenStartFails(t *testing.T) {
	server := &fakeDeviceServer{startErr: errors.New("upstream 503")}
	var out bytes.Buffer

	if err := runDeviceLoginJSON(server.deps(), &out); err == nil {
		t.Fatal("expected an error when the device start fails")
	}
	if server.polls != 0 {
		t.Errorf("polled %d times after a failed start, want 0", server.polls)
	}
}
