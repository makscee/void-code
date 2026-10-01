package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/welcome"
)

// void-works#90: the welcome screen tells a running account check from a
// failed one. welcomeBalance used to hand the running screen nothing when the
// check ended without an account (its command returned nil), so the screen
// could only ever show the failure text from the first frame on. Now the
// finished-and-failed check reaches the screen as its own message, and the
// first frame says «проверяю аккаунт…».

const (
	welcomeChecking    = "проверяю аккаунт…"
	welcomeCheckFailed = "не удалось проверить аккаунт"
)

// failedChecks are the ways the launch's /v1/vc/me ends without vouching for
// anyone while the token is still accepted (a rejected token signs the screen
// out instead).
var failedChecks = []struct {
	name   string
	result launchAuthResult
}{
	{"unavailable", launchAuthResult{err: fmt.Errorf("Session verification unavailable; try again: %w", errors.New("dial tcp: connection refused"))}},
	{"not reached", launchAuthResult{}},
	// A refusal (auth.ErrAccessNotGranted) is not here: the check ran and
	// answered — see TestWelcomeRefusalSaysAccessNotGranted.
}

func preflightWith(result launchAuthResult, done bool) *launchPreflight {
	p := &launchPreflight{deps: launchPreflightDeps{now: time.Now}, authDone: make(chan struct{}), authResult: result}
	if done {
		close(p.authDone)
	}
	return p
}

func welcomeView(state welcome.AuthState, msgs ...tea.Msg) string {
	var m tea.Model = welcome.NewMenuModelForTest(state)
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return plainText(m.View())
}

func signedInLocalState() welcome.AuthState {
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true}
}

// The first frame of a signed-in launch, with the check still running.
func TestWelcomeBalanceFirstFrameSaysChecking(t *testing.T) {
	p := preflightWith(launchAuthResult{}, false)
	state, late := welcomeBalance(signedInLocalState(), p)
	if late == nil {
		t.Fatal("the check is running and welcomeBalance hands the screen no command to wait for it")
	}
	close(p.authDone) // let the command's goroutine, if any, finish
	view := welcomeView(state)
	if !strings.Contains(view, welcomeChecking) {
		t.Errorf("first frame with the check in flight does not say %q:\n%s", welcomeChecking, view)
	}
	if strings.Contains(view, welcomeCheckFailed) {
		t.Errorf("first frame says %q while the check is still running:\n%s", welcomeCheckFailed, view)
	}
}

// The check fails while the screen is up: the command welcomeBalance returned
// brings the failure to the screen.
func TestWelcomeBalanceBringsALateFailureToTheScreen(t *testing.T) {
	for _, tc := range failedChecks {
		t.Run(tc.name, func(t *testing.T) {
			p := preflightWith(tc.result, false)
			state, late := welcomeBalance(signedInLocalState(), p)
			if late == nil {
				t.Fatal("the check is running and welcomeBalance hands the screen no command to wait for it")
			}
			close(p.authDone)
			msgs := make(chan tea.Msg, 1)
			go func() { msgs <- late() }()
			var msg tea.Msg
			select {
			case msg = <-msgs:
			case <-time.After(2 * time.Second):
				t.Fatal("welcomeBalance's command did not return after the check finished")
			}
			if msg == nil {
				t.Fatal("the check failed and welcomeBalance's command handed the screen nothing: it would keep saying it is checking")
			}
			if want := (welcome.AccountMsg{Failed: true}); msg != want {
				t.Errorf("late message = %#v, want %#v", msg, want)
			}
			view := welcomeView(state, msg)
			if !strings.Contains(view, welcomeCheckFailed) {
				t.Errorf("the check failed and the screen does not say %q:\n%s", welcomeCheckFailed, view)
			}
			if strings.Contains(view, welcomeChecking) {
				t.Errorf("the check is over and the screen still says %q:\n%s", welcomeChecking, view)
			}
		})
	}
}

// The check had already failed when the screen is drawn (a return to the
// menu, a slow terminal): the first frame says so.
func TestWelcomeBalanceShowsAFailureAlreadyIn(t *testing.T) {
	for _, tc := range failedChecks {
		t.Run(tc.name, func(t *testing.T) {
			state, late := welcomeBalance(signedInLocalState(), preflightWith(tc.result, true))
			if late != nil {
				t.Error("the check is over and welcomeBalance still hands the screen a command to wait for it")
			}
			view := welcomeView(state)
			if !strings.Contains(view, welcomeCheckFailed) {
				t.Errorf("the check failed before the first frame and the screen does not say %q:\n%s", welcomeCheckFailed, view)
			}
			if strings.Contains(view, welcomeChecking) {
				t.Errorf("the check is over and the screen says %q:\n%s", welcomeChecking, view)
			}
		})
	}
}

