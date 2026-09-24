package main

import (
	"strings"
	"testing"
)

// `vc status --json` carries walletText — second panel on void-code#76, G3:
// the desktop renderer showed no balance and no days at all. The desktop gets
// the line already written: walletText is exactly what formatWallet renders,
// the same words `vc status` prints after "balance: " and the welcome screen
// shows, or null when there is no wallet. The display rules (-$3.00, cents
// floored, days never below 0, no days without a tariff) therefore live in one
// place, in Go, and TypeScript never re-implements them.
//
// A refusal carries no walletText (status_json_access_test.go), and the wallet
// object itself stays as it is (TestStatusJSONMirrorsWallet).

func TestStatusJSONCarriesWalletText(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"t1, 9 days", meBody(wallet("18", tariffT1, "true", "9")), "$18.00 · T1 · ~9 days left"},
		{"t2, 5 days", meBody(wallet("20.5", tariffT2, "true", "5")), "$20.50 · T2 · ~5 days left"},
		{"one day", meBody(wallet("2", tariffT1, "true", "1")), "$2.00 · T1 · ~1 day left"},
		{"no tariff: the balance alone", meBody(wallet("18", "null", "null", "null")), "$18.00"},
		{"a tariff without a day count", meBody(wallet("18", tariffT1, "true", "null")), "$18.00 · T1"},
		{"a debt: minus before the dollar, days never below 0", meBody(wallet("-3", tariffT1, "false", "-2")), "-$3.00 · T1 · ~0 days left"},
		{"a fraction of a cent is floored, not rounded up", meBody(wallet("7.666", tariffT3, "true", "0")), "$7.66 · T3 · ~0 days left"},
		{"exact cents stored low: 1.15", meBody(wallet("1.15", "null", "null", "null")), "$1.15"},
		{"the retired pct alongside changes nothing", meBody(`"pct":77,"resetAt":"2026-10-01T00:00:00Z",` + wallet("18", tariffT1, "true", "9")), "$18.00 · T1 · ~9 days left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := jsonStatus(t, tc.body) // asserts authState == signed_in
			got, present := obj["walletText"]
			if s, ok := got.(string); !ok || s != tc.want {
				t.Fatalf("walletText = %#v (present=%v), want %q", got, present, tc.want)
			}
			if strings.Contains(got.(string), "%") {
				t.Errorf("walletText %q shows a percentage", got)
			}
			// One formatter: the line the desktop gets is the line `vc status`
			// prints after its label.
			line, ok := statusLine(humanStatus(t, tc.body), "balance:")
			if !ok {
				t.Fatalf("vc status has no balance line for this wallet")
			}
			if want := strings.TrimPrefix(line, "balance: "); got != want {
				t.Errorf("walletText = %q, but `vc status` says %q — two formatters", got, want)
			}
		})
	}
}

// No wallet on the wire, or one vc cannot read: nothing to show, and the
// desktop must not be handed a made-up "$0.00".
func TestStatusJSONWalletTextNullWithoutWallet(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"old server, pct budget", meBody(`"pct":42.5,"resetAt":"2026-10-01T00:00:00Z"`)},
		{"void-auth era top-level balance", meBody(`"balanceUsd":12.4`)},
		{"nothing", meBody("")},
		{"a wallet vc cannot read (todayPaid as a string)", meBody(wallet("1.5", tariffT1, `"false"`, "0"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := jsonStatus(t, tc.body)
			if got := obj["walletText"]; got != nil {
				t.Errorf("walletText = %#v, want null or absent — there is no wallet to show", got)
			}
		})
	}
}
