package main

import (
	"testing"
)

// Weekly charging in the client — spec 2026-09-23-client-wallet-days, section
// "Недельное списание (решение 25.09)", parts "Клиент" and "Relay". Keys now
// charges a week at a time, and Relay's /v1/vc/me wallet grows, additively —
// every earlier field stays:
//
//	"wallet": { "balanceUsd", "tariff": { "tier", "monthlyPriceUsd", "dailyRateUsd",
//	                                      "weeklyPriceUsd" } | null,
//	            "todayPaid", "fundedDays",
//	            "chargeRequired": true | false,
//	            "periodEndsAt":   "<ISO>" | null }
//
// chargeRequired is Keys' own verdict: a tariff, no active paid period, and a
// balance that cannot pay the week. Relay refuses on it (402
// wallet_charge_required), so the client's advance notice follows it too. The
// rules pinned here, on top of those in wallet_client_test.go:
//
//  1. chargeRequired === true → the launch notice is exactly Relay's weekly
//     sentence, walletWeekRefusal — whatever todayPaid, dailyRateUsd and
//     fundedDays say. The old rule (todayPaid false and balance < daily rate)
//     no longer decides it: a balance that covers a day but not the week is
//     exactly the case the week exists for.
//  2. chargeRequired === false → no refusal notice, even where the old rule
//     would have given one. The low-balance notice at fundedDays <= 2 still
//     applies, as it did.
//  3. chargeRequired absent (a Relay in front of an older Keys) → the old rule
//     and the old "for today" sentence, unchanged — even when the tariff
//     already carries weeklyPriceUsd and periodEndsAt is there.
//  4. A new field of the wrong type drops the whole wallet, as any other
//     field does: no notice, no balance line.
//  5. The wallet line (`~N days left`) is unchanged: still fundedDays, which
//     Keys now computes weekly. The client does not recount days from the
//     balance and the weekly price.
//
// Driven through the same seams as wallet_client_test.go — `vc` from a
// terminal, `vc desktop-session`, `vc status` and `vc status --json` — with
// real /v1/vc/me bodies, so this file compiles against HEAD.

// walletWeekRefusal is the sentence Relay sends with its 402
// wallet_charge_required.
const walletWeekRefusal = "Balance is not enough for this week — message @makscee on Telegram to top up."

// Tariffs as Keys prices them per week: monthly = weekly × 4 and daily =
// weekly / 7, both kept for display only.
const (
	weekTariffT1 = `{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":15}`
	weekTariffT3 = `{"tier":"t3","monthlyPriceUsd":230,"dailyRateUsd":8.214285714285714,"weeklyPriceUsd":57.5}`
)

// periodEnd is the end of a paid period, as Keys writes it.
const periodEnd = `"2026-10-02T10:00:00.000Z"`

// weekWallet is a wallet from a weekly-charging Relay: the daily-era fields,
// then chargeRequired and periodEndsAt spliced verbatim. An empty
// chargeRequired or periodEndsAt leaves that key out altogether — the shape a
// Relay in front of an older Keys sends.
func weekWallet(balance, tariff, todayPaid, fundedDays, chargeRequired, periodEndsAt string) string {
	s := `"wallet":{"balanceUsd":` + balance + `,"tariff":` + tariff + `,"todayPaid":` + todayPaid + `,"fundedDays":` + fundedDays
	if chargeRequired != "" {
		s += `,"chargeRequired":` + chargeRequired
	}
	if periodEndsAt != "" {
		s += `,"periodEndsAt":` + periodEndsAt
	}
	return s + `}`
}

