package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/welcome"
	"github.com/spf13/cobra"
)

// The welcome screen in production — spec 2026-09-23-client-wallet-days,
// amendment "после панели void-code#76" §4: the welcome screen takes the
// wallet from the background /v1/vc/me the launch already makes
// (startLaunchPreflight).
//
// Until now the only function that put a wallet on the welcome state was
// meResultToState, and production never calls it. main() draws the screen from
// resolveLocalAuthStateWithSource — a token-only state with no wallet — and the
// preflight's /v1/vc/me answer is read by nobody on the way to the screen. The
// welcome tests in wallet_client_test.go go through meResultToState and so
// prove the formatting, not that anyone sees it.
//
// These tests replay main's bare launch from the outside with its own pieces:
// the local state from resolveLocalAuthStateWithSource; the preflight from
// startLaunchPreflight with the production deps (only the update check is
// silenced), installed in currentLaunchPreflight exactly as main installs it;
// and the welcome program run by runWelcomeCommandTransition — the call inside
// main's menu loop — with its terminal swapped for a pipe through
// welcomeProgramOptions. Nothing is asserted about how the wallet travels, only
// about what the screen shows.
//
// The late case is the one that matters. main starts the preflight and draws
// the screen within the same millisecond, and /v1/vc/me costs a network round
// trip — so in real use the answer lands while the screen is already up. A
// screen that consults the preflight only before its first frame would still
// show the wallet to nobody.

// lockedBuffer is the welcome program's terminal: bubbletea writes from its own
// goroutine while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// welcomeLaunch starts the background preflight the way main does for a
// signed-in bare `vc`, against host as the access-check service, and returns
// the local state main hands to the first screen.
func welcomeLaunch(t *testing.T, host string) (state welcome.AuthState, token, authHost string) {
	t.Helper()
	signedIn(t)
	t.Setenv(config.EnvAccessCheckHost, host)
	state, token, authHost, _ = resolveLocalAuthStateWithSource()
	if !state.LoggedIn {
		t.Fatalf("local state is not signed in: %+v", state)
	}
	deps := defaultLaunchPreflightDeps()
	deps.update = func() string { return "" } // the update check is not under test and must not touch the network
	deps.diagnostics = newLaunchDiagnostics(false, time.Now, io.Discard)
	saved := currentLaunchPreflight
	currentLaunchPreflight = startLaunchPreflight(token, authHost, true, deps)
	t.Cleanup(func() { currentLaunchPreflight = saved })
	return state, token, authHost
}

type welcomeSession struct {
	t      *testing.T
	screen *lockedBuffer
	keys   *io.PipeWriter
	done   chan struct{}
	once   sync.Once
	result welcome.RunResult
	err    error
}

// showWelcome runs the production welcome program for state, as main's menu
// loop does, on a pipe instead of a terminal.
func showWelcome(t *testing.T, state welcome.AuthState) *welcomeSession {
	t.Helper()
	keysIn, keys := io.Pipe()
	s := &welcomeSession{t: t, screen: &lockedBuffer{}, keys: keys, done: make(chan struct{})}
	savedOptions := welcomeProgramOptions
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(keysIn), tea.WithOutput(s.screen), tea.WithoutSignalHandler()}
	spawnPi := &cobra.Command{Use: "vc", SilenceUsage: true, SilenceErrors: true, RunE: func(*cobra.Command, []string) error {
		t.Error("the welcome screen started Pi; the test only ever pressed q")
		return nil
	}}
	go func() {
		defer close(s.done)
		s.result, s.err = runWelcomeCommandTransition(state, welcome.Callbacks{}, spawnPi, nil)
	}()
	t.Cleanup(func() {
		s.quit()
		welcomeProgramOptions = savedOptions
	})
	return s
}

