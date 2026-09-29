package main

import (
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// The weekly limit (void-board#224) on the account line: `vc status` after
// "plan:", walletText in `vc status --json` (which the desktop shows as
// is), and the welcome screen — one formatter, the wallet first, then the
// limit as a share, never money.

func TestStatusShowsTheWeeklyLimit(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"42 beside a wallet", meBody(limitMember(42) + "," + wallet("18", tariffT1, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 42%, сброс через 3 дня"},
		{"85 beside a wallet", meBody(limitMember(85) + "," + wallet("18", tariffT1, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 85%, сброс через 3 дня"},
		{"a share floored: 79.9 reads 79", meBody(limitMember(79.9) + "," + wallet("18", tariffT1, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 79%, сброс через 3 дня"},
		{"no wallet: the limit alone", meBody(limitMember(42)), "лимит использован на 42%, сброс через 3 дня"},
		{"a tariff without monthlyPriceUsd: what is there", meBody(limitMember(42) + "," + wallet("18", `{"tier":"t1","dailyRateUsd":2}`, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 42%, сброс через 3 дня"},
		{"a tariff without either price: what is there", meBody(limitMember(42) + "," + wallet("18", `{"tier":"t1"}`, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 42%, сброс через 3 дня"},
		{"a wallet vc cannot read (a price as a string): the limit alone", meBody(limitMember(42) + "," + wallet("18", `{"tier":"t1","monthlyPriceUsd":"60","dailyRateUsd":2}`, "true", "9")), "лимит использован на 42%, сброс через 3 дня"},
		{"no tariff: the limit alone, no money", meBody(limitMember(42) + "," + wallet("18", "null", "null", "null")), "лимит использован на 42%, сброс через 3 дня"},
		{"no reset sent", meBody(`"limit":{"pct":42},` + wallet("18", tariffT1, "true", "9")), "T1 · осталось ~9 дней · лимит использован на 42%"},
		{"no limit (older Relay): unchanged", meBody(wallet("18", tariffT1, "true", "9")), "T1 · осталось ~9 дней"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := jsonStatus(t, tc.body)
			if got, _ := obj["walletText"].(string); got != tc.want {
				t.Fatalf("walletText = %#v, want %q", obj["walletText"], tc.want)
			}
			line, ok := statusLine(humanStatus(t, tc.body), "plan:")
			if !ok || strings.TrimPrefix(line, "plan: ") != tc.want {
				t.Errorf("`vc status` plan line = %q, want %q", line, tc.want)
			}
			if strings.Contains(tc.want, "$") {
				t.Errorf("%q shows money", tc.want)
			}
		})
	}
}

func TestStatusJSONCarriesTheLimit(t *testing.T) {
	obj := jsonStatus(t, meBody(`"limit":{"pct":42.5,"resetAt":"2026-10-01T00:00:00.000Z"}`))
	limit, ok := obj["limit"].(map[string]any)
	if !ok || limit["pct"] != 42.5 || limit["resetAt"] != "2026-10-01T00:00:00Z" {
		t.Fatalf("limit = %#v, want {pct: 42.5, resetAt: 2026-10-01T00:00:00Z}", obj["limit"])
	}
	if _, present := obj["wallet"]; present {
		t.Errorf("wallet = %v; the server sent none", obj["wallet"])
	}
	if obj := jsonStatus(t, meBody(wallet("18", tariffT1, "true", "9"))); obj["limit"] != nil {
		t.Errorf("limit = %#v with no limit sent, want it absent", obj["limit"])
	}
}

func TestWelcomeShowsTheLimit(t *testing.T) {
	reset := time.Now().Add(73 * time.Hour)
	me := auth.MeResult{UserID: "u-1", Limit: &auth.Limit{Pct: 42, ResetAt: &reset}}
	if got, want := verifiedWelcomeState(me).Balance, "лимит использован на 42%, сброс через 3 дня"; got != want {
		t.Errorf("welcome balance = %q, want %q", got, want)
	}
}
