// Package welcome implements the persistent landing screen for vc.
//
// The landing screen is shown on every bare `vc` invocation (no sub-command).
// It displays auth state + subscription info and waits for any keypress.
// Logged-in: any key → spawn claude.
// Logged-out: any key → vc login flow (caller interprets LoginRequested).
package welcome

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/version"
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
	// UpdateNudge is an optional one-line message shown in the banner when
	// the user declined an update or when no auto-update is configured.
	// Empty means no nudge.
	UpdateNudge string
	// BudgetPct is the month-to-date spend as a percentage of budget.
	// nil = no budget set (server absent / budget_usd=0 → no cap) → do not display.
	// ≥80 = warn; ≥100 = block copy.
	BudgetPct *float64
	// BalanceUsd is the prepaid wallet balance in USD (VCD-55). nil = unknown /
	// server did not return it → render a neutral dash, never a $ figure.
	BalanceUsd *float64
}

// RunResult is returned by Run to indicate what the keypress should trigger.
type RunResult int

const (
	// SpawnClaude means the user chose Start (logged in) — spawn claude.
	SpawnClaude RunResult = iota
	// RunLogin means the user is logged out and chose Login — run vc login.
	RunLogin
	// RunDoctor means the user chose Run doctor — caller runs doctor, then re-shows menu.
	RunDoctor
	// Quit means the user pressed q/ctrl-c/esc at the top level — exit without spawning.
	Quit
	// ShowTopUp is an INTERNAL sentinel (in-TUI navigation, never returned by Run).
	ShowTopUp
)

// Run shows the interactive selectable menu and blocks until the user makes a
// choice (or quits). Returns one of SpawnClaude / RunLogin / RunDoctor / Quit.
//
// In non-TTY environments (CI, pipe) it falls back to a plain-text banner
// and returns SpawnClaude (logged-in) or RunLogin (logged-out).
func Run(state AuthState) (RunResult, error) {
	m := newModel(state)
	p := tea.NewProgram(m)
	out, err := p.Run()
	if err != nil {
		// Non-TTY fallback: print a plain banner, pick safe default.
		fmt.Print(plainBanner(state))
		if !state.LoggedIn {
			return RunLogin, nil
		}
		return SpawnClaude, nil
	}
	fm, ok := out.(model)
	if !ok || !fm.chosen {
		// Closed without an explicit choice (e.g. terminal closed) → safe default.
		if !state.LoggedIn {
			return RunLogin, nil
		}
		return Quit, nil
	}
	return fm.result, nil
}

// FormatBalance renders the prepaid balance for the header.
//
//	nil → "—"           (unknown / server absent)
//	v   → "$X.XX left"  (2-decimal)
func FormatBalance(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("$%.2f left", *v)
}

// ─── budget warning (VCD-49) ─────────────────────────────────────────────────

// BudgetWarning returns a user-facing copy string for the budget state, or ""
// when no warning is needed.
//
//	pct == nil       → no budget configured (or server absent) → ""
//	pct < 80         → healthy → ""
//	80 <= pct < 100  → warn: approaching cap
//	pct >= 100       → block copy: budget reached
//
// The caller decides whether to block or just warn; this function only returns
// the copy. The relay enforces the actual hard block — client copy is additive.
func BudgetWarning(pct *float64) string {
	if pct == nil {
		return ""
	}
	p := *pct
	switch {
	case p >= 100:
		return "Monthly budget reached — message @makscee on Telegram to top up."
	case p >= 80:
		return fmt.Sprintf("Budget at %.0f%% — top up via @makscee on Telegram before you hit the cap.", p)
	default:
		return ""
	}
}

// budgetLeftPct returns (N% left, show) where N = round(100 - pct), clamped to [0,100].
// Returns (0, false) when pct is nil (no budget cap) — hide the line entirely.
func budgetLeftPct(pct *float64) (int, bool) {
	if pct == nil {
		return 0, false
	}
	left := 100.0 - *pct
	if left < 0 {
		left = 0
	}
	return int(math.Round(left)), true
}

