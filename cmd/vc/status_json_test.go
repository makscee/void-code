package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

// The desktop app decides between a "Sign in" button and a chat window before
// it has a terminal to show anything in. It needs the same three-way answer
// `vc status` already computes — no credential, a credential that no longer
// verifies, or a verified identity — as one machine-readable object.
//
// These tests assume seams that do not exist yet, since nothing today lets
// `vc status` speak JSON:
//
//  1. statusCmd gets a bool flag named "json" (default false), added in
//     cmd/vc/status.go's init(), mirroring loginCmd's "json" flag.
//  2. runStatus reads that flag off cmd (when cmd is non-nil — existing
//     tests call runStatus(nil, nil) and must keep getting today's human
//     output) and, when set, dispatches through a package-level indirection
//     instead of printing directly, so a test can swap the destination:
//
//     var statusJSONRunner = func(cfg config.Config, out io.Writer) error {
//     return runStatusJSON(cfg, out)
//     }
//
//  3. runStatusJSON(cfg config.Config, out io.Writer) error in a new
//     cmd/vc/status_json.go, built the same way runStatus itself resolves
//     state — auth.Load() then auth.FetchMe(cfg.AuthHost, ...) — and
//     writing exactly one JSON object to out with these fields:
//
//     - "authState": one of "signed_out", "invalid_credential", "signed_in"
//     — the stable value a GUI branches on.
//     - "identity": present only when authState is "signed_in" (email if
//     the server returned one, else the user id).
//     - "pct": present only when authState is "signed_in" and the server
//     returned a budget percentage.
//     - "resetAt": present only when authState is "signed_in" and the
//     server returned a reset date.
//     - "error": present only when authState is "invalid_credential",
//     holding why verification failed.
//
// A field absent from the object (not merely empty/zero) is what these
// tests check for the states that don't carry it — a struct that always
// serialises pct/resetAt/error as zero values would pass a check that only
// looked at "not truthy".

func decodeSingleJSONObject(t *testing.T, out []byte) map[string]any {
	t.Helper()
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		t.Fatal("no output written; the desktop reader has nothing to parse")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("output is not a JSON object: %v (output: %q)", err, out)
	}
	// A second value on the stream means the desktop's reader would either
	// block waiting for a line terminator that never mattered before, or
	// silently pick up garbage appended after the real status.
	if dec.More() {
		t.Fatalf("more than one JSON value on the stream: %q", out)
	}
	return obj
}

func assertNoANSI(t *testing.T, raw []byte) {
	t.Helper()
	// lipgloss renders every label and value in status.go through styles
	// that emit ESC[...m sequences. Reusing those rendered strings for the
	// JSON fields is the most likely way to get this subtly wrong — a GUI
	// would display the escape codes literally instead of colouring text.
	if bytes.ContainsRune(raw, 0x1b) {
		t.Fatalf("JSON output contains an ANSI escape byte: %q", raw)
	}
	if strings.Contains(string(raw), "[0m") || strings.Contains(string(raw), "[1m") {
		t.Fatalf("JSON output looks like it contains stripped-prefix ANSI codes: %q", raw)
	}
}

// No credential at all must be reported as its own state, not folded into
// the verification-failed case — a GUI must be able to show "Sign in" rather
// than a broken-login message to someone who has simply never signed in.
func TestStatusJSONReportsSignedOutWithNoCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("VC_AUTH_HOST", "http://127.0.0.1:0") // must not be dialed

	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeSingleJSONObject(t, buf.Bytes())
	assertNoANSI(t, buf.Bytes())

	if obj["authState"] != "signed_out" {
		t.Errorf("authState = %v, want signed_out", obj["authState"])
	}
	for _, field := range []string{"identity", "pct", "resetAt", "error"} {
		if _, present := obj[field]; present {
			t.Errorf("signed_out output carries %q = %v, want absent", field, obj[field])
		}
	}
}

// A stored credential that the server now rejects (expired, revoked) is a
// different state from never having signed in: collapsing the two sends
// someone who is already signed in back through a login loop with no
// explanation of why.
func TestStatusJSONReportsInvalidCredentialWhenVerificationFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("VC_AUTH_HOST", srv.URL)

	if err := auth.Save("stale-token"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeSingleJSONObject(t, buf.Bytes())
	assertNoANSI(t, buf.Bytes())

	if obj["authState"] != "invalid_credential" {
		t.Errorf("authState = %v, want invalid_credential", obj["authState"])
	}
	if reason, _ := obj["error"].(string); strings.TrimSpace(reason) == "" {
		t.Error("invalid_credential output carries no error explanation")
	}
	if strings.Contains(buf.String(), "stale-token") {
		t.Error("status leaked the credential value")
	}
	for _, field := range []string{"identity", "pct", "resetAt"} {
		if _, present := obj[field]; present {
			t.Errorf("invalid_credential output carries %q = %v, want absent", field, obj[field])
		}
	}
}

