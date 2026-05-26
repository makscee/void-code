// Package welcome implements the first-launch welcome screen for vc.
//
// The welcome screen is a bubbletea TUI shown exactly once — detected via a
// sentinel file (~/.void-code/.welcomed).  Once the sentinel exists the
// welcome screen is never shown again.
package welcome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DefaultSentinelPath returns the canonical path of the welcomed sentinel file.
func DefaultSentinelPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".void-code", ".welcomed")
}

// SentinelExists reports whether the sentinel file exists at path.
func SentinelExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// NeedsWelcome reports whether the welcome screen should be shown.
func NeedsWelcome(sentinelPath string) bool {
	return !SentinelExists(sentinelPath)
}

// TouchSentinel creates the sentinel file at path, creating parent dirs as
// needed.  Idempotent — safe to call multiple times.
func TouchSentinel(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

// Run shows the welcome screen and blocks until the user dismisses it (any
// key, or automatic after render).  After returning, the sentinel is written
// so the screen never appears again.
//
// In non-TTY environments (CI, pipe) it falls back to a plain-text banner.
func Run(sentinelPath string) error {
	p := tea.NewProgram(newModel())
	if _, err := p.Run(); err != nil {
		// Non-TTY fallback: print a plain banner so CI logs are readable.
		fmt.Print(plainBanner())
	}
	return TouchSentinel(sentinelPath)
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

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 3).
			MarginTop(1).
			MarginBottom(1)
)

type model struct {
	quitting bool
}

func newModel() model { return model{} }

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
	inner.WriteString(accentStyle.Render("vc login        authenticate with an access code"))
	inner.WriteString("\n")
	inner.WriteString(accentStyle.Render("vc              launch Claude Code via relay"))
	inner.WriteString("\n")
	inner.WriteString(accentStyle.Render("vc status       show auth + relay + version info"))
	inner.WriteString("\n")
	inner.WriteString(accentStyle.Render("vc update       pull the latest binary"))
	inner.WriteString("\n\n")
	inner.WriteString(subStyle.Render("support: t.me/makscee"))

	sb.WriteString(boxStyle.Render(inner.String()))
	sb.WriteString("\n")
	sb.WriteString(hintStyle.Render("press any key to continue"))
	sb.WriteString("\n")
	return sb.String()
}

// plainBanner returns a plain-text version of the welcome banner (no ANSI).
func plainBanner() string {
	return `
void-code — relay harness for Claude Code — by makscee.ru

  vc login        authenticate with an access code
  vc              launch Claude Code via relay
  vc status       show auth + relay + version info
  vc update       pull the latest binary

  support: t.me/makscee

`
}
