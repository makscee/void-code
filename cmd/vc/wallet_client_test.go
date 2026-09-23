package main

import (
	"bytes"
	"context"
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
//  2. `vc status` prints `balance: $18.00 · T1 · ~9 days left` (tariff) or
//     `balance: $18.00` (no tariff), nothing without a wallet; --json carries
//     `wallet` mirroring the server.
//  3. The welcome screen shows the same text where it showed `$X left`.
//  4. Launch (terminal and desktop-session): tariff + todayPaid === false +
//     balanceUsd < tariff.dailyRateUsd → refused with walletBlockMessage.
//     An unpaid day that the balance still covers is NOT refused: right after
//     00:00 UTC the daily charge may simply not have run yet (spec, "Поправка
//     23.09"). Tariff + fundedDays <= 2 (and not refused) → walletLowMessage,
//     the session still starts. Anything else → silence.
//
// Every test here drives an existing seam (runStatus, runStatusJSON, runSpawn,
// the desktop-session command, meResultToState) through a real /v1/vc/me body,
// so this file compiles against HEAD and says nothing about how the wallet is
// carried inside the client.

const walletBlockMessage = "Balance is not enough for today — message @makscee on Telegram to top up."

func walletLowMessage(days string) string {
	return "Balance low — " + days + " days left. Message @makscee on Telegram to top up."
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
		if l := strings.TrimSpace(line); strings.HasPrefix(l, label) {
			return l, true
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
		{"t1", meBody(wallet("18", tariffT1, "true", "9")), "balance: $18.00 · T1 · ~9 days left"},
		{"t2", meBody(wallet("20.5", tariffT2, "true", "5")), "balance: $20.50 · T2 · ~5 days left"},
		// The server that still sends the retired budget next to the wallet:
		// the wallet line prints, the percentage does not.
		{"t1 with retired pct alongside", meBody(`"pct":77,"resetAt":"2026-10-01T00:00:00Z",` + wallet("18", tariffT1, "true", "9")), "balance: $18.00 · T1 · ~9 days left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := humanStatus(t, tc.body)
			got, ok := statusLine(out, "balance:")
			if !ok {
				t.Fatalf("vc status has no balance line:\n%s", out)
			}
			if got != tc.want {
				t.Errorf("balance line = %q, want %q", got, tc.want)
			}
			if _, ok := statusLine(out, "budget:"); ok {
				t.Errorf("vc status still prints a budget line:\n%s", out)
			}
			assertNoPercent(t, "vc status", out)
		})
	}
}

// Without a tariff there are no days to count: the balance stands alone.
func TestStatusShowsBareBalanceWithoutTariff(t *testing.T) {
	out := humanStatus(t, meBody(wallet("18", "null", "null", "null")))
	got, ok := statusLine(out, "balance:")
	if !ok {
		t.Fatalf("vc status has no balance line:\n%s", out)
	}
	if got != "balance: $18.00" {
		t.Errorf("balance line = %q, want %q", got, "balance: $18.00")
	}
	assertNoPercent(t, "vc status", out)
}

