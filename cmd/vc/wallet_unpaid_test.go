package main

import (
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// A tier with no paid week (void-board#373). After the 09-29 switch to paid
// tiers most accounts look like the e2e's: /v1/vc/me sends
//
//	"wallet": {"balanceKopecks":0,"paidUntil":null,"tariff":{"tier":"t1",…},
//	           "todayPaid":false,"fundedDays":0,"chargeRequired":true,"periodEndsAt":null}
//
// and vc said «0 ₽ · T1 · лимит использован на 0%, сброс через 6 дней», a
// plan that reads as working. The line now says it is not paid and gives the
// pay link in place of the limit; Relay refuses on the same chargeRequired.

func unpaidWallet(paidUntil string) string {
	return `"wallet":{"balanceKopecks":0,"paidUntil":` + paidUntil + `,"tariff":` + rubTariffT1 +
		`,"todayPaid":false,"fundedDays":0,"chargeRequired":true,"periodEndsAt":null}`
}

func TestStatusSaysUnpaidWithPayLink(t *testing.T) {
	reset := time.Now().Add(6*24*time.Hour + time.Hour)
	body := meBody(unpaidWallet("null") + `,"limit":{"pct":0,"resetAt":"` + reset.UTC().Format(time.RFC3339) + `"}`)
	const want = "plan: 0 ₽ · T1 · не оплачено — оплатить: https://profile.makscee.ru/vc/pay"

	out := humanStatus(t, body)
	got, ok := statusLine(out, "plan:")
	if !ok {
		t.Fatalf("vc status has no plan line:\n%s", out)
	}
	if got != want {
		t.Errorf("plan line = %q, want %q", got, want)
	}
	if obj := jsonStatus(t, body); obj["walletText"] != strings.TrimPrefix(want, "plan: ") {
		t.Errorf("walletText = %v, want %q — the desktop and the welcome screen show the same words", obj["walletText"], strings.TrimPrefix(want, "plan: "))
	}
}

func TestFormatAccountUnpaid(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	yes, no := true, false
	zero := int64(0)
	days := 0
	past, ahead := now.Add(-time.Hour), now.Add(6*24*time.Hour)
	reset := now.Add(6*24*time.Hour + time.Hour)
	limit := &auth.Limit{Pct: 0, ResetAt: &reset}
	t1 := &auth.Tariff{Tier: "t1"}
	for _, tc := range []struct {
		name string
		w    *auth.Wallet
		want string
	}{
		{"charge required, never paid", &auth.Wallet{BalanceKopecks: &zero, Tariff: t1, ChargeRequired: &yes}, "0 ₽ · T1 · не оплачено — оплатить: https://profile.makscee.ru/vc/pay"},
		{"charge required, paid week over", &auth.Wallet{BalanceKopecks: &zero, PaidUntil: &past, Tariff: t1, ChargeRequired: &yes}, "0 ₽ · T1 · не оплачено — оплатить: https://profile.makscee.ru/vc/pay"},
		{"paid week running", &auth.Wallet{BalanceKopecks: &zero, PaidUntil: &ahead, Tariff: t1, ChargeRequired: &no}, "0 ₽ · T1 · до 5 окт · лимит использован на 0%, сброс через 6 дней"},
		{"no charge required", &auth.Wallet{BalanceKopecks: &zero, Tariff: t1, ChargeRequired: &no}, "0 ₽ · T1 · лимит использован на 0%, сброс через 6 дней"},
		{"older Keys: no verdict, no guess", &auth.Wallet{BalanceKopecks: &zero, Tariff: t1}, "0 ₽ · T1 · лимит использован на 0%, сброс через 6 дней"},
		{"no tariff", &auth.Wallet{BalanceKopecks: &zero, ChargeRequired: &yes}, "0 ₽ · лимит использован на 0%, сброс через 6 дней"},
		{"older dollar relay keeps its line", &auth.Wallet{Tariff: t1, FundedDays: &days, ChargeRequired: &yes}, "T1 · осталось ~0 дней · лимит использован на 0%, сброс через 6 дней"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatAccount(auth.MeResult{Wallet: tc.w, Limit: limit}, now); got != tc.want {
				t.Errorf("formatAccount = %q, want %q", got, tc.want)
			}
		})
	}
}
