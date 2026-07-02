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

// ProviderRowInfo is a granted-provider entry for the menu (safe fields only).
// Mirrors auth.ProviderInfo without importing internal/auth (keeps welcome pure).
type ProviderRowInfo struct {
	ID   string
	Name string
	Type string
}

// buildProviderRows assembles the provider selector: always the bare DeepSeek
// relay default plus granted ChatGPT/OpenAI/Codex relay providers. Plain and
// saved BYO keys remain readable from storage but are not valid matrix rows.
func buildProviderRows(keyNames []string, granted []ProviderRowInfo) []providerRow {
	_ = keyNames // named keys are intentionally hidden from the main selector.
	rows := []providerRow{{label: provider.Provider{Kind: provider.Relay}.Label(), prov: provider.Provider{Kind: provider.Relay}}}
	for _, g := range granted {
		name, ok := supportedRelayProviderName(g)
		if !ok || name == "DeepSeek" {
			continue
		}
		p := provider.Provider{Kind: provider.RelayProvider, ID: g.ID}
		rows = append(rows, providerRow{label: name + " relay", prov: p})
	}
	return rows
}

func supportedRelayProviderName(g ProviderRowInfo) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(g.Type)) {
	case "deepseek":
		return "DeepSeek", true
	case "openai-codex-oauth":
		return "ChatGPT", true
	}
	for _, v := range []string{g.Name, g.ID} {
		v = strings.ToLower(strings.TrimSpace(v))
		switch {
		case strings.Contains(v, "deepseek"):
			return "DeepSeek", true
		case strings.Contains(v, "chatgpt") || strings.Contains(v, "openai") || strings.Contains(v, "codex"):
			return "ChatGPT", true
		}
	}
	return "", false
}

func newProvidersModel(keyNames []string, granted []ProviderRowInfo, activeStr string) providersModel {
	return providersModel{rows: buildProviderRows(keyNames, granted), active: activeStr}
}

// ─── white-box test accessors ────────────────────────────────────────────────

func NewProvidersModelForTest(keyNames []string, activeStr string) providersModel {
	return newProvidersModel(keyNames, nil, activeStr)
}

func NewProvidersModelWithGrantedForTest(keyNames []string, granted []ProviderRowInfo, activeStr string) providersModel {
	return newProvidersModel(keyNames, granted, activeStr)
}
func (m providersModel) RowCount() int         { return len(m.rows) }
func (m providersModel) RowLabel(i int) string { return m.rows[i].label }
func (m providersModel) RowIsActive(i int) bool {
	r := m.rows[i]
	return !r.addKey && r.prov.String() == m.active
}

// RowIsDeletable returns true only for named-key rows (not Relay, Plain, or + Add key…).
func (m providersModel) RowIsDeletable(i int) bool {
	r := m.rows[i]
	return !r.addKey && r.prov.Kind == provider.NamedKey
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
	hint := "↑/↓ · enter select · esc back"
	if m.RowIsDeletable(m.cursor) {
		hint = "↑/↓ · enter select · d delete · esc back"
	}
	out += clackui.RailLine("└", "  "+clackui.HintStyle.Render(hint)) + "\n"
	return out
}

// ─── Add-key text input model ────────────────────────────────────────────────

type addKeyStage int

const (
	addKeyStageName addKeyStage = iota
	addKeyStageToken
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
func (m addKeyModel) typeForTest(s string) addKeyModel { m.buf += s; return m }
func (m addKeyModel) submitForTest() addKeyModel       { return m.submit() }

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

// handlePaste appends a full pasted string to the buffer unconditionally.
// Call this when the originating tea.KeyMsg has Paste==true, to avoid the
// len==1 guard in handleKey from silently dropping multi-rune clipboard content.
func (m addKeyModel) handlePaste(text string) addKeyModel {
	m.buf += text
	return m
}

// handleKey processes a bubbletea key string against the buffer (real TUI path).
// For normal (non-paste) key events. Uses rune-count (not byte-length) to accept
// multi-rune typed chars from IME inputs.
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
		if len([]rune(s)) == 1 { // single printable rune (rune-aware, handles multi-byte UTF-8)
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

// ─── Delete-key confirm model ────────────────────────────────────────────────

type deleteConfirmState int

const (
	deleteConfirmPending   deleteConfirmState = iota
	deleteConfirmConfirmed                    // user pressed y
	deleteConfirmCancelled                    // user pressed n / esc
)

// deleteConfirmModel is the one-shot confirm dialog for key deletion.
type deleteConfirmModel struct {
	keyName string
	state   deleteConfirmState
}

func newDeleteConfirmModel(keyName string) deleteConfirmModel {
	return deleteConfirmModel{keyName: keyName, state: deleteConfirmPending}
}

func (m deleteConfirmModel) KeyName() string { return m.keyName }
func (m deleteConfirmModel) Confirmed() bool { return m.state == deleteConfirmConfirmed }
func (m deleteConfirmModel) Cancelled() bool { return m.state == deleteConfirmCancelled }

// handleKey processes the y/n/esc input.
func (m deleteConfirmModel) handleKey(s string) deleteConfirmModel {
	switch s {
	case "y", "Y":
		m.state = deleteConfirmConfirmed
	case "n", "N", "esc", "q":
		m.state = deleteConfirmCancelled
	}
	return m
}

// render shows the confirm prompt on the clack rail.
func (m deleteConfirmModel) render() string {
	var out string
	out += clackui.RailLine("◆", "  "+clackui.WarnStyle.Render("Delete key: "+m.keyName+"?")) + "\n"
	out += clackui.RailLine("│", "  "+clackui.HintStyle.Render("This cannot be undone.")) + "\n"
	out += clackui.RailLine("└", "  "+clackui.HintStyle.Render("y confirm · n/esc cancel")) + "\n"
	return out
}