var weeklyGateCases = []walletGateCase{
	// ── chargeRequired: true — Relay's weekly sentence, and nothing else ──
	{name: "charge required, balance under the daily rate: week refusal",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "0", "true", "null")), notice: walletWeekRefusal},
	// The loophole case: the balance pays a day ($2.14) but not the week
	// ($15). The old rule saw no refusal here, only a low balance.
	{name: "charge required, balance covers a day but not the week: week refusal",
		body: meBody(weekWallet("10", weekTariffT1, "false", "0", "true", "null")), notice: walletWeekRefusal},
	{name: "t3, charge required, a cent under the week: week refusal",
		body: meBody(weekWallet("57.49", weekTariffT3, "false", "0", "true", "null")), notice: walletWeekRefusal},
	{name: "charge required, zero balance: week refusal",
		body: meBody(weekWallet("0", weekTariffT1, "false", "0", "true", "null")), notice: walletWeekRefusal},
	{name: "charge required, negative balance: week refusal",
		body: meBody(weekWallet("-3", weekTariffT1, "false", "-2", "true", "null")), notice: walletWeekRefusal},
	// The refusal outranks the low-balance notice, as the daily one did.
	{name: "charge required, 1 funded day: week refusal, not the low notice",
		body: meBody(weekWallet("10", weekTariffT1, "false", "1", "true", "null")), notice: walletWeekRefusal},
	// Keys' verdict alone decides: the daily-era fields do not veto it.
	{name: "charge required beside todayPaid true and 9 days: week refusal",
		body: meBody(weekWallet("100", weekTariffT1, "true", "9", "true", periodEnd)), notice: walletWeekRefusal},

	// ── chargeRequired: false — never a refusal; low notice at <= 2 days ──
	// todayPaid false with the balance under the daily rate is the old
	// rule's refusal; Keys says no charge is required, and Keys wins.
	{name: "no charge required, old rule would refuse, 5 days: no notice",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "5", "false", periodEnd))},
	{name: "no charge required, old rule would refuse, 2 days: low notice",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "2", "false", periodEnd)), notice: walletLowNotice(2)},
	{name: "no charge required, old rule would refuse, 0 days: low notice",
		body: meBody(weekWallet("0", weekTariffT1, "false", "0", "false", periodEnd)), notice: walletLowNotice(0)},
	{name: "no charge required, period paid, 1 day left: low notice",
		body: meBody(weekWallet("3", weekTariffT1, "true", "1", "false", periodEnd)), notice: walletLowNotice(1)},
	{name: "no charge required, fundedDays -2: low notice says 0 days",
		body: meBody(weekWallet("-3", weekTariffT1, "true", "-2", "false", "null")), notice: walletLowNotice(0)},
	{name: "no charge required, 3 days left: no notice",
		body: meBody(weekWallet("3", weekTariffT1, "true", "3", "false", periodEnd))},
	{name: "no charge required, 13 days left: no notice",
		body: meBody(weekWallet("20", weekTariffT1, "true", "13", "false", periodEnd))},
	{name: "no charge required, no tariff: no notice",
		body: meBody(weekWallet("0", "null", "null", "null", "false", "null"))},

	// ── chargeRequired absent — the old rule, the old sentence ──
	// A weekly tariff and a period end are not a verdict: only chargeRequired
	// switches the client to the weekly rule.
	{name: "no verdict, weekly tariff and period end, balance under the daily rate: today's refusal",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "0", "", periodEnd)), notice: walletBlockMessage},
	{name: "no verdict, weekly tariff, balance covers the day, 2 days: low notice",
		body: meBody(weekWallet("4.5", weekTariffT1, "false", "2", "", "")), notice: walletLowNotice(2)},
	{name: "no verdict, weekly tariff, balance covers the day, 9 days: no notice",
		body: meBody(weekWallet("20", weekTariffT1, "false", "9", "", periodEnd))},

	// ── a new field the client cannot read drops the wallet: no notice ──
	// Each body would give today's refusal if the bad field were skipped.
	{name: "chargeRequired as a string: no notice",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "0", `"true"`, "null"))},
	{name: "chargeRequired as a number: no notice",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "0", "1", "null"))},
	{name: "periodEndsAt as a number: no notice",
		body: meBody(weekWallet("1.5", weekTariffT1, "false", "0", "true", "1759399200"))},
	{name: "weeklyPriceUsd as a string: no notice",
		body: meBody(weekWallet("1.5", `{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":"15"}`, "false", "0", "true", "null"))},
}

