package main

import (
	"errors"
	"fmt"
	"strings"
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
	{"access refused", launchAuthResult{err: auth.ErrAccessNotGranted}},
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
