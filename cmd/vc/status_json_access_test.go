package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// `vc status --json` today answers with three words, and two different facts
// share the last one. Prod, same token, same command:
//
//	VC_AUTH_HOST=https://auth.makscee.ru  → invalid_credential ("not logged in")
//	VC_AUTH_HOST=https://relay.makscee.ru → invalid_credential ("status 402")
//
// The first is a token the server did not accept. The second is a token it
// accepted and a session it verified — with no access granted to the subject
// behind it. The desktop renders both as "your sign-in expired, sign in again",
// so the human presses the button forever while the thing they actually need
// is an operator handing them access.
//
// These tests assume a fourth value in the authState contract:
//
//	"access_not_granted"
//
// The name states the human's situation (nobody has granted this account
// access yet), not the protocol code that revealed it. It deliberately avoids
// "subscription", "budget" and "payment": what a granted access is made of —
// a subscription row, an operator grant, a trial — is an open product question
// for Максим, and the client must not have to be renamed when he answers it.
// Relay's own wire name for this, `budget_exceeded`, is the cautionary example:
// it points at a monthly budget that has nothing to do with the case.
//
// Contract for the state, checked below:
//   - authState is "access_not_granted";
//   - "error" carries a human-readable reason, and does not advise a new sign-in;
//   - "identity", "pct", "resetAt" are ABSENT — the refusal arrives before the
//     server names anyone, so there is nobody to name;
//   - only this one server answer produces it.

