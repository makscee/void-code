package welcome_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makscee/void-code/internal/welcome"
)

// void-works#90: the screen is up long before /v1/vc/me answers (0.06 s at the
// console start), and it starts signed in with no verified identity — the state
// main draws from the local token alone. That first frame said «не удалось
// проверить аккаунт», so everyone who read it was told their account was broken
// while the check was still running. The failure text is for a check that
// finished and failed; while it is running the screen says it is checking.
const (
	accountChecking = "проверяю аккаунт…"
	accountFailed   = "не удалось проверить аккаунт"
	staleIdentity   = "member@example.test (последний известный; сейчас не проверен)"
)

// checkInFlight is the state main hands the first frame for a signed-in launch
// (resolveLocalAuthStateWithSource): the token is there, the answer is not.
func checkInFlight() welcome.AuthState {
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true}
}

func viewAfter(t *testing.T, state welcome.AuthState, msgs ...tea.Msg) string {
	t.Helper()
	var m tea.Model = welcome.NewMenuModelForTest(state)
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m.View()
}

func TestWelcomeSaysCheckingWhileTheAccountCheckIsInFlight(t *testing.T) {
	view := viewAfter(t, checkInFlight())
	if !strings.Contains(view, accountChecking) {
		t.Errorf("before /v1/vc/me answers the screen must say %q:\n%s", accountChecking, view)
	}
	if strings.Contains(view, accountFailed) {
		t.Errorf("the screen says %q while the check is still running:\n%s", accountFailed, view)
	}
}

func TestWelcomeSaysCheckFailedOnlyAfterTheCheckFailed(t *testing.T) {
	failed := welcome.AccountMsg{Failed: true}

	view := viewAfter(t, checkInFlight(), failed)
	if !strings.Contains(view, accountFailed) {
		t.Errorf("the check finished without an account and the screen does not say %q:\n%s", accountFailed, view)
	}
	if strings.Contains(view, accountChecking) {
		t.Errorf("the check is over and the screen still says %q:\n%s", accountChecking, view)
	}

	// The same answer already in before the first frame (welcomeBalance lays
	// it over the state with WithAccount), on the menu and on the plain banner
	// printed when the terminal cannot run the program.
	state := welcome.WithAccount(checkInFlight(), failed)
	for where, screen := range map[string]string{"menu": viewAfter(t, state), "plain banner": welcome.PlainBannerForTest(state)} {
		if !strings.Contains(screen, accountFailed) {
			t.Errorf("welcome %s: the check failed before the first frame and it does not say %q:\n%s", where, accountFailed, screen)
		}
		if strings.Contains(screen, accountChecking) {
			t.Errorf("welcome %s: the check is over and it says %q:\n%s", where, accountChecking, screen)
		}
	}
}

func TestWelcomeShowsTheVerifiedAccountAfterTheCheck(t *testing.T) {
	view := viewAfter(t, checkInFlight(), welcome.AccountMsg{Identity: "person@example.test", Balance: "T1 · осталось ~9 дней"})
	if !strings.Contains(view, "person@example.test · T1 · осталось ~9 дней") {
		t.Errorf("/v1/vc/me answered and the screen does not show the email with the balance:\n%s", view)
	}
	for _, stale := range []string{accountChecking, accountFailed} {
		if strings.Contains(view, stale) {
			t.Errorf("the account was verified and the screen still says %q:\n%s", stale, view)
		}
	}
}

// A last known identity stays on the screen, marked unchecked, whether the
// check is running or failed.
func TestWelcomeKeepsTheLastKnownIdentity(t *testing.T) {
	stale := welcome.AuthState{LoggedIn: true, Identity: "member@example.test", IdentityUnverified: true}
	for name, view := range map[string]string{
		"check in flight": viewAfter(t, stale),
		"check failed":    viewAfter(t, stale, welcome.AccountMsg{Failed: true}),
	} {
		if !strings.Contains(view, staleIdentity) {
			t.Errorf("%s: the screen lost %q:\n%s", name, staleIdentity, view)
		}
	}
}

func TestWelcomeSignedOutScreenSaysNothingAboutTheCheck(t *testing.T) {
	view := viewAfter(t, welcome.AuthState{})
	if !strings.Contains(view, "Вход не выполнен") {
		t.Errorf("signed-out screen lost «Вход не выполнен»:\n%s", view)
	}
	for _, text := range []string{accountChecking, accountFailed} {
		if strings.Contains(view, text) {
			t.Errorf("signed-out screen says %q:\n%s", text, view)
		}
	}
}
