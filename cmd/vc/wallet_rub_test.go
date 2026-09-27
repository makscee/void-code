package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// The rouble wallet (void-board#234, mission #225). Since void-relay#28,
// /v1/vc/me sends
//
//	"wallet": { "balanceKopecks", "paidUntil", "tariff": { "tier", "weekPriceKopecks", "packPriceKopecks" } | null,
//	            "todayPaid", "fundedDays", "chargeRequired", "periodEndsAt" }
//
// with no dollar field. The rules pinned here:
//
//  1. The wallet line is `2 000 ₽ · T1 · до 4 окт`, then the weekly limit:
//     `2 000 ₽ · T1 · до 4 окт · лимит использован на 37%, сброс через 3 дня`.
//  2. The date shows only while it lies ahead; kopecks show only when there
//     are any (`1 837,12 ₽`).
//  3. An older Relay without kopecks keeps the #224 line (`T1 · осталось ~9 дней`):
//     no money shown, never $.
//  4. No dollar leaves vc: not on screen, not in --json.

// rubMonths is spelled here on purpose, apart from wallet.go's table.
var rubMonths = []string{"янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

func rubWallet(kopecks, paidUntil, tariff string) string {
	return `"wallet":{"balanceKopecks":` + kopecks + `,"paidUntil":` + paidUntil + `,"tariff":` + tariff +
		`,"todayPaid":true,"fundedDays":9,"chargeRequired":false,"periodEndsAt":null}`
}

const rubTariffT1 = `{"tier":"t1","weekPriceKopecks":150000,"packPriceKopecks":500000}`

func TestFormatRoubles(t *testing.T) {
	for _, tc := range []struct {
		kopecks int64
		want    string
	}{
		{0, "0 ₽"},
		{5, "0,05 ₽"},
		{99900, "999 ₽"},
		{200000, "2 000 ₽"},
		{183712, "1 837,12 ₽"},
		{500055, "5 000,55 ₽"},
		{123456789, "1 234 567,89 ₽"},
		{-150000, "-1 500 ₽"},
	} {
		if got := formatRoubles(tc.kopecks); got != tc.want {
			t.Errorf("formatRoubles(%d) = %q, want %q", tc.kopecks, got, tc.want)
		}
	}
}

func TestFormatWalletRoubles(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)
	at := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 18, 0, 0, 0, time.Local)
		return &v
	}
	k := func(v int64) *int64 { return &v }
	f := func(v float64) *float64 { return &v }
	days := func(v int) *int { return &v }
	t1 := &auth.Tariff{Tier: "t1"}
	for _, tc := range []struct {
		name string
		w    *auth.Wallet
		want string
	}{
		{"balance, tier, paid until", &auth.Wallet{BalanceKopecks: k(200000), PaidUntil: at(2026, 10, 4), Tariff: t1, FundedDays: days(9)}, "2 000 ₽ · T1 · до 4 окт"},
		{"may is genitive", &auth.Wallet{BalanceKopecks: k(0), PaidUntil: at(2027, 5, 1), Tariff: t1}, "0 ₽ · T1 · до 1 мая 2027"},
		{"next year names the year", &auth.Wallet{BalanceKopecks: k(150000), PaidUntil: at(2027, 1, 4), Tariff: t1}, "1 500 ₽ · T1 · до 4 янв 2027"},
		{"no paid time", &auth.Wallet{BalanceKopecks: k(50000), Tariff: t1, FundedDays: days(2)}, "500 ₽ · T1"},
		{"paid time already over", &auth.Wallet{BalanceKopecks: k(50000), PaidUntil: at(2026, 9, 20), Tariff: t1}, "500 ₽ · T1"},
		{"no tariff: the money alone", &auth.Wallet{BalanceKopecks: k(200000)}, "2 000 ₽"},
		{"older relay: days, no money", &auth.Wallet{BalanceUsd: f(18), Tariff: t1, FundedDays: days(9)}, "T1 · осталось ~9 дней"},
		{"older relay, no tariff: nothing", &auth.Wallet{BalanceUsd: f(18)}, ""},
		{"no wallet", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatWallet(tc.w, now); got != tc.want {
				t.Errorf("formatWallet = %q, want %q", got, tc.want)
			}
		})
	}
}