// The state exists and carries the refusal without inventing an identity.
func TestStatusJSONReportsAccessNotGrantedWhenServerRefusesAccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		// The real Relay payload, identity included: some deployments echo the
		// subject back with the refusal.
		_, _ = w.Write([]byte(`{"error":"budget_exceeded","subject_id":"u-1","email":"person@example.test","pct":12.5,"resetAt":"2026-09-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("VC_AUTH_HOST", srv.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)

	if err := auth.Save("accepted-token"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	obj := decodeSingleJSONObject(t, buf.Bytes())
	assertNoANSI(t, buf.Bytes())

	if obj["authState"] != "access_not_granted" {
		t.Errorf("authState = %v, want access_not_granted — the token was accepted, so this is not a credential problem", obj["authState"])
	}

	reason, _ := obj["error"].(string)
	if strings.TrimSpace(reason) == "" {
		t.Error("access_not_granted output carries no reason — the desktop has nothing to put on screen except a blank refusal")
	}
	for _, advice := range []string{"vc login", "log in", "login", "sign in", "signin"} {
		if strings.Contains(strings.ToLower(reason), advice) {
			t.Errorf("error = %q tells the human to sign in again; signing in is what already worked", reason)
			break
		}
	}

	// The server refused before it vouched for anyone. Echoing the subject out
	// of the refusal body would show the desktop a name nobody confirmed —
	// and would make an unauthorised session look half-authorised.
	for _, field := range []string{"identity", "pct", "resetAt"} {
		if _, present := obj[field]; present {
			t.Errorf("access_not_granted output carries %q = %v, want absent — nothing in a refusal is confirmed state", field, obj[field])
		}
	}
	if strings.Contains(buf.String(), "accepted-token") {
		t.Error("status leaked the credential value")
	}
}

// One answer, one state. The failure this guards is the cheap fix: "anything
// that is not 401 and not 200 must be the new thing", which trades one
// collapsed pair for another and leaves the desktop showing "no access
// granted" while the server is simply down.
func TestStatusJSONMapsOnlyRefusedAccessToAccessNotGranted(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"ok", http.StatusOK, `{"userId":"u-1","email":"person@example.test"}`, "signed_in"},
		{"unauthorized", http.StatusUnauthorized, ``, "invalid_credential"},
		{"access_refused", http.StatusPaymentRequired, `{"error":"budget_exceeded"}`, "access_not_granted"},
		{"forbidden", http.StatusForbidden, ``, "invalid_credential"},
		{"not_found", http.StatusNotFound, ``, "invalid_credential"},
		{"rate_limited", http.StatusTooManyRequests, ``, "invalid_credential"},
		{"server_error", http.StatusInternalServerError, ``, "invalid_credential"},
		{"bad_gateway", http.StatusBadGateway, ``, "invalid_credential"},
		{"unavailable", http.StatusServiceUnavailable, ``, "invalid_credential"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()
			t.Setenv("VC_AUTH_HOST", srv.URL)
			t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)

			if err := auth.Save("some-token"); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			obj := decodeSingleJSONObject(t, buf.Bytes())
			state, ok := obj["authState"].(string)
			if !ok {
				t.Fatalf("authState = %v, want a string", obj["authState"])
			}
			if state != tc.want {
				t.Errorf("HTTP %d → authState %q, want %s", tc.status, state, tc.want)
			}
			// The four words are the whole contract with the desktop, and
			// desktop/src/renderer/auth-view.ts falls back to the sign-in
			// screen for any word it does not recognise. A state invented at
			// the edge — an empty string, a raw status code — arrives there as
			// silence.
			if !knownAuthStates[state] {
				t.Errorf("HTTP %d → authState %q is not one of the contract words %v", tc.status, state, sortedKnownAuthStates())
			}
			// The word names a situation, not a wire code: putting the
			// protocol number in the contract pins the client to today's
			// transport, and the transport is exactly what is in motion.
			if strings.Contains(state, "402") || strings.Contains(strings.ToLower(state), "http") {
				t.Errorf("authState = %q carries a protocol detail; the contract word must name the human's situation", state)
			}
		})
	}

	// "We could not ask" is a third outcome, not a refusal. Nothing answered,
	// so nothing was granted or denied — the state must stay the one it is
	// today rather than migrate into the new word.
	t.Run("unreachable_host", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		t.Setenv("VC_AUTH_HOST", srv.URL)
		t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)
		srv.Close() // nothing is listening

		if err := auth.Save("some-token"); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		obj := decodeSingleJSONObject(t, buf.Bytes())
		if obj["authState"] == "access_not_granted" {
			t.Error("an unreachable host reported as access_not_granted — we never received an answer, so nobody refused anything")
		}
	})

	// No credential at all still comes before any of this: the host is never
	// dialed, so nothing about access can be known.
	t.Run("no_credential", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("VC_AUTH_HOST", "http://127.0.0.1:0") // must not be dialed

		var buf bytes.Buffer
		if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		obj := decodeSingleJSONObject(t, buf.Bytes())
		if obj["authState"] != "signed_out" {
			t.Errorf("authState = %v, want signed_out", obj["authState"])
		}
	})
}

// The state must be decided from the sentinel FetchMe returns, not from what
// the message happens to say. Both halves are needed to prove that: a 401 whose
// body spells the number must stay a credential problem, and a refusal whose
// body never spells it must still become the new state. A substring sniff
// passes one half and fails the other, whichever way it is written.
func TestStatusJSONDecidesStateFromTheAnswerNotItsText(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "digits_in_a_rejected_token_message",
			status: http.StatusUnauthorized,
			body:   `{"error":"token rejected; see incident 402 for details"}`,
			want:   "invalid_credential",
		},
		{
			name:   "refusal_that_never_names_the_code",
			status: http.StatusPaymentRequired,
			body:   `{"error":"no entitlement for this subject"}`,
			want:   "access_not_granted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("VC_AUTH_HOST", srv.URL)
			t.Setenv("VC_ACCESS_CHECK_HOST", srv.URL)

			if err := auth.Save("some-token"); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			obj := decodeSingleJSONObject(t, buf.Bytes())
			if obj["authState"] != tc.want {
				t.Errorf("HTTP %d with body %s → authState %v, want %s", tc.status, tc.body, obj["authState"], tc.want)
			}
		})
	}
}

// The contract words, in one place: the tests above check membership, and a
// fifth word added without a decision has to be added here first.
var knownAuthStates = map[string]bool{
	"signed_out":         true,
	"invalid_credential": true,
	"access_not_granted": true,
	"signed_in":          true,
}

func sortedKnownAuthStates() []string {
	out := make([]string, 0, len(knownAuthStates))
	for k := range knownAuthStates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
