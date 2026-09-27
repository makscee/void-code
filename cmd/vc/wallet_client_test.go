package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/welcome"
)

// Wallet and tariff in the client — spec 2026-09-23-client-wallet-days,
// section "Клиент (vc и десктоп)". Relay's /v1/vc/me now answers with
//
//	"wallet": { "balanceUsd", "tariff": { "tier", "monthlyPriceUsd", "dailyRateUsd" } | null,
//	            "todayPaid", "fundedDays" }
//
// and no longer with pct/resetAt. The rules pinned here:
//
//  1. No percentage anywhere: no `budget:` line, no `pct` in --json, no
//     "Budget at N%" at launch, no launch refused over pct. A server that still
//     sends pct is ignored.
//  2. `vc status` prints `plan: T1 · осталось ~9 дней` (tariff), nothing
//     without a tariff or a wallet (no money shown, void-board#224); --json carries
//     `wallet` mirroring the server.
//  3. The welcome screen shows the same text where it showed `$X left`.
//  4. Launch (terminal and desktop-session) is NEVER refused over the wallet —
//     only Relay refuses (402 wallet_daily_charge_required, under its
//     BUDGET_ENFORCE switch), and Pi shows that refusal itself. The client
//     computes a launch notice instead (spec, "Поправка 23.09 (после панели
//     void-code#76)"), tariff required:
//       - todayPaid === false and balanceUsd < dailyRateUsd → walletBlockMessage
//         (Relay's own refusal sentence, as advance notice);
//       - otherwise fundedDays <= 2 → walletLowNotice(fundedDays);
//       - anything else → no notice.
//     An unpaid day that the balance still covers is not a refusal: right after
//     00:00 UTC the daily charge may simply not have run yet.
//  5. The notice reaches Pi, not the terminal: vc hands it to Pi as
//     VC_LAUNCH_NOTICE, and the managed extension shows it on session_start
//     (desktop/tests/pi-launch-notice.test.ts). Pi's fullscreen mode clears
//     whatever was printed before it, so nothing about money is printed before
//     Pi starts. A VC_LAUNCH_NOTICE inherited from the parent never reaches Pi.
//  6. `vc status --json` carries the same notice as `launchNotice`.
//  7. Display: no money at all (void-board#224), and days never go below 0.
//
// Every test here drives an existing seam (runStatus, runStatusJSON, runSpawn,
// the desktop-session command, the welcome program) through a real /v1/vc/me
// body, so this file compiles against HEAD and says nothing about how the
// wallet is carried inside the client.

const walletBlockMessage = "Balance is not enough for today — top up: https://profile.makscee.ru/vc/pay"

// launchNoticeEnv is how vc hands the launch notice to Pi.
const launchNoticeEnv = "VC_LAUNCH_NOTICE"

// walletLowNotice is the low-balance notice for n funded days, spelled the way
// cmd/vc/wallet.go daysLeft spells a day count ("1 day left", "N days left").
func walletLowNotice(n int) string {
	days := fmt.Sprintf("%d days left", n)
	if n == 1 {
		days = "1 day left"
	}
	return "Balance low — " + days + ". Top up: https://profile.makscee.ru/vc/pay"
}

const (
	tariffT1 = `{"tier":"t1","monthlyPriceUsd":60,"dailyRateUsd":2}`
	tariffT2 = `{"tier":"t2","monthlyPriceUsd":120,"dailyRateUsd":4}`
	tariffT3 = `{"tier":"t3","monthlyPriceUsd":230,"dailyRateUsd":7.67}`
)

// meBody is a signed-in /v1/vc/me answer; extra is spliced in verbatim
// (`"wallet":{...}`, `"pct":100`, ...).
func meBody(extra string) string {
	if extra == "" {
		return `{"userId":"u-1","email":"person@example.test"}`
	}
	return `{"userId":"u-1","email":"person@example.test",` + extra + `}`
}