// waitFor polls what the screen has drawn so far until it contains want.
func (s *welcomeSession) waitFor(want string, within time.Duration) (string, bool) {
	deadline := time.Now().Add(within)
	for {
		screen := plainText(s.screen.String())
		if strings.Contains(screen, want) {
			return screen, true
		}
		if time.Now().After(deadline) {
			return screen, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *welcomeSession) quit() {
	s.once.Do(func() {
		go func() { _, _ = s.keys.Write([]byte("q")) }()
		select {
		case <-s.done:
			if s.err != nil || s.result != welcome.Quit {
				s.t.Errorf("welcome program ended with (%v, %v), want Quit on q", s.result, s.err)
			}
		case <-time.After(5 * time.Second):
			s.t.Error("the welcome program did not quit on q")
		}
		_ = s.keys.Close()
	})
}

// slowMeServer answers /v1/vc/me with body only once release is called — the
// network round trip the screen must not wait for.
func slowMeServer(t *testing.T, body string) (host string, release func()) {
	t.Helper()
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(release) // runs first: never leave a handler blocking Close
	return srv.URL, release
}

const welcomeMenuPrompt = "What now?"

// The production case: the screen is drawn at once, /v1/vc/me answers a moment
// later with a wallet, and the screen that is up shows it.
func TestWelcomeShowsWalletThatArrivesWhileTheScreenIsUp(t *testing.T) {
	host, release := slowMeServer(t, meBody(wallet("18", tariffT1, "true", "9")))
	state, _, _ := welcomeLaunch(t, host)
	s := showWelcome(t, state)

	// The screen never waits on the network (main.go: the local state exists
	// "so the welcome screen can render before any optional network request
	// completes"), and before the answer there is no money to show.
	screen, drawn := s.waitFor(welcomeMenuPrompt, time.Second)
	if !drawn {
		t.Fatalf("the welcome screen was not drawn within 1s while /v1/vc/me was still in flight:\n%s", screen)
	}
	if strings.Contains(screen, "$") {
		t.Fatalf("the welcome screen shows money before /v1/vc/me answered:\n%s", screen)
	}

	release()
	if screen, ok := s.waitFor("$18.00 · T1 · ~9 days left", 3*time.Second); !ok {
		t.Fatalf("/v1/vc/me answered with a wallet while the welcome screen was up, and the screen never showed it (spec amendment §4: the welcome screen takes the wallet from the launch's background /v1/vc/me):\n%s", screen)
	}
}

// The preflight already answered when the screen is drawn — a return to the
// menu (after doctor, profile) or a slow terminal. The wallet is on the screen
// straight away, in the same words `vc status` uses.
func TestWelcomeShowsWalletTheLaunchAlreadyFetched(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"tariff", meBody(wallet("18", tariffT1, "true", "9")), "$18.00 · T1 · ~9 days left"},
		{"negative balance, negative days", meBody(wallet("-3", tariffT1, "false", "-2")), "-$3.00 · T1 · ~0 days left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, token, authHost := welcomeLaunch(t, meServer(t, tc.body))
			if _, reached, err, reused := currentLaunchPreflight.awaitAuth(token, authHost); !reused || !reached || err != nil {
				t.Fatalf("preflight did not reach the fixture: reused=%v reached=%v err=%v", reused, reached, err)
			}
			s := showWelcome(t, state)
			if screen, ok := s.waitFor(tc.want, 2*time.Second); !ok {
				t.Fatalf("the launch's /v1/vc/me already returned a wallet, and the welcome screen does not show %q:\n%s", tc.want, screen)
			}
		})
	}
}

// No wallet on the wire, or no answer about this person at all: nothing about
// money on the screen, after the preflight has settled.
func TestWelcomeShowsNoMoneyWhenTheLaunchFetchedNoWallet(t *testing.T) {
	refusal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		// Relay's refusal body, wallet included: a refusal is not a verified
		// wallet and must not reach the screen.
		_, _ = w.Write([]byte(`{"error":"wallet_daily_charge_required",` + wallet("1", tariffT1, "false", "0") + `}`))
	}))
	t.Cleanup(refusal.Close)

	for _, tc := range []struct{ name, host string }{
		{"old server, pct only", meServer(t, meBody(`"pct":100,"resetAt":"2026-10-01T00:00:00Z","balanceUsd":12.4`))},
		{"access refused", refusal.URL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, token, authHost := welcomeLaunch(t, tc.host)
			currentLaunchPreflight.awaitAuth(token, authHost)
			s := showWelcome(t, state)
			screen, drawn := s.waitFor(welcomeMenuPrompt, 2*time.Second)
			if !drawn {
				t.Fatalf("the welcome screen was not drawn:\n%s", screen)
			}
			time.Sleep(200 * time.Millisecond) // room for any late redraw
			screen = plainText(s.screen.String())
			for _, word := range []string{"$", "%", "days left"} {
				if strings.Contains(screen, word) {
					t.Errorf("the welcome screen shows %q with no verified wallet on the wire:\n%s", word, screen)
				}
			}
		})
	}
}