// ─── subscription warning ────────────────────────────────────────────────────

// SubscriptionWarning returns the soft ≤3-day expiry warning copy, or "" when
// days-left is outside the 1–3 band.  Days=0 is handled upstream as a hard block.
func SubscriptionWarning(daysLeft int) string {
	if daysLeft < 1 || daysLeft > 3 {
		return ""
	}
	unit := "days"
	if daysLeft == 1 {
		unit = "day"
	}
	return fmt.Sprintf(
		"Subscription ending in %d %s — top up via @makscee on Telegram to avoid interruption.",
		daysLeft, unit)
}

// PlainBannerForTest exposes plainBanner for white-box testing from the
// welcome_test package without making it part of the public API.
func PlainBannerForTest(state AuthState) string {
	return plainBanner(state)
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

// clack rail styles — replicate @clack/prompts left-rail aesthetic in lipgloss.
var (
	// railStyle renders the continuous left rail │ and caps ┌ └.
	railStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))

	// titleStyle: bold white title text next to ┌ cap.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))

	// infoTextStyle: text on the ◇ info line (identity · balance).
	infoTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))

	// selectedItemStyle: ● selected menu item label.
	selectedItemStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED"))

	// unselectedItemStyle: ○ unselected menu item label.
	unselectedItemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	// hintStyle: bottom cap hint text.
	hintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))

	// warnStyle: warning text (logged-out state).
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B"))

	// topUpStyle: top-up instruction text.
	topUpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))

	// Legacy styles kept for plainBanner / BudgetWarning callers.
	subStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	// boxStyle retained for backward compat (unused in clack View).
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 3)
)

// viewState distinguishes the top-level menu from the Top-up info sub-view.
type viewState int

const (
	menuView  viewState = iota
	topUpView           // in-TUI info screen; any key returns to menu
)

// menuItem pairs a display label with the RunResult it produces on activation.
type menuItem struct {
	label  string
	result RunResult
}

type model struct {
	AuthState
	items    []menuItem
	cursor   int
	view     viewState
	result   RunResult
	chosen   bool // true once a process-exiting result was selected
	quitting bool
}

func menuItemsFor(state AuthState) []menuItem {
	if !state.LoggedIn {
		return []menuItem{{label: "Login", result: RunLogin}}
	}
	// DEFERRED: "Disable relay" item intentionally omitted (operator deferred 2026-05-30).
	return []menuItem{
		{label: "Start", result: SpawnClaude},
		{label: "Top up", result: ShowTopUp},
		{label: "Run doctor", result: RunDoctor},
	}
}

func newModel(state AuthState) model {
	return model{AuthState: state, items: menuItemsFor(state), view: menuView}
}

func (m model) Init() tea.Cmd { return nil }

// ─── white-box test accessors ────────────────────────────────────────────────

// NewMenuModelForTest exposes newModel for package-external tests.
func NewMenuModelForTest(state AuthState) model { return newModel(state) }

func (m model) Cursor() int            { return m.cursor }
func (m model) ItemCount() int         { return len(m.items) }
func (m model) ItemLabel(i int) string { return m.items[i].label }
func (m model) SetCursor(i int) model  { m.cursor = i; return m }
func (m model) MoveCursor(d int) model {
	n := len(m.items)
	m.cursor = ((m.cursor+d)%n + n) % n
	return m
}

// Activate returns the RunResult for the item under the cursor. Callers check
// whether the result is ShowTopUp (in-TUI navigation) or a process-exiting value.
func (m model) Activate() RunResult { return m.items[m.cursor].result }

