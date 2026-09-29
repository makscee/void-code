package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/config"
)

// runStatusJSON resolves auth state the same way runStatus does — auth.Load,
// then auth.FetchMe against cfg.AccessCheckHost — and writes exactly one JSON object
// to out. It builds the object from the raw values, not from status.go's
// lipgloss-rendered strings: those carry ANSI escape codes that a GUI would
// display literally.
func runStatusJSON(cfg config.Config, out io.Writer) error {
	// The profile page (balance, weekly limit, usage), in every state, as
	// `vc status` prints it: the page asks for its own email code.
	obj := map[string]any{"profileUrl": browser.ProfileURL}

	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		obj["authState"] = "signed_out"
		return json.NewEncoder(out).Encode(obj)
	}

	me, err := auth.FetchMe(cfg.AccessCheckHost, strings.TrimSpace(token), &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		// A refused-access answer is its own state: the credential worked, so
		// sending the human back to the sign-in screen is advice that cannot
		// help. The branch is taken on the sentinel, never on the message —
		// a 401 whose text happens to mention the number is still a credential
		// problem, and a refusal whose text never mentions it is still this.
		// Everything else, transport failures included, stays where it was:
		// "we could not get an answer" is not "the answer was no".
		if errors.Is(err, auth.ErrAccessNotGranted) {
			obj["authState"] = "access_not_granted"
			obj["error"] = err.Error()
			return json.NewEncoder(out).Encode(obj)
		}
		obj["authState"] = "invalid_credential"
		obj["error"] = err.Error()
		return json.NewEncoder(out).Encode(obj)
	}

	identity := me.UserID
	if me.Email != "" {
		identity = me.Email
	}

	obj["authState"] = "signed_in"
	obj["identity"] = identity
	// The wallet is only set when the server actually sent a usable one — a
	// zero wallet here would read as "0 ₽" instead of "no wallet information
	// available". The wallet itself carries no percentages; the weekly limit
	// below is the one share the client reports.
	if me.Wallet != nil {
		obj["wallet"] = walletJSONFor(me.Wallet)
	}
	// The weekly limit, as a share only, set like the wallet: only when the
	// server sent one (void-board#224). Independent of the wallet.
	if me.Limit != nil {
		obj["limit"] = limitJSONFor(me.Limit)
	}
	// The wallet line already written, for the desktop to show as is: exactly
	// what formatAccount renders — the words `vc status` prints after
	// "plan:", wallet and weekly limit — or null when there is neither. The display rules live
	// here, in Go, once; the desktop never re-implements them.
	obj["walletText"] = nil
	if text := formatAccount(me, time.Now()); text != "" {
		obj["walletText"] = text
	}
	// The same notice a launch hands to Pi, for the desktop to show: a string,
	// or null when the wallet and the limit have nothing to say. Only a signed-in answer
	// carries one — a refusal names no wallet to warn about.
	obj["launchNotice"] = nil
	if notice := launchNotice(me, time.Now()); notice != "" {
		obj["launchNotice"] = notice
	}
	return json.NewEncoder(out).Encode(obj)
}

// walletJSON mirrors the server's "wallet" object under the server's names,
// tier unformatted, money in kopecks only: an older Relay's dollars never
// leave vc (void-board#224), so its wallet reads balanceKopecks null. No
// omitempty: todayPaid false and fundedDays 0 are the values that matter
// most, and null is how "no tariff" and "no paid time" read.
type walletJSON struct {
	BalanceKopecks *int64      `json:"balanceKopecks"`
	PaidUntil      *string     `json:"paidUntil"`
	Tariff         *tariffJSON `json:"tariff"`
	TodayPaid      *bool       `json:"todayPaid"`
	FundedDays     *int        `json:"fundedDays"`
}

// The prices read null when the server sent none (void-board#224).
type tariffJSON struct {
	Tier             string `json:"tier"`
	WeekPriceKopecks *int64 `json:"weekPriceKopecks"`
	PackPriceKopecks *int64 `json:"packPriceKopecks"`
}

func walletJSONFor(w *auth.Wallet) walletJSON {
	out := walletJSON{BalanceKopecks: w.BalanceKopecks, TodayPaid: w.TodayPaid, FundedDays: w.FundedDays}
	if w.PaidUntil != nil {
		s := w.PaidUntil.UTC().Format(time.RFC3339)
		out.PaidUntil = &s
	}
	if w.Tariff != nil {
		out.Tariff = &tariffJSON{Tier: w.Tariff.Tier, WeekPriceKopecks: w.Tariff.WeekPriceKopecks, PackPriceKopecks: w.Tariff.PackPriceKopecks}
	}
	return out
}

// limitJSON is the server's "limit" object as vc read it: pct as sent,
// resetAt as RFC 3339 or null when absent or unreadable.
type limitJSON struct {
	Pct     float64 `json:"pct"`
	ResetAt *string `json:"resetAt"`
}

func limitJSONFor(l *auth.Limit) limitJSON {
	out := limitJSON{Pct: l.Pct}
	if l.ResetAt != nil {
		s := l.ResetAt.UTC().Format(time.RFC3339)
		out.ResetAt = &s
	}
	return out
}
