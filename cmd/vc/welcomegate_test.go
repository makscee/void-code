package main

import (
	"strings"
	"testing"
)

// The wallet a verified /v1/vc/me answer carries reaches the welcome screen.
// Built from a real answer rather than a literal MeResult, so the test holds
// whatever shape the wallet takes inside the client.
func TestVerifiedWelcomeState_CarriesBalance(t *testing.T) {
	st := verifiedWelcomeState(fetchMeFrom(t, `{"email":"a@b.com","wallet":{"balanceUsd":9.99,"tariff":{"tier":"t2","monthlyPriceUsd":120,"dailyRateUsd":4},"todayPaid":true,"fundedDays":4}}`))
	view, banner := welcomeScreens(st)
	if !strings.Contains(view, "T2 · осталось ~4 дня") || !strings.Contains(banner, "T2 · осталось ~4 дня") {
		t.Errorf("the welcome state dropped the wallet balance:\n%s\n%s", view, banner)
	}
}

// TestDecideGate locks the bare-launch gating logic. The regression it guards
// against: a non-TTY caller (e.g. void-os spawns `vc -- --session-id … -p …`
// with stdin set to /dev/null) must NOT enter the interactive welcome screen.
// The welcome bubbletea program waits for a keypress that a non-TTY stdin can
// never deliver, so it hangs forever and claude never starts.
func TestDecideGate(t *testing.T) {
	cases := []struct {
		name     string
		stdinTTY bool
		loggedIn bool
		want     gateDecision
	}{
		// Interactive terminal: always show the landing screen (login flow
		// handled afterwards when not logged in).
		{"tty logged-in", true, true, gateShowWelcome},
		{"tty logged-out", true, false, gateShowWelcome},
		// Non-TTY + logged in: skip welcome, go straight to spawning claude.
		// THIS is the void-os hang regression.
		{"nontty logged-in", false, true, gateSpawn},
		// Non-TTY + logged out: cannot present login picker, fail fast.
		{"nontty logged-out", false, false, gateFailAuth},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideGate(c.stdinTTY, c.loggedIn)
			if got != c.want {
				t.Errorf("decideGate(stdinTTY=%v, loggedIn=%v) = %v, want %v",
					c.stdinTTY, c.loggedIn, got, c.want)
			}
		})
	}
}
