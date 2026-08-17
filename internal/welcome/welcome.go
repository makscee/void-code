// Package welcome implements VC's subscription landing screen.
package welcome

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/version"
)

type AuthState struct {
	LoggedIn           bool
	Identity           string
	IdentityUnverified bool
	UpdateNudge        string
	BalanceUsd         *float64
}

type RunResult int

const (
	SpawnPi RunResult = iota
	RunLogin
	RunDoctor
	Quit
	ShowTopUp
	RunProfile
)

// Callbacks is intentionally empty: console subscription choices are not persisted here.
type Callbacks struct{}

func Run(state AuthState, cb Callbacks) (RunResult, error) { return RunWithOptions(state, cb) }
func RunWithOptions(state AuthState, cb Callbacks, opts ...tea.ProgramOption) (RunResult, error) {
	p := tea.NewProgram(newModel(state), opts...)
	out, err := p.Run()
	if err != nil {
		fmt.Print(plainBanner(state))
		if state.LoggedIn {
			return SpawnPi, nil
		}
		return RunLogin, nil
	}
	m, ok := out.(model)
	if !ok || !m.chosen {
		if !state.LoggedIn {
			return RunLogin, nil
		}
		return Quit, nil
	}
	return m.result, nil
}

func FormatBalance(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("$%.2f left", *v)
}
func PlainBannerForTest(state AuthState) string { return plainBanner(state) }

type viewState int

const (
	menuView viewState = iota
	topUpView
)

type menuItem struct {
	label  string
	result RunResult
}
type model struct {
	AuthState
	items            []menuItem
	cursor           int
	view             viewState
	result           RunResult
	chosen, quitting bool
}

func menuItemsFor(state AuthState) []menuItem {
	if !state.LoggedIn {
		return []menuItem{{"Login", RunLogin}}
	}
	return []menuItem{{"Start", SpawnPi}, {"Top up", ShowTopUp}, {"Run doctor", RunDoctor}, {"Open profile", RunProfile}}
}
func newModel(state AuthState) model            { return model{AuthState: state, items: menuItemsFor(state)} }
func (m model) Init() tea.Cmd                   { return nil }
func NewMenuModelForTest(state AuthState) model { return newModel(state) }
func (m model) Cursor() int                     { return m.cursor }
func (m model) ItemCount() int                  { return len(m.items) }
func (m model) ItemLabel(i int) string          { return m.items[i].label }
func (m model) SetCursor(i int) model           { m.cursor = i; return m }
func (m model) MoveCursor(d int) model {
	n := len(m.items)
	m.cursor = ((m.cursor+d)%n + n) % n
	return m
}
func (m model) Activate() RunResult { return m.items[m.cursor].result }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	s := key.String()
	if m.view == topUpView {
		m.view = menuView
		return m, nil
	}
	switch s {
	case "ctrl+c", "q", "esc":
		m.result, m.chosen, m.quitting = Quit, true, true
		return m, tea.Quit
	case "up", "k":
		return m.MoveCursor(-1), nil
	case "down", "j":
		return m.MoveCursor(1), nil
	case "enter", " ":
		r := m.Activate()
		if r == ShowTopUp {
			m.view = topUpView
			return m, nil
		}
		m.result, m.chosen, m.quitting = r, true, true
		return m, tea.Quit
	}
	return m, nil
}
func identityDisplay(identity string, unverified bool) string {
	if !unverified {
		return identity
	}
	if identity == "" {
		return "identity temporarily unavailable"
	}
	return identity + " (last known; temporarily unverified)"
}
func (m model) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(clackui.RailLine("┌", "  "+clackui.TitleStyle.Render("void-code")+"  "+clackui.TitleStyle.Render(version.Version)) + "\n")
	sb.WriteString(clackui.RailLine("│", "") + "\n")
	if m.view == topUpView {
		sb.WriteString(clackui.RailLine("◇", "  "+clackui.InfoTextStyle.Render("Top up your balance")) + "\n")
		sb.WriteString(clackui.RailLine("│", "  "+clackui.InfoTextStyle.Render("Text @makscee on Telegram to top up your balance.")) + "\n")
		sb.WriteString(clackui.RailLine("└", "  "+clackui.HintStyle.Render("press any key to go back")) + "\n")
		return sb.String()
	}
	if m.LoggedIn {
		sb.WriteString(clackui.RailLine("◇", "  "+clackui.InfoTextStyle.Render(identityDisplay(m.Identity, m.IdentityUnverified)+" · "+FormatBalance(m.BalanceUsd))) + "\n")
	} else {
		sb.WriteString(clackui.RailLine("◇", "  "+clackui.WarnStyle.Render("Not logged in")) + "\n")
	}
	if m.UpdateNudge != "" {
		sb.WriteString(clackui.RailLine("◇", "  "+clackui.HintStyle.Render(m.UpdateNudge)) + "\n")
	}
	sb.WriteString(clackui.RailLine("│", "") + "\n")
	sb.WriteString(clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render("What now?")) + "\n")
	for i, item := range m.items {
		style := clackui.UnselectedItemStyle
		marker := "○"
		if i == m.cursor {
			style, marker = clackui.SelectedItemStyle, "●"
		}
		sb.WriteString(clackui.RailLine("│", "  "+style.Render(marker+"  "+item.label)) + "\n")
	}
	sb.WriteString(clackui.RailLine("│", "") + "\n")
	sb.WriteString(clackui.RailLine("└", "  "+clackui.HintStyle.Render("↑/↓ · enter · q quit")) + "\n")
	return sb.String()
}
func plainBanner(state AuthState) string {
	var sb strings.Builder
	sb.WriteString("\nvoid-code " + version.Version + " — subscription console — by makscee.ru\n\n")
	if state.LoggedIn {
		if state.IdentityUnverified {
			sb.WriteString("  Identity: " + identityDisplay(state.Identity, true) + "\n")
		} else {
			sb.WriteString("  Logged in as " + state.Identity + "\n")
		}
		sb.WriteString("  " + FormatBalance(state.BalanceUsd) + "\n")
	} else {
		sb.WriteString("  Not logged in\n")
	}
	if state.UpdateNudge != "" {
		sb.WriteString("  " + state.UpdateNudge + "\n")
	}
	return sb.String()
}