func wallet(balance, tariff, todayPaid, fundedDays string) string {
	return `"wallet":{"balanceUsd":` + balance + `,"tariff":` + tariff + `,"todayPaid":` + todayPaid + `,"fundedDays":` + fundedDays + `}`
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// plainText drops terminal styling so the assertions are about the words a
// person reads, whatever colour profile lipgloss picked for the test process.
func plainText(s string) string { return ansiEscape.ReplaceAllString(s, "") }

func meServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// ─── vc status ──────────────────────────────────────────────────────────────

func humanStatus(t *testing.T, body string) string {
	t.Helper()
	signedIn(t)
	t.Setenv(config.EnvAccessCheckHost, meServer(t, body))
	out, err := captureStdout(t, func() error { return runStatus(nil, nil) })
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	return plainText(out)
}

// statusLine returns the trimmed line that starts with label, if any.
func statusLine(out, label string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		// Labels are padded to line up ("plan:    T1"); the value after
		// the padding comes back after a single space ("plan: T1").
		if l := strings.TrimSpace(line); strings.HasPrefix(l, label) {
			return label + " " + strings.TrimSpace(strings.TrimPrefix(l, label)), true
		}
	}
	return "", false
}

func assertNoPercent(t *testing.T, where, out string) {
	t.Helper()
	if strings.Contains(out, "%") {
		t.Errorf("%s shows a percentage — the client shows money and days, never percent:\n%s", where, out)
	}
	if strings.Contains(strings.ToLower(out), "budget") {
		t.Errorf("%s still talks about a budget:\n%s", where, out)
	}
}

func TestStatusShowsBalanceTierAndDays(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"t1", meBody(wallet("18", tariffT1, "true", "9")), "plan: T1 · осталось ~9 дней"},
		{"t2", meBody(wallet("20.5", tariffT2, "true", "5")), "plan: T2 · осталось ~5 дней"},
		// The server that still sends the retired budget next to the wallet:
		// the wallet line prints, the percentage does not.
		{"t1 with retired pct alongside", meBody(`"pct":77,"resetAt":"2026-10-01T00:00:00Z",` + wallet("18", tariffT1, "true", "9")), "plan: T1 · осталось ~9 дней"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := humanStatus(t, tc.body)
			got, ok := statusLine(out, "plan:")
			if !ok {
				t.Fatalf("vc status has no plan line:\n%s", out)
			}
			if got != tc.want {
				t.Errorf("plan line = %q, want %q", got, tc.want)
			}
			if _, ok := statusLine(out, "budget:"); ok {
				t.Errorf("vc status still prints a budget line:\n%s", out)
			}
			assertNoPercent(t, "vc status", out)
		})
	}
}

// Without a tariff there are no days to count, and the balance is money,
// which the client never shows (void-board#224): no plan line at all.
func TestStatusShowsNoLineWithoutTariff(t *testing.T) {
	out := humanStatus(t, meBody(wallet("18", "null", "null", "null")))
	if line, ok := statusLine(out, "plan:"); ok {
		t.Errorf("vc status prints %q for a wallet with no tariff, want no plan line", line)
	}
	if strings.Contains(out, "$") {
		t.Errorf("vc status shows money:\n%s", out)
	}
}

// Old servers: no wallet means no plan line — neither from the retired
// pct budget nor from the void-auth era top-level balanceUsd.
func TestStatusShowsNoMoneyLineWithoutWallet(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"pct budget", meBody(`"pct":42.5,"resetAt":"2026-10-01T00:00:00Z"`)},
		{"pct at the cap", meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z"`)},
		{"top-level balance", meBody(`"balanceUsd":12.4`)},
		{"nothing", meBody("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := humanStatus(t, tc.body)
			if !strings.Contains(out, "logged in as person@example.test") {
				t.Fatalf("status did not sign in:\n%s", out)
			}
			for _, label := range []string{"plan:", "balance:", "budget:"} {
				if line, ok := statusLine(out, label); ok {
					t.Errorf("no wallet on the wire, yet status prints %q", line)
				}
			}
			if strings.Contains(out, "$") {
				t.Errorf("no wallet on the wire, yet status prints money:\n%s", out)
			}
			assertNoPercent(t, "vc status", out)
		})
	}
}

// ─── vc status --json ───────────────────────────────────────────────────────

func jsonStatus(t *testing.T, body string) map[string]any {
	t.Helper()
	signedIn(t)
	t.Setenv(config.EnvAccessCheckHost, meServer(t, body))
	var buf bytes.Buffer
	if err := runStatusJSON(config.OSResolve(), &buf); err != nil {
		t.Fatalf("runStatusJSON: %v", err)
	}
	assertNoANSI(t, buf.Bytes())
	obj := decodeSingleJSONObject(t, buf.Bytes())
	if obj["authState"] != "signed_in" {
		t.Fatalf("authState = %v, want signed_in", obj["authState"])
	}
	for _, retired := range []string{"pct", "resetAt"} {
		if _, present := obj[retired]; present {
			t.Errorf("--json carries %q = %v; the client reports no percentages", retired, obj[retired])
		}
	}
	return obj
}

