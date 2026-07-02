// Package welcome implements the persistent landing screen for vc.
//
// The landing screen is shown on every bare `vc` invocation (no sub-command).
// It displays auth state + subscription info and waits for any keypress.
// Logged-in: any key → spawn the active harness.
// Logged-out: any key → vc login flow (caller interprets LoginRequested).
package welcome

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/harnesschoice"
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
	// ShowHarnesses is an INTERNAL sentinel (in-TUI navigation, never returned by Run).
	ShowHarnesses
	// RunInstallPi means the user selected Pi while it is not installed.
	RunInstallPi
	// RunInstallClaude means the user selected Claude Code while it is not installed.
	RunInstallClaude
	// RunInstallCodex means the user selected OpenAI Codex while it is not installed.
	RunInstallCodex
	// RunStatusline means the user chose "Install statusline" — caller runs the install flow.
	RunStatusline
	// RunProfile means the user chose "Open profile" — caller opens the profile URL in a browser.
	RunProfile
)

// Callbacks holds I/O functions for the Providers sub-view. Passed into Run
// so the welcome package stays decoupled from keystore/provider I/O.
// Default (zero) values are no-ops — safe for non-TTY / test paths.
type Callbacks struct {
	// KeyNames is the list of saved key names shown in the Providers menu.
	KeyNames []string
	// ActiveProvider is the persisted-string form of the currently active provider.
	ActiveProvider string
	// ActiveProviderLabel is the human-facing display label for the active provider.
	// Populated at startup from provider.LoadLabel() so the view never shows raw ids.
	// If empty, the view falls back to provider.Parse(ActiveProvider).Label().
	ActiveProviderLabel string
	// GrantedProviders is the user's relay-routed granted-provider list (VCD-72),
	// fetched from void-auth GET /v1/vc/providers. Empty for ungranted users.
	GrantedProviders []ProviderRowInfo
	// ActiveHarness is the persisted-string form of the currently active harness.
	ActiveHarness string
	// ActiveHarnessLabel is the human-facing display label for the active harness.
	// If empty, the view falls back to harnesschoice.Parse(ActiveHarness).Label().
	ActiveHarnessLabel string
	// ClaudeInstalled reports whether the claude binary is available on PATH.
	ClaudeInstalled bool
	// CodexInstalled reports whether the codex binary is available on PATH.
	CodexInstalled bool
	// PiInstalled reports whether the pi binary is available on PATH.
	PiInstalled bool
	// OnSelectHarness is called when the user selects an installed harness row.
	OnSelectHarness func(harnesschoice.Choice) error
	// OnSelect is called when the user selects a provider row. May be nil.
	OnSelect func(provider.Provider) error
	// OnSelectLabel is called with the display label of the selected row when a
	// provider is chosen. May be nil. Used to persist the label at selection time
	// so the statusline renderer can show it without a network call.
	OnSelectLabel func(label string) error
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

// PlainBannerForTest exposes plainBanner for white-box testing from the
// welcome_test package without making it part of the public API.
func PlainBannerForTest(state AuthState) string {
	return plainBanner(state)
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
)

// viewState distinguishes the top-level menu from sub-views.
type viewState int

const (
	menuView      viewState = iota
	topUpView               // in-TUI info screen; any key returns to menu
	providersView           // Providers radio list
	harnessesView           // Harness radio list
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
	items         []menuItem
	cursor        int
	view          viewState
	result        RunResult
	chosen        bool // true once a process-exiting result was selected
	quitting      bool
	providers     providersModel
	harnesses     harnessesModel
	addKey        addKeyModel
	deleteConfirm deleteConfirmModel
	cb            Callbacks // I/O callbacks (provider select, add key, delete key)
}

func menuItemsFor(state AuthState, cb Callbacks) []menuItem {
	if !state.LoggedIn {
		return []menuItem{{label: "Login", result: RunLogin}}
	}
	return []menuItem{
		{label: "Start", result: SpawnClaude},
		{label: "Change harness", result: ShowHarnesses},
		{label: "Change provider", result: ShowProviders},
		{label: "Top up", result: ShowTopUp},
		{label: "Run doctor", result: RunDoctor},
		{label: "Install statusline", result: RunStatusline},
		{label: "Open profile", result: RunProfile},
	}
}

func activeProviderLabel(cb Callbacks) string {
	if cb.ActiveProviderLabel != "" {
		return cb.ActiveProviderLabel
	}
	return provider.Parse(cb.ActiveProvider).Label()
}

func activeHarnessLabel(cb Callbacks) string {
	if cb.ActiveHarnessLabel != "" {
		return cb.ActiveHarnessLabel
	}
	return harnesschoice.Parse(cb.ActiveHarness).Label()
}

func (m *model) refreshMenuItems() {
	m.items = menuItemsFor(m.AuthState, m.cb)
}

func newModel(state AuthState, cb Callbacks) model {
	m := model{
		AuthState: state,
		view:      menuView,
		cb:        cb,
	}
	m.refreshMenuItems()
	return m
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
	// Enter a legacy key-management providers view. The PRD-088 main selector
	// hides named keys, but the delete-key model/tests still exercise storage cleanup.
	legacyRows := make([]providerRow, 0, len(m.cb.KeyNames))
	for _, n := range m.cb.KeyNames {
		p := provider.Provider{Kind: provider.NamedKey, Name: n}
		legacyRows = append(legacyRows, providerRow{label: p.Label(), prov: p})
	}
	m.providers = providersModel{rows: legacyRows, active: m.cb.ActiveProvider}
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

	if m.view == harnessesView {
		return m.updateHarnesses(s)
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
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.GrantedProviders, m.cb.ActiveProvider)
			m.view = providersView
			return m, nil
		}
		if r == ShowHarnesses {
			m.harnesses = newHarnessesModel(m.cb.ActiveHarness, m.cb.ClaudeInstalled, m.cb.CodexInstalled, m.cb.PiInstalled)
			m.view = harnessesView
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
	if n == 0 {
		m.view = menuView
		return m, nil
	}
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
		// Delete remains available for legacy named-key management rows.
		if m.providers.RowIsDeletable(m.providers.cursor) {
			row := m.providers.rows[m.providers.cursor]
			m.deleteConfirm = newDeleteConfirmModel(row.prov.Name)
			m.view = deleteView
			return m, nil
		}
		return m, nil
	case "enter", " ":
		row := m.providers.rows[m.providers.cursor]
		m.applyReconciledSelection(harnesschoice.Parse(m.cb.ActiveHarness), row.prov, row.label)
		m.providers.active = m.cb.ActiveProvider
		m.view = menuView
		m.refreshMenuItems()
		return m, nil
	}
	return m, nil
}

