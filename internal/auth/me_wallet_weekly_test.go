package auth

import (
	"strings"
	"testing"
)

// Weekly charging on /v1/vc/me — spec 2026-09-23-client-wallet-days, section
// "Недельное списание (решение 25.09)", parts "Relay" and "Клиент". The
// wallet grows, additively; every earlier field stays:
//
//	"wallet": {
//	  ...,                                   // balanceUsd, todayPaid, fundedDays as before
//	  "tariff": { ..., "weeklyPriceUsd": 15 } | null,
//	  "chargeRequired": true | false,        // Keys' verdict: no paid period, balance short of the week
//	  "periodEndsAt":   "2026-10-02T10:00:00.000Z" | null
//	}
//
// The seam these tests need — the only new names in this package:
//
//	type Wallet struct {
//		... // as before
//		ChargeRequired *bool   // nil: the server did not say (an older Keys) — the old daily rule applies
//		PeriodEndsAt   *string // as sent; nil: absent or null
//	}
//	type Tariff struct {
//		... // as before
//		WeeklyPriceUsd *float64 // nil: an older server that sends no weekly price
//	}
//
// ChargeRequired is a pointer for the same reason TodayPaid is: false ("no
// refusal, whatever the old rule says") and nil ("use the old rule") lead to
// different launch notices and must not collapse into one zero value.
//
// All three are optional — a Relay that does not send them still gives a
// wallet. Present with the wrong type, any one of them drops the whole wallet,
// as a wrong-typed old field does (parseWallet's strictness): a guessed
// verdict could tell a person they cannot work when they can, or the reverse.

const weeklyTariffT1 = `{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":15}`

func TestFetchMe_WalletWeeklyFields(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":18,"tariff":`+weeklyTariffT1+`,"todayPaid":true,"fundedDays":13,"chargeRequired":false,"periodEndsAt":"2026-10-02T10:00:00.000Z"}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	w := res.Wallet
	if w == nil {
		t.Fatal("Wallet = nil, want the wallet the server sent — the new fields are well-formed")
	}
	if w.ChargeRequired == nil {
		t.Fatal("ChargeRequired = nil, want false — Keys said no charge is required, and that is not 'Keys did not say'")
	}
	if *w.ChargeRequired {
		t.Error("ChargeRequired = true, want false")
	}
	if w.PeriodEndsAt == nil || *w.PeriodEndsAt != "2026-10-02T10:00:00.000Z" {
		t.Errorf("PeriodEndsAt = %v, want %q as sent", w.PeriodEndsAt, "2026-10-02T10:00:00.000Z")
	}
	if w.Tariff == nil {
		t.Fatal("Tariff = nil, want t1")
	}
	if w.Tariff.WeeklyPriceUsd == nil || *w.Tariff.WeeklyPriceUsd != 15 {
		t.Errorf("Tariff.WeeklyPriceUsd = %v, want 15", w.Tariff.WeeklyPriceUsd)
	}
	// The contract only grew: the earlier fields read as they did.
	if w.BalanceUsd != 18 || w.Tariff.Tier != "t1" || w.Tariff.MonthlyPriceUsd != 60 || w.Tariff.DailyRateUsd != 2.142857142857143 {
		t.Errorf("Wallet = %+v, Tariff = %+v — the earlier fields changed", *w, *w.Tariff)
	}
	if w.TodayPaid == nil || !*w.TodayPaid || w.FundedDays == nil || *w.FundedDays != 13 {
		t.Errorf("TodayPaid = %v, FundedDays = %v, want true / 13", w.TodayPaid, w.FundedDays)
	}
}

func TestFetchMe_WalletChargeRequiredWithoutPeriod(t *testing.T) {
	res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":{"balanceUsd":10,"tariff":`+weeklyTariffT1+`,"todayPaid":false,"fundedDays":0,"chargeRequired":true,"periodEndsAt":null}}`)
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	w := res.Wallet
	if w == nil {
		t.Fatal("Wallet = nil, want the wallet — periodEndsAt null means no paid period, not a malformed wallet")
	}
	if w.ChargeRequired == nil || !*w.ChargeRequired {
		t.Errorf("ChargeRequired = %v, want true", w.ChargeRequired)
	}
	if w.PeriodEndsAt != nil {
		t.Errorf("PeriodEndsAt = %q, want nil for null", *w.PeriodEndsAt)
	}
}

