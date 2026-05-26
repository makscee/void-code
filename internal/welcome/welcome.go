// Package welcome implements the persistent landing screen for vc.
//
// The landing screen is shown on every bare `vc` invocation (no sub-command).
// It displays auth state + subscription info and waits for any keypress.
// Logged-in: any key → spawn claude.
// Logged-out: any key → vc login flow (caller interprets LoginRequested).
package welcome

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// AuthState carries the information to display on the landing screen.
type AuthState struct {
	// LoggedIn reports whether a valid token is present.
	LoggedIn bool
	// Identity is the user identity string (email or userId) when logged in.
	Identity string
	// SubDaysLeft is days remaining on subscription.
	// -1 = unlimited; 0 = no active subscription; positive = days remaining.
	SubDaysLeft int
	// SubUnknown is true when subscription status could not be fetched.
	SubUnknown bool
}

// RunResult is returned by Run to indicate what the keypress should trigger.
type RunResult int

const (
	// SpawnClaude means the user is logged in and pressed a key — spawn claude.
	SpawnClaude RunResult = iota
	// RunLogin means the user is logged out and pressed a key — run vc login.
	RunLogin
)

// Run shows the persistent landing screen and blocks until the user presses
// any key.  Returns SpawnClaude or RunLogin based on auth state.
//
// In non-TTY environments (CI, pipe) it falls back to a plain-text banner
// and returns SpawnClaude.
func Run(state AuthState) (RunResult, error) {
	m := newModel(state)
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		// Non-TTY fallback: print a plain banner and treat as logged-in.
		fmt.Print(plainBanner(state))
		if !state.LoggedIn {
			return RunLogin, nil
		}
		return SpawnClaude, nil
	}
	final, ok := result.(model)
	if !ok || !final.LoggedIn {
		return RunLogin, nil
	}
	return SpawnClaude, nil
}

// ─── legacy compat shim (tests + callers that used the old sentinel API) ──────

// DefaultSentinelPath is kept for callers that reference it; it no longer
// gates the banner (banner is always shown).
func DefaultSentinelPath() string {
	return ""
}

// NeedsWelcome always returns true — banner is always shown now.
func NeedsWelcome(_ string) bool {
	return true
}

// TouchSentinel is a no-op kept for backward compat.
func TouchSentinel(_ string) error {
	return nil
}

// SentinelExists is a no-op kept for backward compat; always returns false.
func SentinelExists(_ string) bool {
	return false
}

// ─── bubbletea model ───────────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED")).
			PaddingLeft(2)

	subStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF")).
			PaddingLeft(2)

	accentStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A78BFA")).
			PaddingLeft(2)

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280")).
			Italic(true).
			PaddingLeft(2)

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			PaddingLeft(2)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 3).
			MarginTop(1).
			MarginBottom(1)
)

type model struct {
	AuthState
	quitting bool
}

func newModel(state AuthState) model {
	return model{AuthState: state}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder

	inner := strings.Builder{}
	inner.WriteString(titleStyle.Render("void-code"))
	inner.WriteString("\n")
	inner.WriteString(subStyle.Render("relay harness for Claude Code — by makscee.ru"))
	inner.WriteString("\n\n")

	if m.LoggedIn {
		// Subscription line.
		inner.WriteString(accentStyle.Render("Logged in as " + m.Identity))
		inner.WriteString("\n")
		if m.SubUnknown {
			inner.WriteString(subStyle.Render("· subscription status unknown ·"))
		} else if m.SubDaysLeft == -1 {
			inner.WriteString(accentStyle.Render("· unlimited subscription ·"))
		} else if m.SubDaysLeft > 0 {
			inner.WriteString(accentStyle.Render(fmt.Sprintf("· %d days left on subscription ·", m.SubDaysLeft)))
		} else {
			inner.WriteString(warnStyle.Render("· no active subscription ·"))
		}
		inner.WriteString("\n\n")
		inner.WriteString(hintStyle.Render("press any key to start"))
	} else {
		inner.WriteString(warnStyle.Render("Not logged in"))
		inner.WriteString("\n\n")
		inner.WriteString(hintStyle.Render("press any key to login"))
	}
	inner.WriteString("\n")

	sb.WriteString(boxStyle.Render(inner.String()))
	sb.WriteString("\n")
	return sb.String()
}

// plainBanner returns a plain-text version of the landing screen (no ANSI).
func plainBanner(state AuthState) string {
	var sb strings.Builder
	sb.WriteString("\nvoid-code — relay harness for Claude Code — by makscee.ru\n\n")
	if state.LoggedIn {
		sb.WriteString("  Logged in as " + state.Identity + "\n")
		if state.SubUnknown {
			sb.WriteString("  · subscription status unknown ·\n")
		} else if state.SubDaysLeft == -1 {
			sb.WriteString("  · unlimited subscription ·\n")
		} else if state.SubDaysLeft > 0 {
			sb.WriteString(fmt.Sprintf("  · %d days left on subscription ·\n", state.SubDaysLeft))
		} else {
			sb.WriteString("  · no active subscription ·\n")
		}
	} else {
		sb.WriteString("  Not logged in\n")
	}
	sb.WriteString("\n")
	return sb.String()
}