// updateHarnesses handles keystrokes in the Harness radio sub-view.
func (m model) updateHarnesses(s string) (tea.Model, tea.Cmd) {
	n := len(m.harnesses.rows)
	if n == 0 {
		m.view = menuView
		return m, nil
	}
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
		m.harnesses.cursor = ((m.harnesses.cursor-1)%n + n) % n
		return m, nil
	case "down", "j":
		m.harnesses.cursor = (m.harnesses.cursor + 1) % n
		return m, nil
	case "enter", " ":
		row := m.harnesses.rows[m.harnesses.cursor]
		if row.installResult != SpawnClaude {
			m.result = row.installResult
			m.chosen = true
			m.quitting = true
			return m, tea.Quit
		}
		m.applyReconciledSelection(row.choice, provider.Parse(m.cb.ActiveProvider), m.cb.ActiveProviderLabel)
		m.harnesses.active = m.cb.ActiveHarness
		m.view = menuView
		m.refreshMenuItems()
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
				relayProv := provider.Provider{Kind: provider.Relay}
				m.cb.ActiveProviderLabel = relayProv.Label()
				if m.cb.OnSelect != nil {
					_ = m.cb.OnSelect(relayProv)
				}
				if m.cb.OnSelectLabel != nil {
					_ = m.cb.OnSelectLabel(relayProv.Label())
				}
				m.refreshMenuItems()
			}
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.GrantedProviders, m.cb.ActiveProvider)
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
			m.providers = newProvidersModel(m.cb.KeyNames, m.cb.GrantedProviders, m.cb.ActiveProvider)
			m.view = providersView
		}
		return m, nil
	}
}

