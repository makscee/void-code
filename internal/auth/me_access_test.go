package auth

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Live fact, prod 2026-08-23: the same token against the same route gives two
// different answers depending on which service still owns it —
//
//	auth.makscee.ru  /v1/vc/me → 401  (legacy checker: the token is not accepted)
//	relay.makscee.ru /v1/vc/me → 402  (Relay: the token IS accepted, the session
//	                                   verified, but the subject has no access)
//
// FetchMe collapses the second one into a generic `vc/me returned status 402`,
// so every caller sees "verification failed" and tells the human to log in
// again. Logging in again cannot help: the sign-in worked. What is missing is
// access, and only an operator can grant it.
//
// These tests assume one seam that does not exist yet:
//
//	ErrAccessNotGranted — a sentinel in internal/auth/errors.go, returned by
//	FetchMe when the server answers 402, in the same shape ErrNotLoggedIn is
//	returned for 401. Callers branch with errors.Is, never on message text.
//
// Nothing here mentions the number 402 outside the HTTP fixtures on purpose:
// the protocol code is the evidence, the sentinel is the fact.

// 402 must arrive as its own sentinel, not as prose. The alternative an
// implementer reaches for first — leaving the fmt.Errorf and having callers
// look for "402" in the string — is exactly what makes the desktop unable to
// tell "not signed in" from "signed in, no access".
func TestFetchMe_AccessNotGrantedSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		// Relay's real body: void-keys answers 402 subscription_required and
		// Relay renames it budget_exceeded on the way out. The name is
		// misleading (it has nothing to do with the monthly budget) and no
		// caller should be reading it.
		_, _ = w.Write([]byte(`{"error":"budget_exceeded"}`))
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "accepted-token", srv.Client())
	if err == nil {
		t.Fatal("FetchMe returned no error for a refused-access response")
	}
	if !errors.Is(err, ErrAccessNotGranted) {
		t.Fatalf("err = %v, want ErrAccessNotGranted", err)
	}
	// The two states must not be the same sentinel wearing two names:
	// cmd/vc/main.go turns ErrNotLoggedIn into "run vc login", which is the
	// wrong instruction for someone whose token was accepted.
	if errors.Is(err, ErrNotLoggedIn) {
		t.Error("refused access also matches ErrNotLoggedIn — the desktop would send a signed-in person back through the login loop")
	}
}

// The sentinel's own text reaches a human through `vc status` and through the
// desktop's error line. It must not repeat the advice that belongs to 401.
func TestAccessNotGrantedSentinelDoesNotAdviseSigningInAgain(t *testing.T) {
	if ErrAccessNotGranted == nil {
		t.Fatal("ErrAccessNotGranted is nil")
	}
	msg := strings.ToLower(ErrAccessNotGranted.Error())
	if strings.TrimSpace(msg) == "" {
		t.Fatal("ErrAccessNotGranted carries no message")
	}
	for _, advice := range []string{"vc login", "log in", "login", "sign in", "signin", "log back"} {
		if strings.Contains(msg, advice) {
			t.Errorf("ErrAccessNotGranted says %q — it tells the human to repeat a sign-in that already succeeded; the missing thing is access, not a session", ErrAccessNotGranted.Error())
			break
		}
	}
	if ErrAccessNotGranted == ErrNotLoggedIn {
		t.Error("ErrAccessNotGranted is ErrNotLoggedIn — one value cannot carry two states")
	}
}

// 402 arrives *before* any identity is confirmed: the body is an error payload,
// not a session. Some deployments do echo a subject back with the refusal —
// lifting it would let the client claim an identity the verifying service never
// vouched for. That is the same failure TestFetchMe_ConflictingIdentityIsRejected
// guards on the happy path, one status code over.
func TestFetchMe_AccessNotGrantedReturnsNoIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"budget_exceeded","userId":"u-1","subject_id":"u-1","email":"person@example.test","pct":12.5,"resetAt":"2026-09-01T00:00:00Z","balanceUsd":3.5}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "accepted-token", srv.Client())
	if !errors.Is(err, ErrAccessNotGranted) {
		t.Fatalf("err = %v, want ErrAccessNotGranted", err)
	}
	if res.UserID != "" || res.Email != "" {
		t.Errorf("MeResult = %+v, want zero identity — the server refused the request, it did not confirm who is asking", res)
	}
	if res.Pct != nil || res.ResetAt != "" || res.BalanceUsd != nil {
		t.Errorf("MeResult = %+v, want zero budget fields — nothing in a refusal payload is a verified budget", res)
	}
}

// The new sentinel must name one specific fact — "the server refused access to
// a token it accepted" — and nothing else. An implementation that returns it
// for every non-200-non-401 answer turns it into a bucket, and the desktop goes
// back to guessing. Transport failure is in the table on purpose: not being
// able to ask is a third outcome, never a refusal.
func TestFetchMe_OnlyPaymentRequiredIsAccessNotGranted(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,          // 400
		http.StatusForbidden,           // 403
		http.StatusNotFound,            // 404
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
	} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()

			_, err := FetchMe(srv.URL, "tok", srv.Client())
			if err == nil {
				t.Fatalf("status %d: expected an error, got nil", status)
			}
			if errors.Is(err, ErrAccessNotGranted) {
				t.Errorf("status %d reported as ErrAccessNotGranted — the server did not refuse access, it answered %d", status, status)
			}
		})
	}

	t.Run("unreachable_server", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		client := srv.Client()
		url := srv.URL
		srv.Close() // nothing is listening: we cannot ask at all

		_, err := FetchMe(url, "tok", client)
		if err == nil {
			t.Fatal("expected an error when the host is unreachable, got nil")
		}
		if errors.Is(err, ErrAccessNotGranted) {
			t.Error("an unreachable host reported as ErrAccessNotGranted — we never got an answer, so nothing was refused")
		}
		if errors.Is(err, ErrNotLoggedIn) {
			t.Error("an unreachable host reported as ErrNotLoggedIn — the token was never judged")
		}
	})
}

// 401 keeps its meaning exactly. Restated here because the change lives one
// line away in the same switch, and this is the assertion an over-eager
// "everything that is not 200 is a payment problem" edit breaks.
func TestFetchMe_UnauthorizedStaysNotLoggedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "bad-token", srv.Client())
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
	if errors.Is(err, ErrAccessNotGranted) {
		t.Error("401 also matches ErrAccessNotGranted — a rejected token is not a granted one waiting for access")
	}
}
