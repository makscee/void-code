package welcome

import (
	"strings"

	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/provider"
)

// providerRow is one selectable row in the Providers sub-view.
type providerRow struct {
	label  string
	prov   provider.Provider // the provider this row activates (zero for the addKey action)
	addKey bool              // true for the "+ Add key…" action row
}

// providersModel is the Providers radio sub-view state.
type providersModel struct {
	rows   []providerRow
	cursor int
	active string // persisted-string form of the currently active provider
}

// buildProviderRows assembles: Relay, one row per key name, Plain, + Add key…
func buildProviderRows(keyNames []string) []providerRow {
	rows := []providerRow{
		{label: provider.Provider{Kind: provider.Relay}.Label(), prov: provider.Provider{Kind: provider.Relay}},
	}
	for _, n := range keyNames {
		p := provider.Provider{Kind: provider.NamedKey, Name: n}
		rows = append(rows, providerRow{label: p.Label(), prov: p})
	}
	rows = append(rows,
		providerRow{label: provider.Provider{Kind: provider.Plain}.Label(), prov: provider.Provider{Kind: provider.Plain}},
		providerRow{label: "+ Add key…", addKey: true},
	)
	return rows
}

func newProvidersModel(keyNames []string, activeStr string) providersModel {
	return providersModel{rows: buildProviderRows(keyNames), active: activeStr}
}

// ─── white-box test accessors ────────────────────────────────────────────────

func NewProvidersModelForTest(keyNames []string, activeStr string) providersModel {
	return newProvidersModel(keyNames, activeStr)
}
func (m providersModel) RowCount() int         { return len(m.rows) }
func (m providersModel) RowLabel(i int) string { return m.rows[i].label }
func (m providersModel) RowIsActive(i int) bool {
	r := m.rows[i]
	return !r.addKey && r.prov.String() == m.active
}

// renderProviders renders the Providers radio list on the clack rail.
func (m providersModel) render() string {
	var out string
	out += clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render("Providers")) + "\n"
	for i, r := range m.rows {
		marker := "○"
		if m.RowIsActive(i) {
			marker = "◉" // active provider
		}
		style := clackui.UnselectedItemStyle
		if i == m.cursor {
			style = clackui.SelectedItemStyle
			if !m.RowIsActive(i) {
				marker = "●"
			}
		}
		out += clackui.RailLine("│", "  "+style.Render(marker+"  "+r.label)) + "\n"
	}
	out += clackui.RailLine("└", "  "+clackui.HintStyle.Render("↑/↓ · enter select · esc back")) + "\n"
	return out
}

// ─── Add-key text input model ────────────────────────────────────────────────

type addKeyStage int

const (
	addKeyStageName  addKeyStage = iota
	addKeyStageToken             // nolint:deadcode
	addKeyStageDone
)

// addKeyModel captures a key name then its token across two input stages.
type addKeyModel struct {
	stage addKeyStage
	name  string
	token string
	buf   string // current line buffer
}

func newAddKeyModel() addKeyModel { return addKeyModel{stage: addKeyStageName} }

func (m addKeyModel) Stage() addKeyStage { return m.stage }
func (m addKeyModel) Done() bool         { return m.stage == addKeyStageDone }
func (m addKeyModel) Name() string       { return m.name }
func (m addKeyModel) Token() string      { return m.token }

// typeForTest / submitForTest drive the model in tests without a TTY.
func (m addKeyModel) typeForTest(s string) addKeyModel  { m.buf += s; return m }
func (m addKeyModel) submitForTest() addKeyModel        { return m.submit() }

// submit commits the current buffer to the active stage and advances.
func (m addKeyModel) submit() addKeyModel {
	v := strings.TrimSpace(m.buf)
	switch m.stage {
	case addKeyStageName:
		m.name = v
		m.buf = ""
		m.stage = addKeyStageToken
	case addKeyStageToken:
		m.token = v
		m.buf = ""
		m.stage = addKeyStageDone
	}
	return m
}

// handleKey processes a bubbletea key string against the buffer (real TUI path).
func (m addKeyModel) handleKey(s string) addKeyModel {
	switch s {
	case "enter":
		return m.submit()
	case "backspace":
		if len(m.buf) > 0 {
			// Safe rune-aware backspace.
			runes := []rune(m.buf)
			m.buf = string(runes[:len(runes)-1])
		}
	default:
		if len(s) == 1 { // printable rune
			m.buf += s
		}
	}
	return m
}

// render shows the current stage prompt + masked/echoed buffer on the clack rail.
func (m addKeyModel) render() string {
	var out string
	switch m.stage {
	case addKeyStageName:
		out += clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render("Key name")) + "\n"
		out += clackui.RailLine("│", "  "+clackui.SelectedItemStyle.Render(m.buf+"▌")) + "\n"
	case addKeyStageToken:
		out += clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render("Paste OAuth token (sk-ant-oat01-…)")) + "\n"
		out += clackui.RailLine("│", "  "+clackui.SelectedItemStyle.Render(strings.Repeat("•", len(m.buf))+"▌")) + "\n"
	}
	out += clackui.RailLine("└", "  "+clackui.HintStyle.Render("enter · esc cancel")) + "\n"
	return out
}