func jsonWallet(t *testing.T, obj map[string]any) map[string]any {
	t.Helper()
	w, ok := obj["wallet"].(map[string]any)
	if !ok {
		t.Fatalf("wallet = %#v, want an object", obj["wallet"])
	}
	return w
}

// The desktop reads --json; it gets the wallet as the server said it, under
// the server's names — tier included, unformatted — but an older Relay's
// dollars stay behind (void-board#234): its balance mirrors as null kopecks.
func TestStatusJSONMirrorsWallet(t *testing.T) {
	obj := jsonStatus(t, meBody(`"pct":42.5,"resetAt":"2026-10-01T00:00:00Z",`+wallet("18", tariffT1, "true", "9")))
	w := jsonWallet(t, obj)
	if v, present := w["balanceKopecks"]; !present || v != nil {
		t.Errorf("wallet.balanceKopecks = %v (present=%v), want null from a dollar-only Relay", v, present)
	}
	tariff, ok := w["tariff"].(map[string]any)
	if !ok {
		t.Fatalf("wallet.tariff = %#v, want an object", w["tariff"])
	}
	if tariff["tier"] != "t1" {
		t.Errorf("wallet.tariff = %v, want tier t1", tariff)
	}
	assertNoDollarField(t, w)
	if w["todayPaid"] != true {
		t.Errorf("wallet.todayPaid = %v, want true", w["todayPaid"])
	}
	if w["fundedDays"] != 9.0 {
		t.Errorf("wallet.fundedDays = %v, want 9", w["fundedDays"])
	}
}

// false and 0 are the values that matter most and the ones an `omitempty`
// silently drops: an unpaid day must reach the desktop as false, not vanish.
func TestStatusJSONKeepsUnpaidDayAndZeroDays(t *testing.T) {
	w := jsonWallet(t, jsonStatus(t, meBody(wallet("1.5", tariffT3, "false", "0"))))
	if v, present := w["todayPaid"]; !present || v != false {
		t.Errorf("wallet.todayPaid = %v (present=%v), want false", v, present)
	}
	if v, present := w["fundedDays"]; !present || v != 0.0 {
		t.Errorf("wallet.fundedDays = %v (present=%v), want 0", v, present)
	}
	if tariff, _ := w["tariff"].(map[string]any); tariff == nil || tariff["tier"] != "t3" {
		t.Errorf("wallet.tariff = %v, want t3", w["tariff"])
	}
	assertNoDollarField(t, w)
}

// assertNoDollarField fails on any key, at any depth, that names dollars.
func assertNoDollarField(t *testing.T, v any) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		for k, inner := range v {
			if strings.Contains(strings.ToLower(k), "usd") {
				t.Errorf("--json carries %q = %v; no dollar leaves vc", k, inner)
			}
			assertNoDollarField(t, inner)
		}
	case []any:
		for _, inner := range v {
			assertNoDollarField(t, inner)
		}
	}
}

func TestStatusJSONWalletWithoutTariff(t *testing.T) {
	w := jsonWallet(t, jsonStatus(t, meBody(wallet("18", "null", "null", "null"))))
	assertNoDollarField(t, w)
	// null or absent — either says "no tariff"; a zero-valued object does not.
	for _, field := range []string{"tariff", "todayPaid", "fundedDays"} {
		if w[field] != nil {
			t.Errorf("wallet.%s = %#v, want null/absent without a tariff", field, w[field])
		}
	}
}

func TestStatusJSONOmitsWalletFromOldServers(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"pct budget", meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z"`)},
		{"top-level balance", meBody(`"balanceUsd":12.4`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := jsonStatus(t, tc.body)
			if _, present := obj["wallet"]; present {
				t.Errorf("wallet = %v; the server sent none", obj["wallet"])
			}
		})
	}
}

// ─── launch: shared cases for `vc` and `vc desktop-session` ─────────────────