func (m *model) applyReconciledSelection(h harnesschoice.Choice, p provider.Provider, label string) {
	grants := make([]compat.Grant, len(m.cb.GrantedProviders))
	for i, g := range m.cb.GrantedProviders {
		grants[i] = compat.Grant{ID: g.ID, Name: g.Name, Type: g.Type}
	}
	d := compat.Reconcile(h, p, label, grants)
	if m.cb.OnSelectHarness != nil {
		_ = m.cb.OnSelectHarness(d.Harness)
	}
	if m.cb.OnSelect != nil {
		_ = m.cb.OnSelect(d.Provider)
	}
	if m.cb.OnSelectLabel != nil {
		_ = m.cb.OnSelectLabel(d.ProviderLabel)
	}
	m.cb.ActiveHarness = d.Harness.String()
	m.cb.ActiveHarnessLabel = d.Harness.Label()
	m.cb.ActiveProvider = d.Provider.String()
	m.cb.ActiveProviderLabel = d.ProviderLabel
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	var sb strings.Builder

	// ┌  void-code  <version>
	sb.WriteString(clackui.RailLine("┌", "  "+titleStyle.Render("void-code")+"  "+titleStyle.Render(version.Version)))
	sb.WriteString("\n")

	// │  (blank separator)
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")

	if m.view == providersView {
		sb.WriteString(m.providers.render())
		return sb.String()
	}

	if m.view == harnessesView {
		sb.WriteString(m.harnesses.render())
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
		sb.WriteString(clackui.RailLine("◇", "  "+topUpStyle.Render("Top up your balance")))
		sb.WriteString("\n")
		// │
		sb.WriteString(clackui.RailLine("│", ""))
		sb.WriteString("\n")
		// │  Text @makscee on Telegram to top up your balance.
		sb.WriteString(clackui.RailLine("│", "  "+topUpStyle.Render("Text @makscee on Telegram to top up your balance.")))
		sb.WriteString("\n")
		// │
		sb.WriteString(clackui.RailLine("│", ""))
		sb.WriteString("\n")
		// └  press any key to go back
		sb.WriteString(clackui.RailLine("└", "  "+hintStyle.Render("press any key to go back")))
		sb.WriteString("\n")
		return sb.String()
	}

	// ◇  identity · $X.XX left  (or "Not logged in")
	if m.LoggedIn {
		infoText := m.Identity + " · " + FormatBalance(m.BalanceUsd)
		sb.WriteString(clackui.RailLine("◇", "  "+infoTextStyle.Render(infoText)))
	} else {
		sb.WriteString(clackui.RailLine("◇", "  "+warnStyle.Render("Not logged in")))
	}
	sb.WriteString("\n")

	// Update nudge (if present) — shown as an extra ◇ line.
	if m.UpdateNudge != "" {
		sb.WriteString(clackui.RailLine("│", ""))
		sb.WriteString("\n")
		sb.WriteString(clackui.RailLine("◇", "  "+hintStyle.Render(m.UpdateNudge)))
		sb.WriteString("\n")
	}

	// │  (blank separator)
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")

	if m.LoggedIn {
		sb.WriteString(m.renderMatrixSummary())
		sb.WriteString(clackui.RailLine("│", ""))
		sb.WriteString("\n")
	}

	// ◆  What now?
	sb.WriteString(clackui.RailLine("◆", "  "+infoTextStyle.Render("What now?")))
	sb.WriteString("\n")

	// │  ●  Start   (selected, purple)
	// │  ○  Top up  (unselected, dim)
	for i, it := range m.items {
		if i == m.cursor {
			sb.WriteString(clackui.RailLine("│", "  "+selectedItemStyle.Render("●  "+it.label)))
		} else {
			sb.WriteString(clackui.RailLine("│", "  "+unselectedItemStyle.Render("○  "+it.label)))
		}
		sb.WriteString("\n")
	}

	// │  (blank separator)
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")

	// └  ↑/↓ · enter · q quit
	sb.WriteString(clackui.RailLine("└", "  "+hintStyle.Render("↑/↓ · enter · q quit")))
	sb.WriteString("\n")

	return sb.String()
}

func (m model) renderMatrixSummary() string {
	var sb strings.Builder
	sb.WriteString(clackui.RailLine("◆", "  "+infoTextStyle.Render("Harness")))
	sb.WriteString("\n")
	for _, r := range buildHarnessRows(m.cb.ClaudeInstalled, m.cb.CodexInstalled, m.cb.PiInstalled) {
		marker := "○"
		if r.choice.String() == m.cb.ActiveHarness {
			marker = "◉"
		}
		style := unselectedItemStyle
		if r.installResult != SpawnClaude {
			style = hintStyle
		}
		sb.WriteString(clackui.RailLine("│", "  "+style.Render(marker+"  "+r.label)))
		sb.WriteString("\n")
	}
	sb.WriteString(clackui.RailLine("◆", "  "+infoTextStyle.Render("Providers")))
	sb.WriteString("\n")
	sb.WriteString(clackui.RailLine("│", "  "+infoTextStyle.Render("Provider: "+activeProviderLabel(m.cb))))
	sb.WriteString("\n")
	for _, r := range buildProviderRows(m.cb.KeyNames, m.cb.GrantedProviders) {
		marker := "○"
		if r.prov.String() == m.cb.ActiveProvider {
			marker = "◉"
		}
		sb.WriteString(clackui.RailLine("│", "  "+unselectedItemStyle.Render(marker+"  "+r.label)))
		sb.WriteString("\n")
	}
	return sb.String()
}

// plainBanner returns a plain-text version of the landing screen (no ANSI).
// Used as a non-TTY fallback when the bubbletea program cannot run.
func plainBanner(state AuthState) string {
	var sb strings.Builder
	sb.WriteString("\nvoid-code " + version.Version + " — relay harness for Claude Code and Pi — by makscee.ru\n\n")
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
