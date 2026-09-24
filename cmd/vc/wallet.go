package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/makscee/void-code/internal/auth"
)

// The client shows money and days, never a percentage (spec
// 2026-09-23-client-wallet-days, "Клиент"). Everything the wallet makes the
// client say is decided here, so `vc status`, the welcome screen and both
// launch paths cannot drift apart.
//
// The client never refuses a launch over the wallet (amendment "после панели
// void-code#76"): only Relay refuses, with 402 wallet_daily_charge_required
// under its BUDGET_ENFORCE switch, and Pi shows that refusal itself. A
// verdict taken here from a snapshot would ignore the switch — and on the
// desktop it would hide behind "Chat stopped… check your network".

// walletRefusalMessage is the same sentence Relay sends with its 402
// wallet_daily_charge_required.
const walletRefusalMessage = "Balance is not enough for today — message @makscee on Telegram to top up."

// walletLowDays is the fundedDays at or below which a launch is warned.
const walletLowDays = 2

// piLaunchNoticeEnv carries the launch notice from vc to the managed Pi
// extension, which shows it on session_start. vc does not print it itself:
// Pi's fullscreen mode clears whatever was on the terminal before it.
const piLaunchNoticeEnv = "VC_LAUNCH_NOTICE"

// formatWallet renders a wallet as `$18.00 · T1 · ~9 days left`, or just the
// balance when no tariff is assigned (no rate, so no days to count). An absent
// wallet renders as "" — nothing to show, never a made-up $0.00.
func formatWallet(w *auth.Wallet) string {
	if w == nil {
		return ""
	}
	parts := []string{formatUSD(w.BalanceUsd)}
	if w.Tariff != nil {
		parts = append(parts, strings.ToUpper(w.Tariff.Tier))
		if w.FundedDays != nil {
			parts = append(parts, "~"+daysLeft(*w.FundedDays))
		}
	}
	return strings.Join(parts, " · ")
}

// formatUSD renders dollars as `$18.00`, and a debt as `-$3.00` — the sign
// before the dollar. The amount is floored to the cent: rounding up would show
// money that is not there.
func formatUSD(v float64) string {
	cents := math.Floor(v * 100)
	// v*100 can land a hair under the cent v really is — 1.15*100 is
	// 114.99999999999999 — and flooring that takes a real cent away. The
	// server sends cents; when the next cent up is exactly v, it is v's cent.
	if (cents+1)/100 == v {
		cents++
	}
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := int64(cents)
	return fmt.Sprintf("%s$%d.%02d", sign, whole/100, whole%100)
}

// daysLeft spells a day count. Days never go below zero on screen: a negative
// fundedDays is a balance already behind, which reads as none left.
func daysLeft(n int) string {
	n = max(n, 0)
	if n == 1 {
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", n)
}

// walletLaunchNotice is what a launch tells the person about the wallet, or
// "" for nothing. It never stops the launch.
//
//   - no wallet, or no tariff → nothing (no daily charge to be behind on)
//   - todayPaid === false and balance < daily rate → Relay's own refusal
//     sentence, as advance notice of the 402 the first request will get
//   - otherwise fundedDays <= 2 → the low-balance notice
//
// An unpaid day the balance still covers is not a refusal: right after
// 00:00 UTC the daily charge may simply not have run yet. todayPaid == nil is
// not false.
func walletLaunchNotice(w *auth.Wallet) string {
	if w == nil || w.Tariff == nil {
		return ""
	}
	if w.TodayPaid != nil && !*w.TodayPaid && w.BalanceUsd < w.Tariff.DailyRateUsd {
		return walletRefusalMessage
	}
	if w.FundedDays != nil && *w.FundedDays <= walletLowDays {
		return "Balance low — " + daysLeft(*w.FundedDays) + ". Message @makscee on Telegram to top up."
	}
	return ""
}

// withLaunchNotice hands the notice to Pi in its environment. env must already
// be free of an inherited VC_LAUNCH_NOTICE (buildPiSpawnEnv strips it), so
// the one Pi sees is always the one this launch computed — or none.
func withLaunchNotice(env []string, notice string) []string {
	if notice == "" {
		return env
	}
	return append(env, piLaunchNoticeEnv+"="+notice)
}
