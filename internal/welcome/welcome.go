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
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/provider"
	"github.com/makscee/void-code/internal/version"
)

// AuthState carries the information to display on the landing screen.
type AuthState struct {
	// LoggedIn reports whether a valid token is present.
	LoggedIn bool
	// Identity is the user identity string (email or userId) when logged in.
	Identity string
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
	// VCD-65: SubDaysLeft and SubUnknown removed — subscriptionGate removed.
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
	// ShowProviders is an INTERNAL sentinel (in-TUI navigation, never returned by Run).
	ShowProviders
	// RunStatusline means the user chose "Install statusline" — caller runs the install flow.
	RunStatusline
)

// Callbacks holds I/O functions for the Providers sub-view. Passed into Run
// so the welcome package stays decoupled from keystore/provider I/O.
// Default (zero) values are no-ops — safe for non-TTY / test paths.
type Callbacks struct {
	// KeyNames is the list of saved key names shown in the Providers menu.
	KeyNames []string
	// ActiveProvider is the persisted-string form of the currently active provider.
	ActiveProvider string
	// OnSelect is called when the user selects a provider row. May be nil.
	OnSelect func(provider.Provider) error
	// OnAddKey is called when the Add-key flow completes. May be nil.
	OnAddKey func(name, token string) error
	// OnDeleteKey is called when the user confirms deletion of a named key. May be nil.
	OnDeleteKey func(name string) error
}