// Old servers: no wallet means no balance line — neither from the retired
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
			for _, label := range []string{"balance:", "budget:"} {
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
// the server's names — tier included, unformatted.
func TestStatusJSONMirrorsWallet(t *testing.T) {
	obj := jsonStatus(t, meBody(`"pct":42.5,"resetAt":"2026-10-01T00:00:00Z",`+wallet("18", tariffT1, "true", "9")))
	w := jsonWallet(t, obj)
	if w["balanceUsd"] != 18.0 {
		t.Errorf("wallet.balanceUsd = %v, want 18", w["balanceUsd"])
	}
	tariff, ok := w["tariff"].(map[string]any)
	if !ok {
		t.Fatalf("wallet.tariff = %#v, want an object", w["tariff"])
	}
	if tariff["tier"] != "t1" || tariff["monthlyPriceUsd"] != 60.0 || tariff["dailyRateUsd"] != 2.0 {
		t.Errorf("wallet.tariff = %v, want {tier:t1 monthlyPriceUsd:60 dailyRateUsd:2}", tariff)
	}
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
	if tariff, _ := w["tariff"].(map[string]any); tariff == nil || tariff["dailyRateUsd"] != 7.67 {
		t.Errorf("wallet.tariff = %v, want t3 at 7.67", w["tariff"])
	}
}

func TestStatusJSONWalletWithoutTariff(t *testing.T) {
	w := jsonWallet(t, jsonStatus(t, meBody(wallet("18", "null", "null", "null"))))
	if w["balanceUsd"] != 18.0 {
		t.Errorf("wallet.balanceUsd = %v, want 18", w["balanceUsd"])
	}
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
	name  string
	body  string
	block bool
	// warn lists substrings the launch must print; empty means the launch
	// must say nothing about money at all.
	warn []string
}

var walletGateCases = []walletGateCase{
	// Refused: the day is unpaid AND the balance cannot pay it.
	{name: "t1, today unpaid, balance under the rate: refused", body: meBody(wallet("1.5", tariffT1, "false", "0")), block: true},
	{name: "t3, today unpaid, balance under the rate: refused", body: meBody(wallet("3", tariffT3, "false", "0")), block: true},
	{name: "t3, today unpaid, a cent under the rate: refused", body: meBody(wallet("7.66", tariffT3, "false", "0")), block: true},
	{name: "t1, today unpaid, zero balance: refused", body: meBody(wallet("0", tariffT1, "false", "0")), block: true},
	// Proceed: the day is unpaid but the balance covers it — the charge has
	// not run yet (just after 00:00 UTC). No refusal; the warning still follows
	// fundedDays.
	{name: "today unpaid, balance covers it, 9 days: silent", body: meBody(wallet("18", tariffT1, "false", "9"))},
	{name: "today unpaid, balance exactly the rate: proceeds, warned", body: meBody(wallet("2", tariffT1, "false", "1")), warn: []string{"Balance low — 1 ", "Message @makscee on Telegram to top up."}},
	{name: "t3, today unpaid, balance exactly the rate: proceeds", body: meBody(wallet("7.67", tariffT3, "false", "1")), warn: []string{"Balance low — 1 ", "Message @makscee on Telegram to top up."}},
	{name: "today unpaid, balance covers it, 2 days: proceeds, warned", body: meBody(wallet("4", tariffT1, "false", "2")), warn: []string{walletLowMessage("2")}},
	// Paid: warned at fundedDays <= 2, silent above.
	{name: "today paid, 2 days left: warned", body: meBody(wallet("4", tariffT1, "true", "2")), warn: []string{walletLowMessage("2")}},
	{name: "today paid, 0 days left: warned", body: meBody(wallet("1", tariffT1, "true", "0")), warn: []string{walletLowMessage("0")}},
	// 1 is inside the band; the singular/plural spelling is not pinned.
	{name: "today paid, 1 day left: warned", body: meBody(wallet("2", tariffT1, "true", "1")), warn: []string{"Balance low — 1 ", "Message @makscee on Telegram to top up."}},
	{name: "today paid, 3 days left: silent", body: meBody(wallet("6", tariffT1, "true", "3"))},
	{name: "today paid, 9 days left: silent", body: meBody(wallet("18", tariffT1, "true", "9"))},
	// The block needs a tariff; Keys never says todayPaid:false without one, but
	// if something does, there is no daily charge to be behind on.
	{name: "no tariff, todayPaid false, zero balance: silent", body: meBody(wallet("0", "null", "false", "null"))},
	{name: "no tariff: silent", body: meBody(wallet("0.25", "null", "null", "null"))},
	// An inconsistent payload — balance and fundedDays both real zeros, with
	// no tariff — still enforces nothing: no tariff means no decision at all,
	// whatever todayPaid and fundedDays say. fundedDays:0 (not null) matters
	// here — a low-balance message built from a real day count would show up
	// in the output the moment the "no tariff" guard is skipped.
	{name: "no tariff, todayPaid false, zero balance, zero days: silent", body: meBody(wallet("0", "null", "false", "0"))},
	{name: "no tariff, todayPaid null, fundedDays null, zero balance: silent", body: meBody(wallet("0", "null", "null", "null"))},
	// null is not false.
	{name: "tariff, todayPaid null, balance under the rate: not refused", body: meBody(wallet("1", tariffT1, "null", "9"))},
	// A wallet the client cannot read neither blocks nor warns.
	{name: "todayPaid as a string: silent", body: meBody(wallet("1.5", tariffT1, `"false"`, "0"))},
	// Old servers: pct is ignored — no refusal at the cap, no warning under it.
	{name: "old server, pct 100: silent", body: meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "old server, pct 150: silent", body: meBody(`"pct":150,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "old server, pct 85: silent", body: meBody(`"pct":85,"resetAt":"2026-10-01T00:00:00Z"`)},
	{name: "no wallet at all: silent", body: meBody("")},
	// pct next to a healthy wallet is ignored too.
	{name: "pct 100 beside a paid wallet: silent", body: meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z",` + wallet("18", tariffT1, "true", "9"))},
}

func assertLaunchSilentAboutMoney(t *testing.T, stream string) {
	t.Helper()
	for _, word := range []string{"Balance", "@makscee", "%", "udget"} {
		if strings.Contains(stream, word) {
			t.Errorf("launch output mentions %q, want nothing about money:\n%s", word, stream)
		}
	}
}

// `vc` from a terminal: runSpawn is the gate before Pi.
func TestTerminalLaunchFollowsWalletRules(t *testing.T) {
	for _, tc := range walletGateCases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := preparePiPathLaunch(t)
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
			t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "0")
			t.Setenv(config.EnvAccessCheckHost, meServer(t, tc.body))

			spawned := false
			exitCode := -1
			savedSpawn, savedExit := spawnHarness, exitProcess
			spawnHarness = func(context.Context, string, []string, []string) error { spawned = true; return nil }
			exitProcess = func(code int) { exitCode = code }
			t.Cleanup(func() { spawnHarness, exitProcess = savedSpawn, savedExit })

			stopStderr := captureProcessStderr(t)
			err := runSpawn(nil, nil)
			stderr := plainText(stopStderr())

			if tc.block {
				if spawned {
					t.Fatalf("Pi was started although today is unpaid and the balance cannot pay it; stderr:\n%s", stderr)
				}
				if exitCode == 0 || (exitCode == -1 && err == nil) {
					t.Errorf("refused launch reported success (exit=%d, err=%v)", exitCode, err)
				}
				if !strings.Contains(stderr, walletBlockMessage) {
					t.Errorf("stderr does not carry the refusal %q:\n%s", walletBlockMessage, stderr)
				}
				assertNoPercent(t, "launch", stderr)
				return
			}
			if !spawned {
				t.Fatalf("Pi was not started (exit=%d, err=%v); stderr:\n%s", exitCode, err, stderr)
			}
			if err != nil || exitCode != -1 {
				t.Errorf("launch failed: exit=%d err=%v", exitCode, err)
			}
			if len(tc.warn) == 0 {
				assertLaunchSilentAboutMoney(t, stderr)
				return
			}
			for _, want := range tc.warn {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, walletBlockMessage) {
				t.Errorf("a warned launch printed the refusal text:\n%s", stderr)
			}
			assertNoPercent(t, "launch", stderr)
		})
	}
}

