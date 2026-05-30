// Package clackui provides shared @clack/prompts-style left-rail rendering
// helpers for vc TUI screens (welcome, doctor, etc.).
//
// Visual language:
//
//	┌  title line          (top cap, purple)
//	│                      (rail, purple)
//	◇  info text           (info marker, grey text)
//	◆  prompt text         (prompt marker, grey text)
//	│  ●  selected item    (purple bold)
//	│  ○  unselected item  (dim grey)
//	└  hint text           (bottom cap, dim grey)
package clackui

import "github.com/charmbracelet/lipgloss"

// ─── colours ─────────────────────────────────────────────────────────────────

const (
	ColorPurple  = "#7C3AED"
	ColorWhite   = "#FFFFFF"
	ColorGrey    = "#9CA3AF"
	ColorDimGrey = "#6B7280"
	ColorWarn    = "#F59E0B"
	ColorGreen   = "#22C55E"
	ColorRed     = "#EF4444"
)

// ─── styles ──────────────────────────────────────────────────────────────────

var (
	// RailStyle renders the continuous left rail │ and caps ┌ └.
	RailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorPurple))

	// TitleStyle is bold white title text next to ┌ cap.
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(ColorWhite))

	// InfoTextStyle is text on ◇ info lines.
	InfoTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorGrey))

	// SelectedItemStyle is the ● selected menu item label (purple bold).
	SelectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(ColorPurple))

	// UnselectedItemStyle is the ○ unselected menu item label (dim grey).
	UnselectedItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorDimGrey))

	// HintStyle is bottom cap hint text (dim grey).
	HintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorDimGrey))

	// WarnStyle is warning text (amber).
	WarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorWarn))

	// OkStyle is success check text (green).
	OkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorGreen))

	// FailStyle is failure check text (red).
	FailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorRed))
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// RailLine returns a line with the left rail prefix rendered in RailStyle.
// prefix is the rail/cap glyph(s); content is the rest of the line (caller styles it).
func RailLine(prefix, content string) string {
	return RailStyle.Render(prefix) + content
}