// A Relay that does not send the new fields — in front of an older Keys, or
// older itself — still gives a wallet, and each field it left out reads nil.
// Each is optional on its own, not only all three together.
func TestFetchMe_WalletWithoutWeeklyFieldsStillParses(t *testing.T) {
	for name, wallet := range map[string]string{
		"none of the three (the daily-era wallet)": `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9}`,
		"no chargeRequired":                        `{"balanceUsd":18,"tariff":` + weeklyTariffT1 + `,"todayPaid":true,"fundedDays":9,"periodEndsAt":"2026-10-02T10:00:00.000Z"}`,
		"no periodEndsAt":                          `{"balanceUsd":18,"tariff":` + weeklyTariffT1 + `,"todayPaid":true,"fundedDays":9,"chargeRequired":false}`,
		"no weeklyPriceUsd":                        `{"balanceUsd":18,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2},"todayPaid":true,"fundedDays":9,"chargeRequired":false,"periodEndsAt":"2026-10-02T10:00:00.000Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := fetchMeBody(t, `{"subject_id":"subj-1","wallet":`+wallet+`}`)
			if err != nil {
				t.Fatalf("FetchMe: %v", err)
			}
			w := res.Wallet
			if w == nil || w.Tariff == nil {
				t.Fatalf("Wallet = %+v, want the wallet with its tariff — the new fields are optional", w)
			}
			if w.BalanceUsd != 18 || w.Tariff.Tier != "t1" || w.FundedDays == nil || *w.FundedDays != 9 {
				t.Errorf("Wallet = %+v, want balance 18, t1, 9 days", *w)
			}
			has := func(key string) bool { return strings.Contains(wallet, `"`+key+`"`) }
			if !has("chargeRequired") && w.ChargeRequired != nil {
				t.Errorf("ChargeRequired = %v, want nil — the server did not say, and silence is not false", *w.ChargeRequired)
			}
			if !has("periodEndsAt") && w.PeriodEndsAt != nil {
				t.Errorf("PeriodEndsAt = %q, want nil — the server sent none", *w.PeriodEndsAt)
			}
			if !has("weeklyPriceUsd") && w.Tariff.WeeklyPriceUsd != nil {
				t.Errorf("Tariff.WeeklyPriceUsd = %v, want nil — the server sent none, and 0 would be a price", *w.Tariff.WeeklyPriceUsd)
			}
		})
	}
}

// Present with the wrong type, a new field drops the whole wallet — and, as
// with the old fields, never the sign-in. Each body is a wallet the client
// would read fine if the bad field were skipped.
func TestFetchMe_MalformedWeeklyFieldDropsWallet(t *testing.T) {
	const tail = `,"todayPaid":false,"fundedDays":0`
	for name, wallet := range map[string]string{
		"chargeRequired as string true":  `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":"true","periodEndsAt":null}`,
		"chargeRequired as string false": `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":"false","periodEndsAt":null}`,
		"chargeRequired as 1":            `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":1,"periodEndsAt":null}`,
		"chargeRequired as 0":            `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":0,"periodEndsAt":null}`,
		"chargeRequired as an object":    `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":{"value":true},"periodEndsAt":null}`,
		"periodEndsAt as a number":       `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":true,"periodEndsAt":1759399200}`,
		"periodEndsAt as a bool":         `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":true,"periodEndsAt":true}`,
		"periodEndsAt as an object":      `{"balanceUsd":1.5,"tariff":` + weeklyTariffT1 + tail + `,"chargeRequired":true,"periodEndsAt":{"at":"2026-10-02T10:00:00.000Z"}}`,
		"weeklyPriceUsd as a string":     `{"balanceUsd":1.5,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":"15"}` + tail + `,"chargeRequired":true,"periodEndsAt":null}`,
		"weeklyPriceUsd as a bool":       `{"balanceUsd":1.5,"tariff":{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":true}` + tail + `,"chargeRequired":true,"periodEndsAt":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			res, err := fetchMeBody(t, `{"subject_id":"subj-1","email":"person@example.test","wallet":`+wallet+`}`)
			if err != nil {
				t.Fatalf("FetchMe: %v — a wallet the client cannot read must not become a failed sign-in", err)
			}
			if res.UserID != "subj-1" || res.Email != "person@example.test" {
				t.Errorf("identity = %q/%q, want subj-1/person@example.test", res.UserID, res.Email)
			}
			if res.Wallet != nil {
				t.Errorf("Wallet = %+v, want nil — a malformed new field must not be skipped into a half-read wallet", *res.Wallet)
			}
		})
	}
}
