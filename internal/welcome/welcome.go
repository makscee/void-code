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
	inner.WriteString(titleStyle.Render("void-code " + version.Version))
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
		// Soft ≤3-day expiry warning (1–3 days): shown below the subscription line.
		if w := SubscriptionWarning(m.SubDaysLeft); w != "" {
			inner.WriteString("\n")
			inner.WriteString(warnStyle.Render(w))
		}
		// VCD-49 budget warning: shown when pct ≥80 (warn) or ≥100 (block copy).
		if w := BudgetWarning(m.BudgetPct); w != "" {
			inner.WriteString("\n")
			inner.WriteString(warnStyle.Render(w))
		}
		// VCD-54: budget-left line — shown always when a budget cap exists (pct != nil).
		if n, show := budgetLeftPct(m.BudgetPct); show {
			inner.WriteString("\n")
			inner.WriteString(subStyle.Render(fmt.Sprintf("%d%% budget left", n)))
		}
		inner.WriteString("\n\n")
		if m.UpdateNudge != "" {
			inner.WriteString(hintStyle.Render(m.UpdateNudge))
			inner.WriteString("\n")
		}
		inner.WriteString(hintStyle.Render("press any key to start"))
	} else {
		inner.WriteString(warnStyle.Render("Not logged in"))
		inner.WriteString("\n\n")
		if m.UpdateNudge != "" {
			inner.WriteString(hintStyle.Render(m.UpdateNudge))
			inner.WriteString("\n")
		}
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
	sb.WriteString("\nvoid-code " + version.Version + " — relay harness for Claude Code — by makscee.ru\n\n")
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
	// Soft ≤3-day expiry warning.
	if w := SubscriptionWarning(state.SubDaysLeft); w != "" {
		sb.WriteString("  " + w + "\n")
	}
	// VCD-49 budget warning.
	if w := BudgetWarning(state.BudgetPct); w != "" {
		sb.WriteString("  " + w + "\n")
	}
	// VCD-54: budget-left line — shown always when a budget cap exists (pct != nil).
	if n, show := budgetLeftPct(state.BudgetPct); show {
		sb.WriteString(fmt.Sprintf("  %d%% budget left\n", n))
	}
	if state.UpdateNudge != "" {
		sb.WriteString("  " + state.UpdateNudge + "\n")
	}
	sb.WriteString("\n")
	return sb.String()
}
