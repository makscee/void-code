package main

// rails:pin-on-coverage мутации «nil вместо канала в defaultWelcomeMenuDeps» и «не передавать состояние» валят этот тест; красным он быть не мог — проводка уже была верной, тест заведён сторожем регресса после того, как ревьювер измерил, что её можно выдернуть с зелёным сьютом


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

// runWelcomeMenu is tested with its screen replaced, which proves the loop and
// nothing about the screen the loop actually gets. defaultWelcomeMenuDeps is
// that screen: it is where a channel can be dropped on the floor — passing nil
// there leaves every menu test green while no late answer ever repaints
// anything in production.
//
// So this drives the real thing: the production deps, the production program,
// only its terminal replaced by a pipe and a buffer.

// screenOutput is written by the render loop and read by the test, so it
// carries its own lock.
type screenOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *screenOutput) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *screenOutput) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func waitForScreenText(t *testing.T, out *screenOutput, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%q never reached the screen; output was:\n%s", want, out.String())
}

func TestDefaultWelcomeMenuDepsDrawTheStateAndTheLateAnswer(t *testing.T) {
	withTempHome(t)
	in, keys := io.Pipe()
	out := &screenOutput{}
	previous := welcomeProgramOptions
	welcomeProgramOptions = []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignals()}
	t.Cleanup(func() { welcomeProgramOptions = previous })

	late := make(chan welcome.IdentityUpdate, 1)
	deps := defaultWelcomeMenuDeps()
	done := make(chan welcome.RunResult, 1)
	go func() {
		result, _ := deps.screen(welcome.AuthState{LoggedIn: true, Identity: "known@example.com"}, late)
		done <- result
	}()

	// Half one: the state the menu built is what the user sees.
	waitForScreenText(t, out, "known@example.com")

	// Half two: the channel the menu opened is connected to that same screen.
	balance := 4.5
	late <- welcome.IdentityUpdate{AuthState: welcome.AuthState{LoggedIn: true, Identity: "late@example.com", BalanceUsd: &balance}}
	waitForScreenText(t, out, "late@example.com")
	waitForScreenText(t, out, welcome.FormatBalance(&balance))

	_, _ = keys.Write([]byte("q"))
	select {
	case result := <-done:
		if result != welcome.Quit {
			t.Fatalf("screen returned %v, want Quit", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("screen never returned")
	}
	_ = keys.Close()
	close(late)
}
