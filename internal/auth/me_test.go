package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchMe_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/vc/me" {
			t.Errorf("path = %s, want /v1/vc/me", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-42",
			"email":       "user@example.com",
			"subDaysLeft": 14,
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "test-token", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "user-42" {
		t.Errorf("UserID = %q", res.UserID)
	}
	if res.Email != "user@example.com" {
		t.Errorf("Email = %q", res.Email)
	}
	// VCD-65: subDaysLeft is a sentinel returned by server but ignored by client.
	// Verify other fields are correctly parsed.
	if res.UserID != "user-42" {
		t.Errorf("UserID = %q, want user-42", res.UserID)
	}
}

// VCD-65: TestFetchMe_Unlimited removed — SubDaysLeft dropped from MeResult.
// The server still returns subDaysLeft=36500 sentinel; the client ignores it.

func TestFetchMe_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "bad-token", srv.Client())
	if !errors.Is(err, ErrNotLoggedIn) {
		t.Fatalf("expected ErrNotLoggedIn, got %v", err)
	}
}

func TestFetchMe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "tok", srv.Client())
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

// VCD-49: budget fields decoded from /v1/vc/me response.

func TestFetchMe_BudgetFields(t *testing.T) {
	// VCD-49 contract (2026-05-30): server returns only { pct, reset_at, status }.
	// Dollar fields (usedUsd, budgetUsd, remainingUsd) are NOT returned for privacy.
	pct := 27.4
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-1",
			"email":       "u@example.com",
			"subDaysLeft": 10,
			"pct":         pct,
			"resetAt":     "2026-06-01T00:00:00.000Z",
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct == nil {
		t.Fatal("Pct must not be nil when returned by server")
	}
	if *res.Pct != pct {
		t.Errorf("Pct = %f, want %f", *res.Pct, pct)
	}
	if res.ResetAt != "2026-06-01T00:00:00.000Z" {
		t.Errorf("ResetAt = %q", res.ResetAt)
	}
}

func TestFetchMe_BudgetFieldsAbsent(t *testing.T) {
	// Older server: no budget fields — must decode without error; budget fields nil.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"userId":      "user-2",
			"email":       "u2@example.com",
			"subDaysLeft": 5,
		})
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct != nil {
		t.Errorf("Pct must be nil when absent from response, got %v", *res.Pct)
	}
	// Dollar fields are not part of MeResult contract (2026-05-30 privacy change).
}

func TestFetchMe_BudgetPctNull(t *testing.T) {
	// Server returns pct:null (no budget set / unlimited) — Pct field must be nil.
	// VCD-49 contract (2026-05-30): only { pct, reset_at } in budget portion.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write raw JSON with explicit null for pct (no budget configured).
		w.Write([]byte(`{"userId":"u3","email":"u3@x.com","subDaysLeft":10,"pct":null,"resetAt":"2026-06-01T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Pct != nil {
		t.Errorf("Pct must be nil when no budget configured, got %v", *res.Pct)
	}
}

// VCD-56: prepaid wallet balance fields.

func TestFetchMe_BalanceUsd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"u9","email":"u9@x.com","subDaysLeft":-1,"pct":null,"balanceUsd":12.4,"resetAt":""}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe error: %v", err)
	}
	if res.BalanceUsd == nil {
		t.Fatalf("BalanceUsd = nil, want 12.4")
	}
	if *res.BalanceUsd != 12.4 {
		t.Errorf("BalanceUsd = %v, want 12.4", *res.BalanceUsd)
	}
}

func TestFetchMe_BalanceUsdAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No balanceUsd field — older server / VCD-55 not yet shipped.
		w.Write([]byte(`{"userId":"u9","email":"u9@x.com","subDaysLeft":10,"pct":null,"resetAt":""}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe error: %v", err)
	}
	if res.BalanceUsd != nil {
		t.Errorf("BalanceUsd = %v, want nil (field absent)", *res.BalanceUsd)
	}
}

// --- Relay migration: subject_id as identity ---------------------------------
//
// Grounding (checked 2026-08-23 in the void-relay clone, not assumed):
//
//   void-relay/src/vc-me.ts:41  →  return Response.json({ subject_id: user.userId });
//   void-relay/src/vc-me.ts:40  →  if (body.subject_id !== user.userId ...) return unavailable();
//
// Two things follow. First, `subject_id` carries the very same value the legacy
// void-auth route calls `userId` — relay refuses the upstream answer when the
// two disagree, so it treats them as one identity. Second, relay's own reply
// body has EXACTLY one field: `subject_id`. `active` / `expires_at` belong to
// the void-keys /v1/entitlements/me shape that relay consumes and does not
// forward, so they are not modelled here (see report).
//
// Chosen seam: no new exported symbol. `subject_id` fills the existing
// MeResult.UserID. A second identity field would force cmd/vc/authcache.go,
// main.go and status_json.go to each decide which of two identities is real.

func TestFetchMe_SubjectIDAloneIsIdentity(t *testing.T) {
	// The literal production shape of relay.makscee.ru/v1/vc/me.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"subject_id":"subj-7"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "subj-7" {
		t.Errorf("UserID = %q, want %q — identity must be taken from subject_id", res.UserID, "subj-7")
	}
	if res.Email != "" {
		t.Errorf("Email = %q, want empty — subject_id is an identity, not an address", res.Email)
	}
}