type walletGateCase struct {
	name string
	body string
	// notice is the exact launch notice vc hands to Pi (and reports as
	// `launchNotice` in `vc status --json`); "" means none at all. No case
	// refuses the launch: every one of them must start Pi.
	notice string
}

var walletGateCases = []walletGateCase{
	// Unpaid AND the balance cannot pay the day: Relay will refuse the first
	// request with 402. The client does not refuse — it starts Pi and passes
	// Relay's sentence along as advance notice. This takes priority over the
	// low-balance notice (fundedDays is 0 here, which alone would say "low").
	{name: "t1, today unpaid, balance under the rate: refusal notice, Pi starts", body: meBody(wallet("1.5", tariffT1, "false", "0")), notice: walletBlockMessage},
	{name: "t3, today unpaid, balance under the rate: refusal notice, Pi starts", body: meBody(wallet("3", tariffT3, "false", "0")), notice: walletBlockMessage},
	{name: "t3, today unpaid, a cent under the rate: refusal notice, Pi starts", body: meBody(wallet("7.66", tariffT3, "false", "0")), notice: walletBlockMessage},
	{name: "t1, today unpaid, zero balance: refusal notice, Pi starts", body: meBody(wallet("0", tariffT1, "false", "0")), notice: walletBlockMessage},
	{name: "t1, today unpaid, negative balance: refusal notice, Pi starts", body: meBody(wallet("-3", tariffT1, "false", "-2")), notice: walletBlockMessage},
	// The day is unpaid but the balance covers it — the charge has not run yet
	// (just after 00:00 UTC). No refusal notice; the low notice follows
	// fundedDays.
	{name: "today unpaid, balance covers it, 9 days: no notice", body: meBody(wallet("18", tariffT1, "false", "9"))},
	{name: "today unpaid, balance exactly the rate: low notice, 1 day", body: meBody(wallet("2", tariffT1, "false", "1")), notice: walletLowNotice(1)},
	{name: "t3, today unpaid, balance exactly the rate: low notice, 1 day", body: meBody(wallet("7.67", tariffT3, "false", "1")), notice: walletLowNotice(1)},
	{name: "today unpaid, balance covers it, 2 days: low notice", body: meBody(wallet("4", tariffT1, "false", "2")), notice: walletLowNotice(2)},
	// Paid: low notice at fundedDays <= 2, nothing above.
	{name: "today paid, 2 days left: low notice", body: meBody(wallet("4", tariffT1, "true", "2")), notice: walletLowNotice(2)},
	{name: "today paid, 1 day left: low notice", body: meBody(wallet("2", tariffT1, "true", "1")), notice: walletLowNotice(1)},
	{name: "today paid, 0 days left: low notice", body: meBody(wallet("1", tariffT1, "true", "0")), notice: walletLowNotice(0)},
	// Days are never shown below zero, in the notice included.
	{name: "today paid, fundedDays -2: low notice says 0 days", body: meBody(wallet("-3", tariffT1, "true", "-2")), notice: walletLowNotice(0)},
	{name: "today paid, 3 days left: no notice", body: meBody(wallet("6", tariffT1, "true", "3"))},
	{name: "today paid, 9 days left: no notice", body: meBody(wallet("18", tariffT1, "true", "9"))},
	// Every notice needs a tariff; Keys never says todayPaid:false without one,
	// but if something does, there is no daily charge to be behind on.
	{name: "no tariff, todayPaid false, zero balance: no notice", body: meBody(wallet("0", "null", "false", "null"))},
	{name: "no tariff: no notice", body: meBody(wallet("0.25", "null", "null", "null"))},
	// An inconsistent payload — balance and fundedDays both real zeros, with
	// no tariff — still says nothing: no tariff means no notice at all,
	// whatever todayPaid and fundedDays say. fundedDays:0 (not null) matters
	// here — a low-balance notice built from a real day count would show up
	// the moment the "no tariff" guard is skipped.
	{name: "no tariff, todayPaid false, zero balance, zero days: no notice", body: meBody(wallet("0", "null", "false", "0"))},
	{name: "no tariff, todayPaid null, fundedDays null, zero balance: no notice", body: meBody(wallet("0", "null", "null", "null"))},
	// null is not false.
	{name: "tariff, todayPaid null, balance under the rate: no refusal notice", body: meBody(wallet("1", tariffT1, "null", "9"))},
	// A wallet the client cannot read says nothing.
	{name: "todayPaid as a string: no notice", body: meBody(wallet("1.5", tariffT1, `"false"`, "0"))},
	// Old servers: pct is ignored — nothing at the cap, nothing under it.
	{name: "old server, pct 100: no notice", body: meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "old server, pct 150: no notice", body: meBody(`"pct":150,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "old server, pct 85: no notice", body: meBody(`"pct":85,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "no wallet at all: no notice", body: meBody("")},
	// pct next to a healthy wallet is ignored too.
	{name: "pct 100 beside a paid wallet: no notice", body: meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z",` + wallet("18", tariffT1, "true", "9"))},
	// The weekly limit (void-board#224), under its own "limit": warned from
	// 80% used, whatever the wallet says, and on its own line after the
	// wallet's notice when there is one.
	{name: "limit 42 pct beside a paid wallet: no notice", body: meBody(limitMember(42) + "," + wallet("18", tariffT1, "true", "9"))},
	{name: "limit 79.9 pct: no notice", body: meBody(limitMember(79.9) + "," + wallet("18", tariffT1, "true", "9"))},
	{name: "limit 80 pct: limit notice", body: meBody(limitMember(80) + "," + wallet("18", tariffT1, "true", "9")), notice: limitNotice(80)},
	{name: "limit 85 pct: limit notice", body: meBody(limitMember(85) + "," + wallet("18", tariffT1, "true", "9")), notice: limitNotice(85)},
	{name: "limit 130 pct: limit notice says 100 pct", body: meBody(limitMember(130) + "," + wallet("18", tariffT1, "true", "9")), notice: limitNotice(100)},
	{name: "limit 85 pct and no wallet: limit notice", body: meBody(limitMember(85)), notice: limitNotice(85)},
	{name: "limit 85 pct beside a tariff without dailyRateUsd: limit notice", body: meBody(limitMember(85) + "," + wallet("18", `{"tier":"t1","monthlyPriceUsd":60}`, "true", "9")), notice: limitNotice(85)},
	{name: "limit 85 pct and a low balance: both, the wallet's first", body: meBody(limitMember(85) + "," + wallet("2", tariffT1, "true", "1")), notice: walletLowNotice(1) + "\n" + limitNotice(85)},
	{name: "limit pct as a string: no notice", body: meBody(`"limit":{"pct":"85"},` + wallet("18", tariffT1, "true", "9"))},
	// T3 has no higher tier to upgrade to: its warning says when the limit
	// resets instead of linking the pay page. T1/T2 keep the link.
	{name: "limit 85 pct on T3: resets, no link", body: meBody(limitMember(85) + "," + wallet("18", tariffT3, "true", "9")), notice: "Weekly limit 85% used — resets in 3 days"},
	{name: "limit 85 pct on T3 sent as upper case: resets, no link", body: meBody(limitMember(85) + "," + wallet("18", `{"tier":"T3"}`, "true", "9")), notice: "Weekly limit 85% used — resets in 3 days"},
	{name: "limit 85 pct on T3, no reset sent: the share alone", body: meBody(`"limit":{"pct":85},` + wallet("18", tariffT3, "true", "9")), notice: "Weekly limit 85% used"},
	{name: "limit 85 pct on T2: limit notice with the link", body: meBody(limitMember(85) + "," + wallet("18", tariffT2, "true", "9")), notice: limitNotice(85)},
	{name: "limit 42 pct on T3: no notice", body: meBody(limitMember(42) + "," + wallet("18", tariffT3, "true", "9"))},
	// A tariff without its prices (void-board#224): no daily rate to check
	// the daily rule against, so no refusal notice; the low-days one stands.
	{name: "no dailyRateUsd, unpaid, 1 day: low notice, no refusal", body: meBody(wallet("0.5", `{"tier":"t1"}`, "false", "1")), notice: walletLowNotice(1)},
	{name: "no prices, paid, 9 days: no notice", body: meBody(wallet("18", `{"tier":"t1"}`, "true", "9"))},
}

// limitMember is Relay's "limit" member with the reset three days and an hour away.
func limitMember(pct float64) string {
	return fmt.Sprintf(`"limit":{"pct":%v,"resetAt":%q}`, pct, time.Now().Add(73*time.Hour).UTC().Format(time.RFC3339))
}

func limitNotice(pct int) string {
	return fmt.Sprintf("Weekly limit %d%% used — upgrade: https://profile.makscee.ru/vc/pay", pct)
}

// staleLaunchNotice is planted in vc's own environment by every launch test.
// It stands for a notice some earlier vc (or a vc that started this one)
// computed for another wallet: Pi must never show it.
const staleLaunchNotice = "stale launch notice inherited from the parent — must not reach Pi"

// assertLaunchSilentAboutMoney checks what vc itself printed before Pi: nothing
// about money, whatever the wallet says. Pi's fullscreen mode clears the
// screen, so a line printed here is a line nobody reads.
func assertLaunchSilentAboutMoney(t *testing.T, stream string) {
	t.Helper()
	for _, word := range []string{"Balance", "/vc/pay", "@makscee", "%", "udget"} {
		if strings.Contains(stream, word) {
			t.Errorf("vc printed %q before Pi started — the launch notice belongs inside Pi (VC_LAUNCH_NOTICE), where fullscreen cannot wipe it:\n%s", word, stream)
		}
	}
}

// assertLaunchNoticeEnv checks the one channel the notice may take to Pi:
// exactly one VC_LAUNCH_NOTICE entry carrying want, or none when want is "".
// A stale value from vc's own environment must be gone either way.
func assertLaunchNoticeEnv(t *testing.T, env []string, want string) {
	t.Helper()
	var got []string
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, launchNoticeEnv) {
			got = append(got, value)
		}
		if strings.Contains(entry, staleLaunchNotice) {
			t.Errorf("Pi's environment carries the launch notice vc inherited from its parent: %q", entry)
		}
	}
	switch {
	case want == "" && len(got) != 0:
		t.Errorf("%s = %q reaches Pi, want no notice for this wallet", launchNoticeEnv, got)
	case want != "" && (len(got) != 1 || got[0] != want):
		t.Errorf("%s in Pi's environment = %q, want exactly [%q]", launchNoticeEnv, got, want)
	}
}