func TestTerminalLaunchFollowsWeeklyWalletRules(t *testing.T) {
	for _, tc := range weeklyGateCases {
		t.Run(tc.name, func(t *testing.T) { assertTerminalLaunch(t, tc) })
	}
}

func TestDesktopSessionFollowsWeeklyWalletRules(t *testing.T) {
	for _, tc := range weeklyGateCases {
		t.Run(tc.name, func(t *testing.T) { assertDesktopSessionLaunch(t, tc) })
	}
}

func TestStatusJSONCarriesWeeklyLaunchNotice(t *testing.T) {
	for _, tc := range weeklyGateCases {
		t.Run(tc.name, func(t *testing.T) { assertStatusJSONLaunchNotice(t, tc) })
	}
}

// The wallet line does not change with the week: `vc status` and walletText
// still say `$X · TIER · ~N days left`, N being fundedDays as Keys sent it.
// The days are not recounted from balance / weeklyPriceUsd: $57.50 on T3 is
// one week by the price, and the line still says the 13 days Keys counted
// (the rest of the paid period plus that week). A wallet dropped over a
// malformed new field shows nothing — want "".
func TestWalletLineUnchangedByWeeklyFields(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"paid period, no charge required", meBody(weekWallet("18", weekTariffT1, "true", "9", "false", periodEnd)), "$18.00 · T1 · ~9 days left"},
		{"charge required: the balance and 0 days, no refusal in the line", meBody(weekWallet("10", weekTariffT1, "false", "0", "true", "null")), "$10.00 · T1 · ~0 days left"},
		{"days are fundedDays, not balance over the weekly price", meBody(weekWallet("57.5", weekTariffT3, "true", "13", "false", periodEnd)), "$57.50 · T3 · ~13 days left"},
		{"one day", meBody(weekWallet("3", weekTariffT1, "true", "1", "false", periodEnd)), "$3.00 · T1 · ~1 day left"},
		{"a debt, days never below 0", meBody(weekWallet("-3", weekTariffT1, "true", "-2", "false", "null")), "-$3.00 · T1 · ~0 days left"},
		{"no tariff: the balance alone", meBody(weekWallet("18", "null", "null", "null", "false", "null")), "$18.00"},
		{"no verdict from an older Keys", meBody(weekWallet("18", weekTariffT1, "true", "9", "", periodEnd)), "$18.00 · T1 · ~9 days left"},
		{"chargeRequired as a string drops the wallet", meBody(weekWallet("18", weekTariffT1, "true", "9", `"false"`, periodEnd)), ""},
		{"periodEndsAt as a bool drops the wallet", meBody(weekWallet("18", weekTariffT1, "true", "9", "false", "true")), ""},
		{"weeklyPriceUsd as a bool drops the wallet", meBody(weekWallet("18", `{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2.142857142857143,"weeklyPriceUsd":true}`, "true", "9", "false", periodEnd)), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, hasLine := statusLine(humanStatus(t, tc.body), "balance:")
			got := jsonStatus(t, tc.body)["walletText"]
			if tc.want == "" {
				if hasLine {
					t.Errorf("vc status prints %q for a wallet it cannot read", line)
				}
				if got != nil {
					t.Errorf("walletText = %#v, want null for a wallet vc cannot read", got)
				}
				return
			}
			if want := "balance: " + tc.want; line != want {
				t.Errorf("vc status balance line = %q (present=%v), want %q", line, hasLine, want)
			}
			if s, ok := got.(string); !ok || s != tc.want {
				t.Errorf("walletText = %#v, want %q", got, tc.want)
			}
		})
	}
}
