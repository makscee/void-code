package welcome_test

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/welcome"
)

// The landing screen renders before the launch preflight has an answer, so the
// answer has to be able to catch up with a screen that is already drawn: the
// program accepts a late AuthState and repaints.
//
// Merging follows the same rule the state builder uses in cmd/vc: a known
// identity is never replaced by an empty one. A late answer that failed carries
// no identity, and losing the name already on screen is exactly the regression
// this pair of tests exists to prevent.

// safeOutput is written by the bubbletea render loop and read by the test, so
// it carries its own lock — without it the race detector fails these tests for
// the harness rather than for the code under test.
type safeOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeOutput) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeOutput) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// waitForFrame polls the rendered output until want shows up. Polling, not
// sleeping: a passing run costs a couple of milliseconds, and the deadline only
// decides how a failure is reported.
func waitForFrame(t *testing.T, out *safeOutput, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%q never rendered; output was:\n%s", want, out.String())
}

func TestLateIdentityReachesAnAlreadyDrawnScreen(t *testing.T) {
	input, keys := io.Pipe()
	out := &safeOutput{}
	late := make(chan welcome.IdentityUpdate, 1)
	done := make(chan error, 1)

	go func() {
		_, err := welcome.RunWithLateIdentity(
			welcome.AuthState{LoggedIn: true, IdentityUnverified: true},
			welcome.Callbacks{},
			late,
			tea.WithInput(input), tea.WithOutput(out), tea.WithoutSignals(),
		)
		done <- err
	}()

	// The screen is up before anything is known about the user.
	waitForFrame(t, out, "identity temporarily unavailable")

	balance := 12.5
	late <- welcome.IdentityUpdate{AuthState: welcome.AuthState{LoggedIn: true, Identity: "late@example.com", BalanceUsd: &balance}}

	waitForFrame(t, out, "late@example.com")
	waitForFrame(t, out, welcome.FormatBalance(&balance))

	_, _ = keys.Write([]byte("q"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not quit after the late identity arrived")
	}
	_ = keys.Close()
}

func TestLateAnswerWithoutIdentityKeepsTheKnownOne(t *testing.T) {
	model := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "known@example.com"})

	// A late answer that could not name anyone: empty identity, nothing else to
	// show. It may mark the screen unverified; it may not blank the name.
	updated, _ := model.Update(welcome.IdentityUpdate{AuthState: welcome.AuthState{LoggedIn: true, IdentityUnverified: true}})
	view := updated.View()

	if !strings.Contains(view, "known@example.com") {
		t.Fatalf("known identity was wiped by an empty late answer:\n%s", view)
	}
	if strings.Contains(view, "identity temporarily unavailable") {
		t.Fatalf("screen fell back to the unknown-identity line while a name was known:\n%s", view)
	}
}

func TestLateAnswerReplacesOneIdentityWithAnother(t *testing.T) {
	model := welcome.NewMenuModelForTest(welcome.AuthState{LoggedIn: true, Identity: "stale@example.com", IdentityUnverified: true})

	balance := 3.25
	updated, _ := model.Update(welcome.IdentityUpdate{AuthState: welcome.AuthState{LoggedIn: true, Identity: "fresh@example.com", BalanceUsd: &balance}})
	view := updated.View()

	if !strings.Contains(view, "fresh@example.com") {
		t.Fatalf("late identity did not reach the view:\n%s", view)
	}
	if strings.Contains(view, "stale@example.com") {
		t.Fatalf("view still shows the superseded identity:\n%s", view)
	}
	if !strings.Contains(view, welcome.FormatBalance(&balance)) {
		t.Fatalf("balance from the late answer did not reach the view:\n%s", view)
	}
}

// The merge rule lives here and nowhere else. It used to exist twice — once in
// this package for the late repaint, once in cmd/vc for the next menu frame —
// and the two copies could drift apart without a single test noticing: a
// mutation in either half survived the whole suite. One exported rule, called
// by both sides, is what makes a mutation in it fail both packages.

func TestMergeIdentityKeepsAKnownNameWhenTheAnswerHasNone(t *testing.T) {
	current := welcome.AuthState{LoggedIn: true, Identity: "known@example.com", UpdateNudge: "update available"}
	answer := welcome.AuthState{LoggedIn: true, IdentityUnverified: true}

	merged := welcome.MergeIdentity(current, answer)

	if merged.Identity != "known@example.com" {
		t.Fatalf("Identity = %q, want the known %q", merged.Identity, "known@example.com")
	}
	if !merged.IdentityUnverified {
		t.Fatalf("IdentityUnverified = false, want true — the kept name was not re-checked (%+v)", merged)
	}
	if merged.UpdateNudge != "update available" {
		t.Fatalf("UpdateNudge = %q, want it kept — an identity answer knows nothing about updates", merged.UpdateNudge)
	}
}

func TestMergeIdentityTakesTheAnswersNameAndMoney(t *testing.T) {
	balance := 3.25
	current := welcome.AuthState{LoggedIn: true, Identity: "stale@example.com", IdentityUnverified: true, UpdateNudge: "update available"}
	answer := welcome.AuthState{LoggedIn: true, Identity: "fresh@example.com", BalanceUsd: &balance}

	merged := welcome.MergeIdentity(current, answer)

	if merged.Identity != "fresh@example.com" {
		t.Fatalf("Identity = %q, want %q", merged.Identity, "fresh@example.com")
	}
	if merged.IdentityUnverified {
		t.Fatalf("IdentityUnverified = true on a checked answer (%+v)", merged)
	}
	if merged.BalanceUsd == nil || *merged.BalanceUsd != balance {
		t.Fatalf("BalanceUsd = %v, want %v", merged.BalanceUsd, balance)
	}
	if merged.UpdateNudge != "update available" {
		t.Fatalf("UpdateNudge = %q, want it kept", merged.UpdateNudge)
	}
}

func TestMergeIdentityPrefersTheAnswersOwnNudge(t *testing.T) {
	current := welcome.AuthState{LoggedIn: true, Identity: "known@example.com", UpdateNudge: "old nudge"}
	answer := welcome.AuthState{LoggedIn: true, Identity: "known@example.com", UpdateNudge: "new nudge"}

	if merged := welcome.MergeIdentity(current, answer); merged.UpdateNudge != "new nudge" {
		t.Fatalf("UpdateNudge = %q, want %q — a nudge the answer carries is the newer one", merged.UpdateNudge, "new nudge")
	}
}

// A rejected token is the one answer that must erase rather than preserve:
// keeping the name here would put it straight back on a screen that has just
// learned the session is gone.
func TestMergeIdentityDoesNotResurrectTheNameOfALoggedOutAnswer(t *testing.T) {
	current := welcome.AuthState{LoggedIn: true, Identity: "known@example.com", UpdateNudge: "update available"}
	answer := welcome.AuthState{LoggedIn: false}

	merged := welcome.MergeIdentity(current, answer)

	if merged.LoggedIn {
		t.Fatalf("LoggedIn = true after a logged-out answer (%+v)", merged)
	}
	if merged.Identity != "" {
		t.Fatalf("Identity = %q after a logged-out answer, want empty", merged.Identity)
	}
	if merged.UpdateNudge != "update available" {
		t.Fatalf("UpdateNudge = %q, want it kept — losing the session says nothing about updates", merged.UpdateNudge)
	}
}