// Run shows the interactive selectable menu and blocks until the user makes a
// choice (or quits). Returns one of SpawnClaude / RunLogin / RunDoctor / RunStatusline / Quit.
//
// In non-TTY environments (CI, pipe) it falls back to a plain-text banner
// and returns SpawnClaude (logged-in) or RunLogin (logged-out).
func Run(state AuthState, cb Callbacks) (RunResult, error) {
	m := newModel(state, cb)
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

// clack rail styles — aliases to clackui shared package.
var (
	railStyle           = clackui.RailStyle
	titleStyle          = clackui.TitleStyle
	infoTextStyle       = clackui.InfoTextStyle
	selectedItemStyle   = clackui.SelectedItemStyle
	unselectedItemStyle = clackui.UnselectedItemStyle
	hintStyle           = clackui.HintStyle
	warnStyle           = clackui.WarnStyle
	topUpStyle          = clackui.InfoTextStyle

	// Legacy styles kept for plainBanner / BudgetWarning callers.
	subStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA"))
	// boxStyle retained for backward compat (unused in clack View).
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(1, 3)
)

// viewState distinguishes the top-level menu from sub-views.
type viewState int

const (
	menuView      viewState = iota
	topUpView               // in-TUI info screen; any key returns to menu
	providersView           // Providers radio list
	addKeyView              // Add-key two-stage text input
	deleteView              // Delete-key confirm dialog
)

// menuItem pairs a display label with the RunResult it produces on activation.
type menuItem struct {
	label  string
	result RunResult
}

type model struct {
	AuthState
	items     []menuItem
	cursor    int
	view      viewState
	result    RunResult
	chosen    bool // true once a process-exiting result was selected
	quitting  bool
	providers     providersModel
	addKey        addKeyModel
	deleteConfirm deleteConfirmModel
	cb            Callbacks // I/O callbacks (provider select, add key, delete key)
}

func menuItemsFor(state AuthState) []menuItem {
	if !state.LoggedIn {
		return []menuItem{{label: "Login", result: RunLogin}}
	}
	return []menuItem{
		{label: "Start", result: SpawnClaude},
		{label: "Providers", result: ShowProviders},
		{label: "Top up", result: ShowTopUp},
		{label: "Run doctor", result: RunDoctor},
		{label: "Install statusline", result: RunStatusline},
	}
}

func newModel(state AuthState, cb Callbacks) model {
	return model{
		AuthState: state,
		items:     menuItemsFor(state),
		view:      menuView,
		cb:        cb,
	}
}

func (m model) Init() tea.Cmd { return nil }

// ─── white-box test accessors ────────────────────────────────────────────────

// NewMenuModelForTest exposes newModel for package-external tests.
func NewMenuModelForTest(state AuthState) model { return newModel(state, Callbacks{}) }

func (m model) Cursor() int            { return m.cursor }
func (m model) ItemCount() int         { return len(m.items) }
func (m model) ItemLabel(i int) string { return m.items[i].label }
func (m model) SetCursor(i int) model  { m.cursor = i; return m }

// SetAddKeyView puts the model into addKeyView with a fresh addKeyModel (test accessor).
func (m model) SetAddKeyView() model { m.view = addKeyView; m.addKey = newAddKeyModel(); return m }

// AddKeyBuf returns the current text buffer of the addKey sub-model (test accessor).
func (m model) AddKeyBuf() string { return m.addKey.buf }
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

// ActiveProviderString returns the in-memory ActiveProvider string (test accessor).
func (m model) ActiveProviderString() string { return m.cb.ActiveProvider }

// KeyNames returns the in-memory key names slice (test accessor).
func (m model) KeyNames() []string { return m.cb.KeyNames }

// NewMenuModelForDeleteTest creates a menu model wired with the given Callbacks
// for driving delete-key integration tests.
func NewMenuModelForDeleteTest(cb Callbacks) model {
	return newModel(AuthState{LoggedIn: true}, cb)
}

// SimulateDeleteKey drives the model as if the user opened the Providers menu,
// navigated to the named key, pressed d, and then confirmed (confirm=true) or
// cancelled (confirm=false) the deletion.
func (m model) SimulateDeleteKey(keyName string, confirm bool) model {
	// Enter providers view.
	m.providers = newProvidersModel(m.cb.KeyNames, m.cb.ActiveProvider)
	m.view = providersView

	// Find the row index for keyName.
	targetIdx := -1
	for i, r := range m.providers.rows {
		if r.prov.Kind == provider.NamedKey && r.prov.Name == keyName {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return m // key not found — return unchanged
	}
	m.providers.cursor = targetIdx

	// Press d → enter deleteView.
	next, _ := m.updateProviders("d")
	m = next.(model)
	if m.view != deleteView {
		return m // delete not triggered
	}

	// Press y or n.
	key := "n"
	if confirm {
		key = "y"
	}
	next2, _ := m.updateDeleteConfirm(key)
	return next2.(model)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// Bracketed-paste (bubbletea v1.3.x): bracketed paste is ON by default.
	// A paste arrives as tea.KeyMsg{Type: KeyRunes, Paste: true, Runes: [...]}.
	// key.String() wraps it in "[...]" which breaks all key-name comparisons, so
	// we intercept paste events here and route them directly to the active text
	// input before falling through to normal key dispatch.
	if key.Paste && m.view == addKeyView {
		m.addKey = m.addKey.handlePaste(string(key.Runes))
		return m, nil
	}

	s := key.String()

	if m.view == topUpView {
		// Any key returns to the menu.
		m.view = menuView
		return m, nil
	}

	if m.view == providersView {
		return m.updateProviders(s)
	}

	if m.view == addKeyView {
		return m.updateAddKey(s)
	}

	if m.view == deleteView {
		return m.updateDeleteConfirm(s)
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
		if r == ShowProviders {
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.ActiveProvider)
			m.view = providersView
			return m, nil
		}
		m.result = r
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// updateProviders handles keystrokes in the Providers sub-view.
func (m model) updateProviders(s string) (tea.Model, tea.Cmd) {
	n := len(m.providers.rows)
	switch s {
	case "ctrl+c":
		m.result = Quit
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	case "esc", "q":
		m.view = menuView
		return m, nil
	case "up", "k":
		m.providers.cursor = ((m.providers.cursor-1)%n + n) % n
		return m, nil
	case "down", "j":
		m.providers.cursor = (m.providers.cursor + 1) % n
		return m, nil
	case "d", "D":
		// Delete: only for named-key rows.
		if m.providers.RowIsDeletable(m.providers.cursor) {
			row := m.providers.rows[m.providers.cursor]
			m.deleteConfirm = newDeleteConfirmModel(row.prov.Name)
			m.view = deleteView
			return m, nil
		}
		return m, nil
	case "enter", " ":
		row := m.providers.rows[m.providers.cursor]
		if row.addKey {
			m.addKey = newAddKeyModel()
			m.view = addKeyView
			return m, nil
		}
		// Select this provider.
		if m.cb.OnSelect != nil {
			_ = m.cb.OnSelect(row.prov)
		}
		m.cb.ActiveProvider = row.prov.String()
		m.providers.active = row.prov.String()
		m.view = menuView
		return m, nil
	}
	return m, nil
}

// updateDeleteConfirm handles keystrokes in the delete-key confirm dialog.
func (m model) updateDeleteConfirm(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "ctrl+c":
		m.result = Quit
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	default:
		m.deleteConfirm = m.deleteConfirm.handleKey(s)
		if m.deleteConfirm.Confirmed() {
			keyName := m.deleteConfirm.KeyName()
			// Invoke the delete callback.
			if m.cb.OnDeleteKey != nil {
				_ = m.cb.OnDeleteKey(keyName)
			}
			// Remove from in-memory key list.
			newNames := make([]string, 0, len(m.cb.KeyNames))
			for _, n := range m.cb.KeyNames {
				if n != keyName {
					newNames = append(newNames, n)
				}
			}
			m.cb.KeyNames = newNames
			// If the deleted key was the active provider, reset to relay.
			if m.cb.ActiveProvider == "key:"+keyName {
				m.cb.ActiveProvider = "relay"
				if m.cb.OnSelect != nil {
					_ = m.cb.OnSelect(provider.Provider{Kind: provider.Relay})
				}
			}
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.ActiveProvider)
			m.view = providersView
			return m, nil
		}
		if m.deleteConfirm.Cancelled() {
			m.view = providersView
			return m, nil
		}
	}
	return m, nil
}

// updateAddKey handles keystrokes in the Add-key text-input sub-view.
func (m model) updateAddKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "ctrl+c":
		m.result = Quit
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.view = providersView
		return m, nil
	default:
		m.addKey = m.addKey.handleKey(s)
		if m.addKey.Done() {
			// Persist the new key.
			if m.cb.OnAddKey != nil {
				_ = m.cb.OnAddKey(m.addKey.Name(), m.addKey.Token())
			}
			// Rebuild providers list to include the new key.
			if m.cb.OnAddKey != nil {
				// Append the new key name if not already present.
				found := false
				for _, n := range m.cb.KeyNames {
					if n == m.addKey.Name() {
						found = true
						break
					}
				}
				if !found {
					m.cb.KeyNames = append(m.cb.KeyNames, m.addKey.Name())
				}
			}
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.ActiveProvider)
			m.view = providersView
		}
		return m, nil
	}
}

// railLine returns a line with the left rail prefix.
// prefix is the rail/cap glyph(s) rendered in railStyle,
// content is the rest of the line (already styled by caller).
func railLine(prefix, content string) string {
	return clackui.RailLine(prefix, content)
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

	if m.view == providersView {
		sb.WriteString(m.providers.render())
		return sb.String()
	}

	if m.view == addKeyView {
		sb.WriteString(m.addKey.render())
		return sb.String()
	}

	if m.view == deleteView {
		sb.WriteString(m.deleteConfirm.render())
		return sb.String()
	}

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

	// ◇  Provider: <active provider label>  — visible without entering Providers sub-menu.
	activeProv := provider.Parse(m.cb.ActiveProvider)
	sb.WriteString(railLine("◇", "  "+hintStyle.Render("Provider: "+activeProv.Label())))
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