// A successful answer is unchanged: the email and the balance replace the
// «checking» line.
func TestWelcomeBalanceLateSuccessShowsTheAccount(t *testing.T) {
	me := auth.MeResult{Email: "person@example.test"}
	p := preflightWith(launchAuthResult{me: me, reached: true}, false)
	state, late := welcomeBalance(signedInLocalState(), p)
	if late == nil {
		t.Fatal("the check is running and welcomeBalance hands the screen no command to wait for it")
	}
	close(p.authDone)
	msg := late()
	account, ok := msg.(welcome.AccountMsg)
	if !ok || account.Identity != "person@example.test" || account.Failed {
		t.Fatalf("late message = %#v, want the verified account", msg)
	}
	view := welcomeView(state, msg)
	if !strings.Contains(view, "person@example.test") {
		t.Errorf("the account was verified and the screen does not show the email:\n%s", view)
	}
	for _, stale := range []string{welcomeChecking, welcomeCheckFailed} {
		if strings.Contains(view, stale) {
			t.Errorf("the account was verified and the screen still says %q:\n%s", stale, view)
		}
	}
}

// /v1/vc/me answered 200, with a wallet but with neither an email nor a user
// id: the check is over and named no one. accountIfReady says it failed (and
// still hands over the balance); otherwise the screen, which says «checking»
// until an identity or a failure arrives, would say «проверяю аккаунт…» for
// as long as it is up.
func TestWelcomeAnswerWithoutIdentityIsAFailedCheck(t *testing.T) {
	const balance = "T1 · осталось ~9 дней"
	// auth.FetchMe refuses such a body on the wire ("missing identity"), but
	// the preflight takes whatever its auth dependency returns: a real answer
	// with its wallet, with the identity blanked.
	me := fetchMeFrom(t, meBody(wallet("18", tariffT1, "true", "9")))
	me.Email, me.UserID = "", ""
	answered := launchAuthResult{me: me, reached: true}

	account, ready := preflightWith(answered, true).accountIfReady()
	if !ready {
		t.Fatal("accountIfReady: the check is over and it says not ready")
	}
	if !account.Failed || account.Identity != "" || account.SignedOut {
		t.Errorf("accountIfReady = %#v, want Failed with no identity and not signed out", account)
	}
	if !strings.Contains(account.Balance, balance) {
		t.Errorf("accountIfReady balance = %q, want the wallet %q from the answer", account.Balance, balance)
	}

	assertFailedWithBalance := func(t *testing.T, view string) {
		t.Helper()
		if !strings.Contains(view, welcomeCheckFailed) {
			t.Errorf("the answer named no one and the screen does not say %q:\n%s", welcomeCheckFailed, view)
		}
		if strings.Contains(view, welcomeChecking) {
			t.Errorf("the check is over and the screen still says %q:\n%s", welcomeChecking, view)
		}
		if !strings.Contains(view, balance) {
			t.Errorf("the answer carried a wallet and the screen does not show %q:\n%s", balance, view)
		}
	}

	t.Run("already in", func(t *testing.T) {
		state, late := welcomeBalance(signedInLocalState(), preflightWith(answered, true))
		if late != nil {
			t.Error("the check is over and welcomeBalance still hands the screen a command to wait for it")
		}
		assertFailedWithBalance(t, welcomeView(state))
	})

	t.Run("arrives while the screen is up", func(t *testing.T) {
		p := preflightWith(answered, false)
		state, late := welcomeBalance(signedInLocalState(), p)
		if late == nil {
			t.Fatal("the check is running and welcomeBalance hands the screen no command to wait for it")
		}
		close(p.authDone)
		msg := late()
		if msg == nil {
			t.Fatal("the answer named no one and welcomeBalance's command handed the screen nothing: it would keep saying it is checking")
		}
		assertFailedWithBalance(t, welcomeView(state, msg))
	})
}

// Panel on void-works#90 [conf 92]: /v1/vc/me answered 402 — the token is
// valid, access is refused (auth.ErrAccessNotGranted). authGate says so
// itself: "A refusal is not a failed check … it ran and answered, so repeating
// it changes nothing." The screen said «не удалось проверить аккаунт», as for
// a network failure, which tells the person to retry something that will
// never pass. Artem, 01.10: it says «доступ не выдан». The refusal reaches the
// screen as AccountMsg{Refused: true}: no identity, no wallet (a refusal
// vouches for neither), not signed out, not failed.
const welcomeAccessRefused = "доступ не выдан"