// void-relay#28's own body, end to end through `vc status`: the line from the
// card, `2 000 ₽ · T1 · до 4 окт · лимит использован на 37%, сброс через 3 дня`.
func TestStatusShowsRoubleWalletAndLimit(t *testing.T) {
	paid := time.Now().Add(7 * 24 * time.Hour)
	paid = time.Date(paid.Year(), paid.Month(), paid.Day(), 12, 0, 0, 0, time.Local)
	reset := time.Now().Add(3*24*time.Hour + time.Hour)
	body := meBody(rubWallet("200000", `"`+paid.UTC().Format(time.RFC3339)+`"`, rubTariffT1) +
		`,"limit":{"pct":37.4,"resetAt":"` + reset.UTC().Format(time.RFC3339) + `"}`)

	date := fmt.Sprintf("%d %s", paid.Day(), rubMonths[paid.Month()-1])
	if paid.Year() != time.Now().Year() {
		date += fmt.Sprintf(" %d", paid.Year())
	}
	want := "plan: 2 000 ₽ · T1 · до " + date + " · лимит использован на 37%, сброс через 3 дня"

	out := humanStatus(t, body)
	got, ok := statusLine(out, "plan:")
	if !ok {
		t.Fatalf("vc status has no plan line:\n%s", out)
	}
	if got != want {
		t.Errorf("plan line = %q, want %q", got, want)
	}
	if strings.Contains(out, "$") {
		t.Errorf("vc status shows dollars:\n%s", out)
	}

	obj := jsonStatus(t, body)
	if obj["walletText"] != strings.TrimPrefix(want, "plan: ") {
		t.Errorf("walletText = %v, want %q", obj["walletText"], strings.TrimPrefix(want, "plan: "))
	}
	w := jsonWallet(t, obj)
	if w["balanceKopecks"] != 200000.0 || w["paidUntil"] != paid.UTC().Format(time.RFC3339) {
		t.Errorf("wallet = %v, want balanceKopecks 200000 and paidUntil %s", w, paid.UTC().Format(time.RFC3339))
	}
	if tariff, _ := w["tariff"].(map[string]any); tariff == nil || tariff["tier"] != "t1" || tariff["weekPriceKopecks"] != 150000.0 || tariff["packPriceKopecks"] != 500000.0 {
		t.Errorf("wallet.tariff = %v, want t1 at 150000 / 500000 kopecks", w["tariff"])
	}
	assertNoDollarField(t, obj)
}

// Relay sends chargeRequired next to the kopecks; the notice follows it, as
// before, with no dollar rule to fall back on.
func TestRoubleWalletLaunchNotice(t *testing.T) {
	charge := strings.Replace(rubWallet("50000", "null", rubTariffT1), `"chargeRequired":false`, `"chargeRequired":true`, 1)
	if got := jsonStatus(t, meBody(charge))["launchNotice"]; got != walletWeekRefusalMessage {
		t.Errorf("launchNotice = %v, want %q", got, walletWeekRefusalMessage)
	}
	if got := jsonStatus(t, meBody(rubWallet("200000", "null", rubTariffT1)))["launchNotice"]; got != nil {
		t.Errorf("launchNotice = %v, want none for a paid, funded wallet", got)
	}
}

// The whole status line is Russian (Maks, 09-27): Russian plurals for the
// days left and the time to the limit's reset, and no English word in it.
func TestStatusLineRussian(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0 дней"}, {1, "1 день"}, {2, "2 дня"}, {4, "4 дня"}, {5, "5 дней"}, {11, "11 дней"}, {12, "12 дней"}, {14, "14 дней"}, {21, "21 день"}, {22, "22 дня"}, {111, "111 дней"}} {
		if got := ruPlural(tc.n, "день", "дня", "дней"); got != tc.want {
			t.Errorf("ruPlural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{0, "скоро"}, {-time.Minute, "скоро"}, {30 * time.Minute, "через 1 час"}, {3 * time.Hour, "через 3 часа"}, {5 * time.Hour, "через 5 часов"}, {25 * time.Hour, "через 1 день"}, {3 * 24 * time.Hour, "через 3 дня"}} {
		if got := ruResetsIn(tc.d); got != tc.want {
			t.Errorf("ruResetsIn(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	reset, paidUntil := now.Add(3*24*time.Hour), now.Add(6*24*time.Hour)
	kopecks, days := int64(200000), 9
	for _, me := range []auth.MeResult{
		{Wallet: &auth.Wallet{BalanceKopecks: &kopecks, PaidUntil: &paidUntil, Tariff: &auth.Tariff{Tier: "t1"}}, Limit: &auth.Limit{Pct: 37, ResetAt: &reset}},
		{Wallet: &auth.Wallet{Tariff: &auth.Tariff{Tier: "t1"}, FundedDays: &days}, Limit: &auth.Limit{Pct: 85}},
	} {
		line := formatAccount(me, now)
		t.Log(line)
		for _, english := range []string{"limit", "used", "resets", "days", "left", "soon"} {
			if strings.Contains(line, english) {
				t.Errorf("status line %q has the English %q", line, english)
			}
		}
	}
}
