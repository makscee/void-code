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