// The desktop app starts Pi through `vc desktop-session`; same rules, and the
// words go where the app reads them — the command's own error stream for the
// warning, the command's error for the refusal.
func TestDesktopSessionFollowsWalletRules(t *testing.T) {
	for _, tc := range walletGateCases {
		t.Run(tc.name, func(t *testing.T) {
			piSettingsSandbox(t)
			host := meServer(t, tc.body)
			node, pi := desktopFiles(t)
			ran := false
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
				run: func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
					ran = true
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

			if tc.block {
				if ran {
					t.Fatalf("Pi was started although today is unpaid and the balance cannot pay it; stream:\n%s", stream)
				}
				if err == nil || !strings.Contains(err.Error(), walletBlockMessage) {
					t.Fatalf("err = %v, want the refusal %q", err, walletBlockMessage)
				}
				assertNoPercent(t, "desktop-session", err.Error())
				return
			}
			if err != nil || !ran {
				t.Fatalf("desktop session did not start (ran=%v): %v\n%s", ran, err, stream)
			}
			if len(tc.warn) == 0 {
				assertLaunchSilentAboutMoney(t, stream)
				return
			}
			for _, want := range tc.warn {
				if !strings.Contains(stream, want) {
					t.Errorf("desktop-session error stream lacks %q:\n%s", want, stream)
				}
			}
			assertNoPercent(t, "desktop-session", stream)
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
		if !strings.Contains(screen, "$18.00 · T1 · ~9 days left") {
			t.Errorf("welcome %s lacks %q:\n%s", where, "$18.00 · T1 · ~9 days left", screen)
		}
		if strings.Contains(screen, "$18.00 left") {
			t.Errorf("welcome %s still shows the old `$X left`:\n%s", where, screen)
		}
		assertNoPercent(t, "welcome "+where, screen)
	}
}

func TestWelcomeShowsBareBalanceWithoutTariff(t *testing.T) {
	view, banner := welcomeScreens(meResultToState(fetchMeFrom(t, meBody(wallet("18", "null", "null", "null")))))
	for where, screen := range map[string]string{"menu": view, "plain banner": banner} {
		if !strings.Contains(screen, "$18.00") {
			t.Errorf("welcome %s lacks the balance:\n%s", where, screen)
		}
		for _, stale := range []string{"$18.00 left", "days left"} {
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
