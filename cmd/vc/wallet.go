package main

import (
	"fmt"
	"strings"

	"github.com/makscee/void-code/internal/auth"
)

// The client shows money and days, never a percentage (spec
// 2026-09-23-client-wallet-days, "Клиент"). Everything the wallet makes the
// client say or do is decided here, so `vc status`, the welcome screen and both
// launch paths cannot drift apart.

// walletRefusalMessage is the same sentence Relay sends with its 402
// wallet_daily_charge_required.
const walletRefusalMessage = "Balance is not enough for today — message @makscee on Telegram to top up."

// walletLowDays is the fundedDays at or below which a launch is warned.
const walletLowDays = 2

// formatWallet renders a wallet as `$18.00 · T1 · ~9 days left`, or just the
// balance when no tariff is assigned (no rate, so no days to count). An absent
// wallet renders as "" — nothing to show, never a made-up $0.00.
func formatWallet(w *auth.Wallet) string {
	if w == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("$%.2f", w.BalanceUsd)}
	if w.Tariff != nil {
		parts = append(parts, strings.ToUpper(w.Tariff.Tier))
		if w.FundedDays != nil {
			parts = append(parts, "~"+daysLeft(*w.FundedDays))
		}
	}
	return strings.Join(parts, " · ")
}

func daysLeft(n int) string {
	if n == 1 {
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", n)
}

// walletDecision is the launch verdict on the wallet /v1/vc/me reported.
type walletDecision struct {
	Block   bool   // do NOT start Pi; show Message; fail
	Warn    bool   // start Pi, but show Message first
	Message string // plain text; the terminal caller adds styling
}

// walletGate decides a launch from the wallet alone.
//
//   - no wallet, or no tariff → nothing to say (no daily charge to be behind on)
//   - todayPaid === false and balance < daily rate → refuse: today cannot be paid
//   - fundedDays <= 2 → warn, and start anyway
//
// An unpaid day the balance still covers is not refused: right after 00:00 UTC
// the daily charge may simply not have run yet. todayPaid == nil is not false.
func walletGate(w *auth.Wallet) walletDecision {
	if w == nil || w.Tariff == nil {
		return walletDecision{}
	}
	if w.TodayPaid != nil && !*w.TodayPaid && w.BalanceUsd < w.Tariff.DailyRateUsd {
		return walletDecision{Block: true, Message: walletRefusalMessage}
	}
	if w.FundedDays != nil && *w.FundedDays <= walletLowDays {
		return walletDecision{Warn: true, Message: "Balance low — " + daysLeft(*w.FundedDays) + ". Message @makscee on Telegram to top up."}
	}
	return walletDecision{}
}