// The signed-in case is the one the desktop actually renders a chat for. The
// identity must be branchable on its own, distinct in shape from the plain
// "logged in as X" prose a human reads.
func TestStatusJSONReportsSignedInWithIdentityAndBudget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"userId":"u-1","email":"person@example.test","pct":42.5,"resetAt":"2026-09-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("VC_AUTH_HOST", srv.URL)

	if err := auth.Save("good-token"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeSingleJSONObject(t, buf.Bytes())
	assertNoANSI(t, buf.Bytes())

	if obj["authState"] != "signed_in" {
		t.Errorf("authState = %v, want signed_in", obj["authState"])
	}
	if obj["identity"] != "person@example.test" {
		t.Errorf("identity = %v, want person@example.test", obj["identity"])
	}
	if pct, ok := obj["pct"].(float64); !ok || pct != 42.5 {
		t.Errorf("pct = %v, want 42.5", obj["pct"])
	}
	if obj["resetAt"] != "2026-09-01T00:00:00Z" {
		t.Errorf("resetAt = %v, want 2026-09-01T00:00:00Z", obj["resetAt"])
	}
	if _, present := obj["error"]; present {
		t.Errorf("signed_in output carries error = %v, want absent", obj["error"])
	}
	if strings.Contains(buf.String(), "good-token") {
		t.Error("status leaked the credential value")
	}
}

// A server that omits budget data (older void-auth, no budget configured)
// must not fabricate pct/resetAt — status.go's own logic never blocks on
// nil budget fields, and the JSON contract must preserve that: a caller
// that always emits pct:0 would tell the desktop "0% used" instead of "no
// budget information available".
func TestStatusJSONOmitsBudgetFieldsWhenServerOmitsThem(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"userId":"u-1"}`))
	}))
	defer srv.Close()
	t.Setenv("VC_AUTH_HOST", srv.URL)

	if err := auth.Save("good-token"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeSingleJSONObject(t, buf.Bytes())
	if obj["authState"] != "signed_in" {
		t.Fatalf("authState = %v, want signed_in", obj["authState"])
	}
	if obj["identity"] != "u-1" {
		t.Errorf("identity = %v, want u-1 (no email returned, fall back to user id)", obj["identity"])
	}
	for _, field := range []string{"pct", "resetAt"} {
		if _, present := obj[field]; present {
			t.Errorf("output carries %q = %v when server sent none, want absent", field, obj[field])
		}
	}
}

// statusCmd must expose the flag at all, with the same off-by-default shape
// as loginCmd's — a bare `vc status` must not change behaviour.
func TestStatusCommandExposesJSONFlag(t *testing.T) {
	flag := statusCmd.Flags().Lookup("json")
	if flag == nil {
		t.Fatal(`status command has no --json flag`)
	}
	if flag.Value.Type() != "bool" {
		t.Fatalf("--json type = %s, want bool", flag.Value.Type())
	}
	if flag.DefValue != "false" {
		t.Fatalf("--json default = %s, want false — a bare `vc status` must stay human-readable", flag.DefValue)
	}
}

// Flipping --json must reach only the JSON path, and leaving it off must
// reach only the human path — a lazy dispatch that always calls one or
// ignores the flag passes any test that checks a single direction.
func TestStatusJSONFlagSelectsRunnerExclusively(t *testing.T) {
	original := statusJSONRunner
	t.Cleanup(func() { statusJSONRunner = original })

	var jsonCalls int
	statusJSONRunner = func(config.Config, io.Writer) error { jsonCalls++; return nil }

	cmd := &cobra.Command{}
	cmd.Flags().Bool("json", false, "")

	// json=true must reach only the JSON runner.
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runStatus(cmd, nil) })
	if err != nil {
		t.Fatalf("runStatus with --json: %v", err)
	}
	if jsonCalls != 1 {
		t.Fatalf("json=true: jsonCalls=%d, want 1", jsonCalls)
	}
	if out != "" {
		t.Errorf("json=true: human output was still printed: %q", out)
	}

	// json=false must not touch the JSON runner and must fall through to
	// the existing human-readable path.
	if err := cmd.Flags().Set("json", "false"); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("VC_AUTH_HOST", "http://127.0.0.1:0")
	out, err = captureStdout(t, func() error { return runStatus(cmd, nil) })
	if err != nil {
		t.Fatalf("runStatus without --json: %v", err)
	}
	if jsonCalls != 1 {
		t.Fatalf("json=false: jsonCalls=%d, want still 1 (unchanged)", jsonCalls)
	}
	if !strings.Contains(out, "not logged in") {
		t.Errorf("json=false: expected the usual human status output, got %q", out)
	}
}