// `vc` from a terminal: runSpawn is the last step before Pi.
func TestTerminalLaunchFollowsWalletRules(t *testing.T) {
	for _, tc := range walletGateCases {
		t.Run(tc.name, func(t *testing.T) { assertTerminalLaunch(t, tc) })
	}
}

// assertTerminalLaunch runs `vc` from a terminal against tc.body and checks
// that Pi starts with exactly tc.notice, and that vc printed nothing about
// money before it.
func assertTerminalLaunch(t *testing.T, tc walletGateCase) {
	t.Helper()
	home, _ := preparePiPathLaunch(t)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "0")
	t.Setenv(config.EnvAccessCheckHost, meServer(t, tc.body))
	t.Setenv(launchNoticeEnv, staleLaunchNotice)

	spawned := false
	var piEnv []string
	exitCode := -1
	savedSpawn, savedExit := spawnHarness, exitProcess
	spawnHarness = func(_ context.Context, _ string, _ []string, env []string) error {
		spawned = true
		piEnv = env
		return nil
	}
	exitProcess = func(code int) { exitCode = code }
	t.Cleanup(func() { spawnHarness, exitProcess = savedSpawn, savedExit })

	stopStderr := captureProcessStderr(t)
	err := runSpawn(nil, nil)
	stderr := plainText(stopStderr())

	if !spawned {
		t.Fatalf("Pi was not started (exit=%d, err=%v) — the client never refuses a launch over the wallet, Relay does; stderr:\n%s", exitCode, err, stderr)
	}
	if err != nil || exitCode != -1 {
		t.Errorf("launch failed: exit=%d err=%v", exitCode, err)
	}
	assertLaunchNoticeEnv(t, piEnv, tc.notice)
	assertLaunchSilentAboutMoney(t, stderr)
}