// SetTopUpView sets the model into the top-up sub-view (for tests).
func (m model) SetTopUpView() model { m.view = topUpView; return m }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	s := key.String()

	if m.view == topUpView {
		// Any key returns to the menu.
		m.view = menuView
		return m, nil
	}

	switch s {
	case "ctrl+c", "q", "esc":
		m.result = Quit
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		m = m.MoveCursor(-1)
		return m, nil
	case "down", "j":
		m = m.MoveCursor(1)
		return m, nil
	case "enter", " ":
		r := m.Activate()
		if r == ShowTopUp {
			m.view = topUpView
			return m, nil
		}
		m.result = r
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// railLine returns a line with the left rail prefix.
// prefix is the rail/cap glyph(s) rendered in railStyle,
// content is the rest of the line (already styled by caller).
func railLine(prefix, content string) string {
	return railStyle.Render(prefix) + content
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var sb strings.Builder

	// ┌  void-code  <version>
	sb.WriteString(railLine("┌", "  "+titleStyle.Render("void-code")+"  "+titleStyle.Render(version.Version)))
	sb.WriteString("\n")

	// │  (blank separator)
	sb.WriteString(railLine("│", ""))
	sb.WriteString("\n")

	if m.view == topUpView {
		// Top-up sub-view — clack style with ◇ info marker.
		// ◇  Top up your balance
		sb.WriteString(railLine("◇", "  "+topUpStyle.Render("Top up your balance")))
		sb.WriteString("\n")
		// │
		sb.WriteString(railLine("│", ""))
		sb.WriteString("\n")
		// │  Text @makscee on Telegram to top up your balance.
		sb.WriteString(railLine("│", "  "+topUpStyle.Render("Text @makscee on Telegram to top up your balance.")))
		sb.WriteString("\n")
		// │
		sb.WriteString(railLine("│", ""))
		sb.WriteString("\n")
		// └  press any key to go back
		sb.WriteString(railLine("└", "  "+hintStyle.Render("press any key to go back")))
		sb.WriteString("\n")
		return sb.String()
	}

	// ◇  identity · $X.XX left  (or "Not logged in")
	if m.LoggedIn {
		infoText := m.Identity + " · " + FormatBalance(m.BalanceUsd)
		sb.WriteString(railLine("◇", "  "+infoTextStyle.Render(infoText)))
	} else {
		sb.WriteString(railLine("◇", "  "+warnStyle.Render("Not logged in")))
	}
	sb.WriteString("\n")

	// Update nudge (if present) — shown as an extra ◇ line.
	if m.UpdateNudge != "" {
		sb.WriteString(railLine("│", ""))
		sb.WriteString("\n")
		sb.WriteString(railLine("◇", "  "+hintStyle.Render(m.UpdateNudge)))
		sb.WriteString("\n")
	}

	// │  (blank separator)
	sb.WriteString(railLine("│", ""))
	sb.WriteString("\n")

	// ◆  What now?
	sb.WriteString(railLine("◆", "  "+infoTextStyle.Render("What now?")))
	sb.WriteString("\n")

	// │  ●  Start   (selected, purple)
	// │  ○  Top up  (unselected, dim)
	for i, it := range m.items {
		if i == m.cursor {
			sb.WriteString(railLine("│", "  "+selectedItemStyle.Render("●  "+it.label)))
		} else {
			sb.WriteString(railLine("│", "  "+unselectedItemStyle.Render("○  "+it.label)))
		}
		sb.WriteString("\n")
	}

	// │  (blank separator)
	sb.WriteString(railLine("│", ""))
	sb.WriteString("\n")

	// └  ↑/↓ · enter · q quit
	sb.WriteString(railLine("└", "  "+hintStyle.Render("↑/↓ · enter · q quit")))
	sb.WriteString("\n")

	return sb.String()
}

// plainBanner returns a plain-text version of the landing screen (no ANSI).
// Used as a non-TTY fallback when the bubbletea program cannot run.
func plainBanner(state AuthState) string {
	var sb strings.Builder
	sb.WriteString("\nvoid-code " + version.Version + " — relay harness for Claude Code — by makscee.ru\n\n")
	if state.LoggedIn {
		sb.WriteString("  Logged in as " + state.Identity + "\n")
		sb.WriteString("  " + FormatBalance(state.BalanceUsd) + "\n")
	} else {
		sb.WriteString("  Not logged in\n")
	}
	if state.UpdateNudge != "" {
		sb.WriteString("  " + state.UpdateNudge + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}