func assertRefusedScreen(t *testing.T, view string) {
	t.Helper()
	if !strings.Contains(view, welcomeAccessRefused) {
		t.Errorf("access was refused and the screen does not say %q:\n%s", welcomeAccessRefused, view)
	}
	for _, wrong := range []string{welcomeCheckFailed, welcomeChecking} {
		if strings.Contains(view, wrong) {
			t.Errorf("access was refused and the screen says %q:\n%s", wrong, view)
		}
	}
}

func TestWelcomeRefusalSaysAccessNotGranted(t *testing.T) {
	refused := launchAuthResult{err: auth.ErrAccessNotGranted}
	want := welcome.AccountMsg{Refused: true}

	account, ready := preflightWith(refused, true).accountIfReady()
	if !ready {
		t.Fatal("accountIfReady: the check is over and it says not ready")
	}
	if account != want {
		t.Errorf("accountIfReady = %#v, want %#v", account, want)
	}

	t.Run("already in", func(t *testing.T) {
		state, late := welcomeBalance(signedInLocalState(), preflightWith(refused, true))
		if late != nil {
			t.Error("the check is over and welcomeBalance still hands the screen a command to wait for it")
		}
		assertRefusedScreen(t, welcomeView(state))
	})

	t.Run("arrives while the screen is up", func(t *testing.T) {
		p := preflightWith(refused, false)
		state, late := welcomeBalance(signedInLocalState(), p)
		if late == nil {
			t.Fatal("the check is running and welcomeBalance hands the screen no command to wait for it")
		}
		if view := welcomeView(state); !strings.Contains(view, welcomeChecking) {
			t.Errorf("first frame with the check in flight does not say %q:\n%s", welcomeChecking, view)
		}
		close(p.authDone)
		msg := late()
		if msg != want {
			t.Errorf("late message = %#v, want %#v", msg, want)
		}
		assertRefusedScreen(t, welcomeView(state, msg))
	})
}

// The same refusal on the wire, through the production preflight and welcome
// program: relay's 402 for a valid token without access.
func TestWelcomeScreenSaysAccessNotGrantedOn402(t *testing.T) {
	refusal := func(gate <-chan struct{}) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if gate != nil {
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"budget_exceeded"}`))
		}
	}

	t.Run("already in", func(t *testing.T) {
		srv := httptest.NewServer(refusal(nil))
		t.Cleanup(srv.Close)
		state, _, _ := welcomeLaunch(t, srv.URL)
		if r := awaitPreflightAuth(t, currentLaunchPreflight); !errors.Is(r.err, auth.ErrAccessNotGranted) {
			t.Fatalf("the 402 fixture did not come back as ErrAccessNotGranted: %v", r.err)
		}
		s := showWelcome(t, state)
		screen, ok := s.waitFor(welcomeAccessRefused, 2*time.Second)
		if !ok {
			t.Fatalf("access was refused before the first frame and the screen does not say %q:\n%s", welcomeAccessRefused, screen)
		}
		for _, wrong := range []string{welcomeCheckFailed, welcomeChecking} {
			if strings.Contains(screen, wrong) {
				t.Errorf("access was refused before the first frame and the screen says %q:\n%s", wrong, screen)
			}
		}
	})

	t.Run("arrives while the screen is up", func(t *testing.T) {
		gate := make(chan struct{})
		var once sync.Once
		release := func() { once.Do(func() { close(gate) }) }
		srv := httptest.NewServer(refusal(gate))
		t.Cleanup(srv.Close)
		t.Cleanup(release)
		state, _, _ := welcomeLaunch(t, srv.URL)
		s := showWelcome(t, state)
		if screen, drawn := s.waitFor(welcomeChecking, time.Second); !drawn {
			t.Fatalf("before /v1/vc/me answers the screen should say %q:\n%s", welcomeChecking, screen)
		}
		release()
		if r := awaitPreflightAuth(t, currentLaunchPreflight); !errors.Is(r.err, auth.ErrAccessNotGranted) {
			t.Fatalf("the 402 fixture did not come back as ErrAccessNotGranted: %v", r.err)
		}
		screen, ok := s.waitFor(welcomeAccessRefused, 3*time.Second)
		if !ok {
			t.Fatalf("access was refused while the screen was up and the screen never said %q:\n%s", welcomeAccessRefused, screen)
		}
		if strings.Contains(screen, welcomeCheckFailed) {
			t.Errorf("access was refused and the screen said %q at some point:\n%s", welcomeCheckFailed, screen)
		}
		if after := screen[strings.LastIndex(screen, welcomeAccessRefused):]; strings.Contains(after, welcomeChecking) {
			t.Errorf("the check is over and the screen still says %q:\n%s", welcomeChecking, after)
		}
	})
}