// The desktop app starts Pi through `vc desktop-session`; same rules. A
// refused desktop-session exits, and the app then hides the terminal behind
// "Chat stopped… check your network" — so it must not refuse either, and the
// notice travels to Pi in the plan's environment, not on the command's error
// stream (which the app shows in the terminal Pi's fullscreen then clears).
func TestDesktopSessionFollowsWalletRules(t *testing.T) {
	for _, tc := range walletGateCases {
		t.Run(tc.name, func(t *testing.T) { assertDesktopSessionLaunch(t, tc) })
	}
}

// assertDesktopSessionLaunch runs `vc desktop-session` against tc.body, with
// the real auth gate, and checks that Pi's plan carries exactly tc.notice and
// that nothing about money went to the command's error stream.
func assertDesktopSessionLaunch(t *testing.T, tc walletGateCase) {
	t.Helper()
	piSettingsSandbox(t)
	t.Setenv(launchNoticeEnv, staleLaunchNotice)
	host := meServer(t, tc.body)
	node, pi := desktopFiles(t)
	ran := false
	var plan desktopSessionPlan
	deps := desktopSessionDeps{
		loadToken: func() (string, error) { return "token", nil },
		resolveConfig: func() config.Config {
			return config.Config{AuthHost: "http://auth.invalid", AccessCheckHost: host, RelayScheme: "https", RelayHost: "relay.invalid"}
		},
		authGate:        authGate, // the real gate, against the fixture server
		resolveCA:       func(config.Config) (string, error) { return "/ca.pem", nil },
		reconcilePi:     func() (string, error) { return "/managed.ts", nil },
		reconcileSearch: func(bool) (managedWebSearchState, error) { return managedWebSearchReady, nil },
		now:             time.Now,
		run: func(_ context.Context, p desktopSessionPlan, _ io.Reader, _ io.Writer, _ io.Writer) error {
			ran = true
			plan = p
			return nil
		},
	}
	cmd := newDesktopSessionCommand(deps)
	var errOut bytes.Buffer
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--node", node, "--pi-entry", pi})
	err := cmd.Execute()
	stream := plainText(errOut.String())

	if err != nil || !ran {
		t.Fatalf("desktop session did not start Pi (ran=%v): %v — the client never refuses a launch over the wallet, Relay does\n%s", ran, err, stream)
	}
	assertLaunchNoticeEnv(t, plan.env, tc.notice)
	assertLaunchSilentAboutMoney(t, stream)
}

