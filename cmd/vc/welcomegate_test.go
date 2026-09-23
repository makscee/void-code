package main

import (
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

func TestStaleMeResultToStateUsesTruthfulIdentityCopy(t *testing.T) {
	tests := []struct {
		name string
		me   auth.MeResult
		want string
	}{
		{name: "last known user", me: auth.MeResult{UserID: "user-last"}, want: "user-last"},
		{name: "no identity history", me: auth.MeResult{}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := staleMeResultToState(tc.me)
			if !state.LoggedIn || !state.IdentityUnverified || state.Identity != tc.want {
				t.Fatal("transient state did not preserve truthful verification status")
			}
			// "No stale money on screen" is asserted on what the screen
			// renders, for a MeResult that really carries a wallet, in
			// TestWelcomeStaleStateShowsNoWallet (wallet_client_test.go).
			if view, banner := welcomeScreens(state); strings.Contains(view+banner, "$") {
				t.Fatalf("stale response presents money as current:\n%s\n%s", view, banner)
			}
		})
	}
}

// The wallet a verified /v1/vc/me answer carries reaches the welcome screen.
// Built from a real answer rather than a literal MeResult, so the test holds
// whatever shape the wallet takes inside the client.
func TestMeResultToState_CarriesBalance(t *testing.T) {
	st := meResultToState(fetchMeFrom(t, `{"email":"a@b.com","wallet":{"balanceUsd":9.99,"tariff":null,"todayPaid":null,"fundedDays":null}}`))
	view, banner := welcomeScreens(st)
	if !strings.Contains(view, "$9.99") || !strings.Contains(banner, "$9.99") {
		t.Errorf("meResultToState dropped the wallet balance:\n%s\n%s", view, banner)
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
