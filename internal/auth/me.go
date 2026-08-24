package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MeResult holds the identity + budget state returned by GET /v1/vc/me.
// VCD-65: SubDaysLeft removed — subscriptionGate no longer exists; the server
// still returns subDaysLeft (sentinel 36500) for old client back-compat but the
// new client ignores it. budgetGate is the sole client-side gate.
type MeResult struct {
	UserID string
	Email  string

	// VCD-49 budget fields — nil when the server does not return budget data
	// (older void-auth or budget not configured). Never block on nil values.
	// Contract (2026-05-30): server returns only { pct, reset_at } — dollar fields
	// (UsedUsd, BudgetUsd, RemainingUsd) are NOT exposed to vc for privacy.
	Pct     *float64 // used/budget*100; nil when budget=0 (no cap) or no budget set
	ResetAt string   // ISO-8601 first day of next calendar month, UTC

	// VCD-55: prepaid wallet balance (USD remaining, 2-decimal). nil when the
	// server does not return it (older void-auth / VCD-55 not yet deployed) →
	// callers degrade gracefully (no balance shown).
	BalanceUsd *float64
}

// FetchMe calls GET <authHost>/v1/vc/me with the supplied bearer token.
// Returns the identity + subscription state.
func FetchMe(authHost, token string, httpClient *http.Client) (MeResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, authHost+"/v1/vc/me", nil)
	if err != nil {
		return MeResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return MeResult{}, fmt.Errorf("GET vc/me: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return MeResult{}, ErrNotLoggedIn
	}
	// 402 means the opposite of 401: the token was accepted and the session
	// verified, and the refusal is about access to the subject behind it. The
	// body is a refusal payload, not a session — even when a deployment echoes
	// a subject back, it is not an identity this service vouched for, so
	// nothing is read out of it.
	if resp.StatusCode == http.StatusPaymentRequired {
		return MeResult{}, ErrAccessNotGranted
	}
	if resp.StatusCode != http.StatusOK {
		return MeResult{}, fmt.Errorf("vc/me returned status %d", resp.StatusCode)
	}

	var r struct {
		UserID string `json:"userId"`
		// Relay's /v1/vc/me names the same value subject_id (void-relay
		// src/vc-me.ts compares body.subject_id against user.userId), so both
		// spellings are accepted as identity.
		SubjectID string `json:"subject_id"`
		Email     string `json:"email"`
		// subDaysLeft intentionally ignored — VCD-65: sentinel from server, no gate.
		Pct        *float64 `json:"pct"`
		ResetAt    string   `json:"resetAt"`
		BalanceUsd *float64 `json:"balanceUsd"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return MeResult{}, fmt.Errorf("decoding vc/me response: %w", err)
	}

	// Trim once, up front: the trimmed values are both what gets checked and
	// what gets returned, so no caller sees a value the checks did not see.
	userID := strings.TrimSpace(r.UserID)
	subjectID := strings.TrimSpace(r.SubjectID)
	email := strings.TrimSpace(r.Email)

	// Two identities that disagree are a contradiction, not a choice: picking a
	// winner would log the user in as somebody the verifying service did not name.
	if userID != "" && subjectID != "" && userID != subjectID {
		return MeResult{}, fmt.Errorf("decoding vc/me response: conflicting identity: userId %q != subject_id %q", userID, subjectID)
	}
	identity := userID
	if identity == "" {
		identity = subjectID
	}
	if identity == "" && email == "" {
		return MeResult{}, fmt.Errorf("decoding vc/me response: missing identity")
	}
	return MeResult{
		UserID:     identity,
		Email:      email,
		Pct:        r.Pct,
		ResetAt:    r.ResetAt,
		BalanceUsd: r.BalanceUsd,
	}, nil
}