// The strip itself, at the function both launch paths build Pi's environment
// with: VC_LAUNCH_NOTICE is vc's to set, like every other VC_* seam, and an
// inherited one is dropped before vc decides whether to add its own.
func TestPiSpawnEnvDropsInheritedLaunchNotice(t *testing.T) {
	env := buildPiSpawnEnv(providerRelay(), []string{"HOME=/home/person", launchNoticeEnv + "=" + staleLaunchNotice}, "https", "relay.test", "secret", "/ca.pem")
	for _, entry := range env {
		if name, _, _ := strings.Cut(entry, "="); strings.EqualFold(name, launchNoticeEnv) {
			t.Fatalf("buildPiSpawnEnv passed an inherited %s through to Pi: %q", launchNoticeEnv, entry)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "HOME=/home/person") {
		t.Fatalf("buildPiSpawnEnv dropped an ordinary variable too; the strip is not specific: %q", env)
	}
}

// ─── vc status --json: launchNotice ─────────────────────────────────────────

// The desktop reads its notice from `vc status --json`: the same notice, by the
// same rules, as a string or null. A refusal notice is still a signed-in
// status — the account is fine, today is not paid — never an error state.
func TestStatusJSONCarriesLaunchNotice(t *testing.T) {
	for _, tc := range walletGateCases {
		t.Run(tc.name, func(t *testing.T) { assertStatusJSONLaunchNotice(t, tc) })
	}
}

// assertStatusJSONLaunchNotice checks `vc status --json` against tc.body: a
// signed-in status whose launchNotice is exactly tc.notice, or null.
func assertStatusJSONLaunchNotice(t *testing.T, tc walletGateCase) {
	t.Helper()
	obj := jsonStatus(t, tc.body) // asserts authState == signed_in
	got, present := obj["launchNotice"]
	if tc.notice == "" {
		if got != nil {
			t.Errorf("launchNotice = %#v, want null or absent for this wallet", got)
		}
	} else if s, ok := got.(string); !ok || s != tc.notice {
		t.Errorf("launchNotice = %#v (present=%v), want %q", got, present, tc.notice)
	}
	if _, present := obj["error"]; present {
		t.Errorf("a signed-in status carries error = %v; a launch notice is not an error", obj["error"])
	}
}

// ─── display ────────────────────────────────────────────────────────────────

// No money on screen (void-board#224), whatever the balance: a debt or a
// fraction of a cent changes nothing in the line, and days never go below
// zero.
func TestStatusBalanceDisplayRules(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"negative balance with a tariff", meBody(wallet("-3", tariffT1, "false", "-2")), "plan: T1 · осталось ~0 дней"},
		{"negative days, positive balance", meBody(wallet("0.5", tariffT1, "true", "-1")), "plan: T1 · осталось ~0 дней"},
		{"a fraction of a cent: no money shown", meBody(wallet("7.666", tariffT3, "true", "0")), "plan: T3 · осталось ~0 дней"},
		{"99.9 cents: no money shown", meBody(wallet("18.999", tariffT1, "true", "9")), "plan: T1 · осталось ~9 дней"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := humanStatus(t, tc.body)
			got, ok := statusLine(out, "plan:")
			if !ok {
				t.Fatalf("vc status has no plan line:\n%s", out)
			}
			if got != tc.want {
				t.Errorf("plan line = %q, want %q", got, tc.want)
			}
		})
	}
}