func TestFetchMe_SubjectIDWithUnmodelledFields(t *testing.T) {
	// Locks tolerance, not a relay contract: whatever else a proxied answer
	// carries (here the entitlement fields relay reads from void-keys), an
	// unmodelled key must never turn a good identity into a decode failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"subject_id":"subj-8","active":true,"expires_at":"2026-09-01T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "subj-8" {
		t.Errorf("UserID = %q, want subj-8", res.UserID)
	}
}

func TestFetchMe_SubjectIDStoredTrimmed(t *testing.T) {
	// The identity is not only tested for emptiness, it is stored, cached
	// (cmd/vc/authcache.go) and compared. Trimming the copy used for the
	// presence check while storing the padded original yields a value that is
	// non-empty and yet equal to no real subject.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"subject_id\":\"  subj-9\\n\"}"))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "subj-9" {
		t.Errorf("UserID = %q, want %q — stored identity must be trimmed", res.UserID, "subj-9")
	}
}

func TestFetchMe_BlankUserIDDoesNotConflictWithSubjectID(t *testing.T) {
	// A blank `userId` is an absent identity, not a competing claim. An
	// implementation that trims for the presence check but compares the raw
	// strings sees two present-and-different values here and reports a
	// conflict; an implementation that trims consistently sees one identity.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"   ","subject_id":"subj-10"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "subj-10" {
		t.Errorf("UserID = %q, want subj-10 — blank userId must not shadow subject_id", res.UserID)
	}
}

func TestFetchMe_IdentityFieldsAgreeAfterTrim(t *testing.T) {
	// Both fields present and equal once trimmed: one identity, no error.
	// Guards the over-strict reading of the conflict rule ("both present =>
	// reject"), and locks that `userId` is stored trimmed on the old path too.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"  same-1  ","subject_id":"same-1","email":"same@example.com"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.UserID != "same-1" {
		t.Errorf("UserID = %q, want %q", res.UserID, "same-1")
	}
	if res.Email != "same@example.com" {
		t.Errorf("Email = %q", res.Email)
	}
}

func TestFetchMe_ConflictingIdentityIsRejected(t *testing.T) {
	// Two identity claims that disagree. Silently preferring one would let the
	// client log in as somebody the checking service did not name — and relay
	// itself refuses exactly this case one hop upstream (vc-me.ts:40) instead
	// of picking a winner. No known server sends both fields, so the only ways
	// to reach this state are a stitched-together proxy or a tampered body;
	// neither is something to guess at.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"userId":"user-a","subject_id":"subj-b","email":"x@example.com"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err == nil {
		t.Fatalf("expected an error for disagreeing userId/subject_id, got identity %q", res.UserID)
	}
	if !strings.Contains(err.Error(), "conflicting identity") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "conflicting identity")
	}
	if errors.Is(err, ErrNotLoggedIn) {
		t.Error("conflicting identity must not surface as ErrNotLoggedIn — a bad body is not an expired session, and cmd/vc/main.go turns ErrNotLoggedIn into `run vc login`")
	}
	if res.UserID != "" || res.Email != "" {
		t.Errorf("result must be empty on conflict, got %+v", res)
	}
}

// --- guards that already hold today; they keep the rules above honest --------

func TestFetchMe_BlankSubjectIDIsMissingIdentity(t *testing.T) {
	// Green before the change (subject_id is simply ignored today). It becomes
	// load-bearing the moment subject_id counts: whitespace must stay absence.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"subject_id":"   ","userId":"","email":"  "}`))
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "tok", srv.Client())
	if err == nil {
		t.Fatal("expected missing identity for whitespace-only subject_id, got nil")
	}
	if !strings.Contains(err.Error(), "missing identity") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "missing identity")
	}
}

func TestFetchMe_NoIdentityFieldsAtAll(t *testing.T) {
	// Guards against dropping the identity requirement while adding subject_id:
	// budget/wallet fields are not an identity.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pct":12.5,"resetAt":"2026-09-01T00:00:00.000Z","balanceUsd":3.5}`))
	}))
	defer srv.Close()

	_, err := FetchMe(srv.URL, "tok", srv.Client())
	if err == nil {
		t.Fatal("expected missing identity when no identity field is present, got nil")
	}
	if !strings.Contains(err.Error(), "missing identity") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "missing identity")
	}
}

func TestFetchMe_EmailOnlyStillAccepted(t *testing.T) {
	// Back-compat guard, green before and after: the legacy route may answer
	// with an address and no id. Identity must not be back-filled from email.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"email":"only@example.com"}`))
	}))
	defer srv.Close()

	res, err := FetchMe(srv.URL, "tok", srv.Client())
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	if res.Email != "only@example.com" {
		t.Errorf("Email = %q", res.Email)
	}
	if res.UserID != "" {
		t.Errorf("UserID = %q, want empty — email is not an id", res.UserID)
	}
}
