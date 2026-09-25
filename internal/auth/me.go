package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// MeResult holds the identity + wallet state returned by GET /v1/vc/me.
// VCD-65: SubDaysLeft removed — subscriptionGate no longer exists; the server
// still returns subDaysLeft (sentinel 36500) for old client back-compat but the
// new client ignores it.
//
// The VCD-49 budget (pct/resetAt) and the VCD-55 top-level balanceUsd are no
// longer read: the client shows money and days, never a percentage (spec
// 2026-09-23-client-wallet-days). MeResult stays comparable — pointers only,
// no slices or maps — so callers can check it against its zero value.
type MeResult struct {
	UserID string
	Email  string

	// Wallet is nil when the server sent no usable wallet: absent, null, or
	// any field of the wrong type. Never block on a nil wallet.
	Wallet *Wallet
}

// Wallet is the prepaid balance and the tariff that draws on it, as Relay
// reports it under "wallet" on /v1/vc/me.
type Wallet struct {
	BalanceUsd float64
	Tariff     *Tariff // nil: no tariff assigned
	// TodayPaid is a pointer on purpose: false (today's charge has not been
	// taken) and nil (no tariff, nothing to charge) lead to different launch
	// decisions and must not collapse into one zero value.
	TodayPaid  *bool
	FundedDays *int // days the balance covers after today; nil: no tariff
	// ChargeRequired is Keys' weekly verdict: a tariff, no paid period, and a
	// balance short of the week. A pointer like TodayPaid: false ("no charge
	// required, whatever the daily fields say") and nil ("the server did not
	// say — an older Keys; the old daily rule applies") must stay apart.
	ChargeRequired *bool
	PeriodEndsAt   *string // end of the paid period, as sent; nil: absent or null
}

// Tariff is the plan a wallet is charged by.
type Tariff struct {
	Tier            string // as sent by the server ("t1"); display formatting is the printer's
	MonthlyPriceUsd float64
	DailyRateUsd    float64
	WeeklyPriceUsd  *float64 // nil: an older server that sends no weekly price
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
		// Kept raw so that a wallet the client cannot read costs the wallet,
		// not the sign-in: see parseWallet.
		Wallet json.RawMessage `json:"wallet"`
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
		UserID: identity,
		Email:  email,
		Wallet: parseWallet(r.Wallet),
	}, nil
}

// parseWallet reads the "wallet" object strictly and all-or-nothing: a value
// of the wrong type anywhere in it ("18" for a number, "false" for a bool, a
// string for the tariff) yields nil rather than a half-read wallet, because a
// guessed field could refuse a launch or print a wrong balance. Unknown keys
// are ignored — the wallet will grow before every client is updated.
func parseWallet(raw json.RawMessage) *Wallet {
	if len(raw) == 0 {
		return nil
	}
	var w struct {
		BalanceUsd *float64        `json:"balanceUsd"`
		Tariff     json.RawMessage `json:"tariff"`
		TodayPaid  *bool           `json:"todayPaid"`
		FundedDays *int            `json:"fundedDays"`
		// Optional (weekly charging): absent or null reads nil, a wrong
		// type fails the decode and drops the wallet like any other field.
		ChargeRequired *bool   `json:"chargeRequired"`
		PeriodEndsAt   *string `json:"periodEndsAt"`
	}
	// null decodes into the zero struct without error and is then rejected
	// for its missing balance, like any other wallet without one.
	if err := json.Unmarshal(raw, &w); err != nil || w.BalanceUsd == nil {
		return nil
	}
	tariff, ok := parseTariff(w.Tariff)
	if !ok {
		return nil
	}
	return &Wallet{
		BalanceUsd:     *w.BalanceUsd,
		Tariff:         tariff,
		TodayPaid:      w.TodayPaid,
		FundedDays:     w.FundedDays,
		ChargeRequired: w.ChargeRequired,
		PeriodEndsAt:   w.PeriodEndsAt,
	}
}

// parseTariff returns (nil, true) for an absent or null tariff and
// (nil, false) for one that is present but unreadable.
func parseTariff(raw json.RawMessage) (*Tariff, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var t struct {
		Tier            *string  `json:"tier"`
		MonthlyPriceUsd *float64 `json:"monthlyPriceUsd"`
		DailyRateUsd    *float64 `json:"dailyRateUsd"`
		WeeklyPriceUsd  *float64 `json:"weeklyPriceUsd"` // optional; a wrong type fails the decode
	}
	if err := json.Unmarshal(raw, &t); err != nil || t.Tier == nil || *t.Tier == "" || t.MonthlyPriceUsd == nil || t.DailyRateUsd == nil {
		return nil, false
	}
	return &Tariff{Tier: *t.Tier, MonthlyPriceUsd: *t.MonthlyPriceUsd, DailyRateUsd: *t.DailyRateUsd, WeeklyPriceUsd: t.WeeklyPriceUsd}, true
}