// ─── welcome screen ─────────────────────────────────────────────────────────

func fetchMeFrom(t *testing.T, body string) auth.MeResult {
	t.Helper()
	host := meServer(t, body)
	me, err := auth.FetchMe(host, "tok", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("FetchMe: %v", err)
	}
	return me
}

func welcomeScreens(state welcome.AuthState) (view, banner string) {
	return plainText(welcome.NewMenuModelForTest(state).View()), plainText(welcome.PlainBannerForTest(state))
}

// Where the welcome screen said `$X left` it now says what `vc status` says
// after its label.
func TestWelcomeShowsBalanceTierAndDays(t *testing.T) {
	view, banner := welcomeScreens(meResultToState(fetchMeFrom(t, meBody(wallet("18", tariffT1, "true", "9")))))
	for where, screen := range map[string]string{"menu": view, "plain banner": banner} {
		if !strings.Contains(screen, "T1 · осталось ~9 дней") {
			t.Errorf("welcome %s lacks %q:\n%s", where, "T1 · осталось ~9 дней", screen)
		}
		if strings.Contains(screen, "$18.00 left") {
			t.Errorf("welcome %s still shows the old `$X left`:\n%s", where, screen)
		}
		assertNoPercent(t, "welcome "+where, screen)
	}
}

// No tariff: nothing to show but money, so the welcome screen shows no wallet.
func TestWelcomeShowsNoMoneyWithoutTariff(t *testing.T) {
	view, banner := welcomeScreens(meResultToState(fetchMeFrom(t, meBody(wallet("18", "null", "null", "null")))))
	for where, screen := range map[string]string{"menu": view, "plain banner": banner} {
		for _, stale := range []string{"$", "days left", "осталось"} {
			if strings.Contains(screen, stale) {
				t.Errorf("welcome %s shows %q without a tariff:\n%s", where, stale, screen)
			}
		}
	}
}

// Old servers: no wallet, no money on the welcome screen — the void-auth era
// top-level balanceUsd included.
func TestWelcomeShowsNoMoneyWithoutWallet(t *testing.T) {
	for _, body := range []string{
		meBody(`"balanceUsd":12.4`),
		meBody(`"pct":42,"resetAt":"2026-10-01T00:00:00Z","balanceUsd":12.4`),
	} {
		view, banner := welcomeScreens(meResultToState(fetchMeFrom(t, body)))
		for where, screen := range map[string]string{"menu": view, "plain banner": banner} {
			if strings.Contains(screen, "$") || strings.Contains(screen, "%") {
				t.Errorf("welcome %s shows money or percent with no wallet on the wire (%s):\n%s", where, body, screen)
			}
		}
	}
}

// A stale identity is only an identity: the wallet it came with is not
// current, so it is not shown.
func TestWelcomeStaleStateShowsNoWallet(t *testing.T) {
	me := fetchMeFrom(t, meBody(wallet("18", tariffT1, "true", "9")))
	view, banner := welcomeScreens(staleMeResultToState(me))
	for where, screen := range map[string]string{"menu": view, "plain banner": banner} {
		if strings.Contains(screen, "18.00") || strings.Contains(screen, "days left") {
			t.Errorf("stale welcome %s presents the last wallet as current:\n%s", where, screen)
		}
	}
}
