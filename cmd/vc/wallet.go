package main

import (
	"fmt"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
)

// The client shows days, never money (void-board#224) and never a percentage
// of the wallet (spec 2026-09-23-client-wallet-days, "Клиент"). Everything the wallet makes the
// client say is decided here, so `vc status`, the welcome screen and both
// launch paths cannot drift apart.
//
// The client never refuses a launch over the wallet (amendment "после панели
// void-code#76"): only Relay refuses, with 402 wallet_charge_required (weekly,
// on Keys' chargeRequired) or wallet_daily_charge_required (an older Keys)
// under its BUDGET_ENFORCE switch, and Pi shows that refusal itself. A
// verdict taken here from a snapshot would ignore the switch — and on the
// desktop it would hide behind "Chat stopped… check your network".

// walletRefusalMessage is the same sentence Relay sends with its 402
// wallet_daily_charge_required.
const walletRefusalMessage = "Balance is not enough for today — top up: " + browser.PayURL

// walletWeekRefusalMessage is the same sentence Relay sends with its 402
// wallet_charge_required (spec, "Недельное списание (решение 25.09)").
const walletWeekRefusalMessage = "Balance is not enough for this week — top up: " + browser.PayURL

// walletLowDays is the fundedDays at or below which a launch is warned.
const walletLowDays = 2

// piLaunchNoticeEnv carries the launch notice from vc to the managed Pi
// extension, which shows it on session_start. vc does not print it itself:
// Pi's fullscreen mode clears whatever was on the terminal before it.
const piLaunchNoticeEnv = "VC_LAUNCH_NOTICE"

// formatWallet renders a wallet as `T1 · ~9 days left`: the plan and the days
// the balance covers, never money (void-board#224: no $ anywhere; the rouble
// balance comes with void-board#225). No tariff means no rate and no days to
// count, so nothing to show; an absent wallet renders as "" too.
func formatWallet(w *auth.Wallet) string {
	if w == nil || w.Tariff == nil {
		return ""
	}
	parts := []string{strings.ToUpper(w.Tariff.Tier)}
	if w.FundedDays != nil {
		parts = append(parts, "~"+daysLeft(*w.FundedDays))
	}
	return strings.Join(parts, " · ")
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
//   - no wallet → nothing
//   - chargeRequired present → it alone decides the refusal: true gives
//     Relay's weekly sentence, false gives none — whatever todayPaid, the
//     daily rate and fundedDays say. Relay refuses on the same verdict, so
//     the notice cannot disagree with the 402 it announces.
//   - chargeRequired absent (an older Keys) → the daily rule: with a tariff
//     that names its daily rate, todayPaid === false and balance < daily
//     rate → Relay's daily sentence
//   - no refusal, a tariff and fundedDays <= 2 → the low-balance notice
//
// Under the daily rule an unpaid day the balance still covers is not a
// refusal: right after 00:00 UTC the daily charge may simply not have run
// yet. todayPaid == nil is not false, and chargeRequired == nil is not false.
func walletLaunchNotice(w *auth.Wallet) string {
	if w == nil {
		return ""
	}
	if w.ChargeRequired != nil {
		if *w.ChargeRequired {
			return walletWeekRefusalMessage
		}
	} else if w.Tariff != nil && w.Tariff.DailyRateUsd != nil && w.TodayPaid != nil && !*w.TodayPaid && w.BalanceUsd < *w.Tariff.DailyRateUsd {
		return walletRefusalMessage
	}
	if w.Tariff != nil && w.FundedDays != nil && *w.FundedDays <= walletLowDays {
		return "Balance low — " + daysLeft(*w.FundedDays) + ". Top up: " + browser.PayURL
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
