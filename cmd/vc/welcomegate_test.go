package main

import (
	"testing"

	"github.com/makscee/void-code/internal/auth"
)

// TestMeResultToState_CarriesBalance verifies that BalanceUsd flows through.
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
			if state.BalanceUsd != nil {
				t.Fatal("stale response must not present stale balance as current")
			}
		})
	}
}

func TestMeResultToState_CarriesBalance(t *testing.T) {
	bal := 9.99
	st := meResultToState(auth.MeResult{Email: "a@b.com", BalanceUsd: &bal})
	if st.BalanceUsd == nil || *st.BalanceUsd != 9.99 {
		t.Errorf("meResultToState dropped BalanceUsd: %+v", st.BalanceUsd)
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
