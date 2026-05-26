package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/version"
)

// bannerModel is a minimal bubbletea model that displays the vc welcome banner
// then quits immediately.  VCD-6 will extend this with a first-launch tour and
// sentinel logic (~/.void-code/.welcomed).
type bannerModel struct {
	done bool
}

var (
	bannerTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#7C3AED")).
				PaddingLeft(2)

	bannerSubStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9CA3AF")).
			PaddingLeft(2)
)

func (m bannerModel) Init() tea.Cmd {
	return tea.Quit
}

func (m bannerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	return m, tea.Quit
}

func (m bannerModel) View() string {
	var sb strings.Builder
	sb.WriteString(bannerTitleStyle.Render(fmt.Sprintf("void-code  %s", version.Version)))
	sb.WriteString("\n")
	sb.WriteString(bannerSubStyle.Render("relay harness for Claude Code · makscee.ru"))
	sb.WriteString("\n")
	return sb.String()
}

// printBanner renders the welcome banner to stdout via bubbletea.
// It is a no-op in non-TTY environments.
func printBanner() {
	p := tea.NewProgram(bannerModel{})
	if _, err := p.Run(); err != nil {
		// Non-fatal — banner failure must never block spawn.
		_ = err
	}
}
