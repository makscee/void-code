package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
)

// The client shows roubles and the paid-until date (void-board#234), never
// dollars (void-board#224) and never a percentage
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

// formatWallet renders a wallet as `2 000 ₽ · T1 · до 4 окт`: the rouble
// balance, the plan and the end of the paid time (void-board#234). Each part
// shows only when the server sent it; the date only while it lies ahead. An
// older Relay sends no kopecks: its wallet keeps the #224 line, the plan and
// the days left (`T1 · осталось ~9 дней`), and never shows its dollars. The
// line is Russian throughout. No
// tariff and no roubles renders as "", as does an absent wallet.
func formatWallet(w *auth.Wallet, now time.Time) string {
	if w == nil {
		return ""
	}
	var parts []string
	if w.BalanceKopecks != nil {
		parts = append(parts, formatRoubles(*w.BalanceKopecks))
	}
	if w.Tariff != nil {
		parts = append(parts, strings.ToUpper(w.Tariff.Tier))
	}
	switch {
	case w.BalanceKopecks != nil:
		if w.PaidUntil != nil && w.PaidUntil.After(now) {
			parts = append(parts, "до "+formatRuDate(*w.PaidUntil, now))
		}
	case w.Tariff != nil && w.FundedDays != nil:
		parts = append(parts, "осталось ~"+ruPlural(max(*w.FundedDays, 0), "день", "дня", "дней"))
	}
	return strings.Join(parts, " · ")
}

// formatRoubles spells kopecks as `2 000 ₽`, or `1 837,12 ₽` when there are
// kopecks: thousands split by a space, a comma before the kopecks.
func formatRoubles(kopecks int64) string {
	sign := ""
	if kopecks < 0 {
		sign, kopecks = "-", -kopecks
	}
	digits := fmt.Sprint(kopecks / 100)
	var grouped strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteRune(d)
	}
	text := sign + grouped.String()
	if k := kopecks % 100; k != 0 {
		text += fmt.Sprintf(",%02d", k)
	}
	return text + " ₽"
}

var ruMonths = [...]string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

// formatRuDate spells a date as `4 окт` in this machine's time zone, with the
// year only when it is not this year's (`4 янв 2027`).
func formatRuDate(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	text := fmt.Sprintf("%d %s", t.Day(), ruMonths[t.Month()-1])
	if t.Year() != now.Year() {
		text += fmt.Sprintf(" %d", t.Year())
	}
	return text
}

// daysLeft spells a day count for a launch notice. Days never go below zero on screen: a negative
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
	} else if w.Tariff != nil && w.Tariff.DailyRateUsd != nil && w.TodayPaid != nil && !*w.TodayPaid && w.BalanceUsd != nil && *w.BalanceUsd < *w.Tariff.DailyRateUsd {
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
