package welcome

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makscee/void-code/internal/provider"
)

func TestBuildProviderRowsIncludesOnlySupportedGranted(t *testing.T) {
	granted := []ProviderRowInfo{
		{ID: "deepseek-sub", Name: "DeepSeek Sub"},
		{ID: "plat-2", Name: "Platform 2"},
		{ID: "chatgpt-sub", Name: "ChatGPT Sub"},
	}
	rows := buildProviderRows([]string{"mykey"}, granted)

	var deepseekRows, haveChatGPT int
	var havePlat2, haveMyKey bool
	for _, r := range rows {
		if r.prov.Kind == provider.Relay && r.label == "DeepSeek relay" {
			deepseekRows++
		}
		if r.prov.Kind == provider.RelayProvider && r.prov.ID == "chatgpt-sub" && r.label == "ChatGPT relay" {
			haveChatGPT++
		}
		if r.prov.Kind == provider.RelayProvider && r.prov.ID == "plat-2" {
			havePlat2 = true
		}
		if r.prov.Kind == provider.NamedKey && r.prov.Name == "mykey" {
			haveMyKey = true
		}
	}
	if deepseekRows != 1 || haveChatGPT != 1 {
		t.Fatalf("rows missing supported entries or duplicated DeepSeek: deepseek=%d chatgpt=%d rows=%+v", deepseekRows, haveChatGPT, rows)
	}
	if havePlat2 || haveMyKey {
		t.Fatalf("unsupported relay/named-key rows must be hidden: plat2=%v mykey=%v rows=%+v", havePlat2, haveMyKey, rows)
	}
}

func TestBuildProviderRowsUsesSupportedIDFallback(t *testing.T) {
	granted := []ProviderRowInfo{{ID: "chatgpt-sub", Name: ""}}
	rows := buildProviderRows(nil, granted)
	if rows[1].label != "ChatGPT relay" {
		t.Fatalf("label = %q, want ChatGPT relay", rows[1].label)
	}
}

func TestBuildProviderRowsUsesProviderType(t *testing.T) {
	granted := []ProviderRowInfo{
		{ID: "opaque-deep", Name: "Enterprise", Type: "deepseek"},
		{ID: "opaque-chat", Name: "Enterprise", Type: "openai-codex-oauth"},
	}
	rows := buildProviderRows(nil, granted)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2: %+v", len(rows), rows)
	}
	if rows[0].label != "DeepSeek relay" || rows[0].prov.Kind != provider.Relay {
		t.Fatalf("row0 = %+v, want baseline DeepSeek", rows[0])
	}
	if rows[1].label != "ChatGPT relay" || rows[1].prov.Kind != provider.RelayProvider || rows[1].prov.ID != "opaque-chat" {
		t.Fatalf("row1 = %+v, want opaque ChatGPT relay", rows[1])
	}
}

func TestBuildProviderRowsNoGrantedIsBaseline(t *testing.T) {
	// ungranted user: empty granted list → bare DeepSeek relay only.
	rows := buildProviderRows(nil, nil)
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].prov.Kind != provider.Relay || rows[0].label != "DeepSeek relay" {
		t.Fatalf("row0 = %+v, want bare DeepSeek relay", rows[0])
	}
}

func TestProvidersModel_RowsFromKeys(t *testing.T) {
	keys := []string{"work", "personal"}
	m := NewProvidersModelForTest(keys, "relay")
	// Expected rows (no granted): DeepSeek relay. Named keys are hidden.
	if got := m.RowCount(); got != 1 {
		t.Fatalf("RowCount = %d, want 1", got)
	}
	if m.RowLabel(0) != "DeepSeek relay" {
		t.Errorf("row0 = %q, want DeepSeek relay", m.RowLabel(0))
	}
}

func TestProvidersModel_RowsWithGranted(t *testing.T) {
	keys := []string{"work"}
	granted := []ProviderRowInfo{{ID: "deepseek-sub", Name: "DeepSeek Sub"}, {ID: "chatgpt-sub", Name: ""}}
	m := NewProvidersModelWithGrantedForTest(keys, granted, "relay")
	// Expected rows: bare DeepSeek relay, ChatGPT relay. Named keys are hidden.
	if got := m.RowCount(); got != 2 {
		t.Fatalf("RowCount = %d, want 2", got)
	}
	if m.RowLabel(0) != "DeepSeek relay" {
		t.Errorf("row0 = %q, want DeepSeek relay", m.RowLabel(0))
	}
	if m.RowLabel(1) != "ChatGPT relay" {
		t.Errorf("row1 = %q, want ChatGPT relay", m.RowLabel(1))
	}
	if !m.RowIsActive(0) {
		t.Error("DeepSeek relay row should be active")
	}
}

func TestProviderSelectionClaudeChatGPTPersistsRelayProvider(t *testing.T) {
	var selectedProvider, selectedLabel string
	granted := []ProviderRowInfo{{ID: "opaque-chat", Name: "Enterprise", Type: "openai-codex-oauth"}}
	m := newModel(AuthState{LoggedIn: true}, Callbacks{
		ActiveHarness:    "claude",
		ActiveProvider:   "relay",
		GrantedProviders: granted,
		OnSelect: func(p provider.Provider) error {
			selectedProvider = p.String()
			return nil
		},
		OnSelectLabel: func(label string) error {
			selectedLabel = label
			return nil
		},
	})
	m.providers = newProvidersModel(nil, granted, "relay")
	m.view = providersView
	m.providers.cursor = 1

	next, _ := m.updateProviders("enter")
	got := next.(model)

	if got.cb.ActiveHarness != "claude" {
		t.Fatalf("ActiveHarness = %q, want claude", got.cb.ActiveHarness)
	}
	if got.cb.ActiveProvider != "prov:opaque-chat" || selectedProvider != "prov:opaque-chat" {
		t.Fatalf("selected active provider = %q callback=%q, want prov:opaque-chat", got.cb.ActiveProvider, selectedProvider)
	}
	if got.cb.ActiveProviderLabel != "ChatGPT relay" || selectedLabel != "ChatGPT relay" {
		t.Fatalf("selected label = %q callback=%q, want ChatGPT relay", got.cb.ActiveProviderLabel, selectedLabel)
	}
}

func TestProvidersModel_SelectMarksActive(t *testing.T) {
	m := NewProvidersModelForTest([]string{"work"}, "relay")
	if !m.RowIsActive(0) {
		t.Error("DeepSeek should be the active row")
	}
}

func TestProvidersModel_RowIsDeleteable(t *testing.T) {
	m := NewProvidersModelForTest([]string{"work"}, "relay")
	if m.RowIsDeletable(0) {
		t.Error("DeepSeek row should not be deletable")
	}
}

func TestDeleteConfirmModel_YConfirms(t *testing.T) {
	dc := newDeleteConfirmModel("work")
	if dc.Confirmed() || dc.Cancelled() {
		t.Fatal("freshly created model should not be confirmed or cancelled")
	}
	dc = dc.handleKey("y")
	if !dc.Confirmed() {
		t.Fatal("pressing y should confirm")
	}
	if dc.KeyName() != "work" {
		t.Fatalf("KeyName = %q, want work", dc.KeyName())
	}
}

func TestDeleteConfirmModel_NorEscCancels(t *testing.T) {
	dc := newDeleteConfirmModel("mykey")
	dc2 := dc.handleKey("n")
	if !dc2.Cancelled() {
		t.Fatal("pressing n should cancel")
	}
	dc3 := dc.handleKey("esc")
	if !dc3.Cancelled() {
		t.Fatal("pressing esc should cancel")
	}
}

// TestAddKeyInput_PasteHandled verifies that pasting a multi-char string into
// the addKeyModel lands in the buffer. This is the repro for the bug reported
// 2026-05-31: "can't paste the key, pasting just doesn't do anything".
//
// Root cause: handleKey only accepted len(s)==1, dropping multi-rune paste text.
// Fix: add handlePaste(text string) that appends the full string unconditionally.
func TestAddKeyInput_PasteHandled(t *testing.T) {
	// repro-before-fix: paste of an oauth token must NOT be silently dropped.
	in := newAddKeyModel()
	in = in.typeForTest("mykey")
	in = in.submitForTest() // advance to token stage
	if in.Stage() != addKeyStageToken {
		t.Fatalf("expected token stage, got %v", in.Stage())
	}

	// Simulate bubbletea bracketed-paste: multi-rune string arrives via handlePaste.
	const pastedToken = "sk-ant-oat01-ABC123XYZ-LONGTOKEN"
	in = in.handlePaste(pastedToken)
	if in.buf != pastedToken {
		t.Fatalf("buf after paste = %q, want %q — paste was dropped (repro confirmed)", in.buf, pastedToken)
	}

	// And submitting captures the token.
	in = in.submitForTest()
	if !in.Done() {
		t.Fatal("should be done after submit")
	}
	if in.Token() != pastedToken {
		t.Fatalf("Token() = %q, want %q", in.Token(), pastedToken)
	}
}

func TestAddKeyInput_TwoStageCapture(t *testing.T) {
	in := newAddKeyModel()
	// Stage 1: type a name.
	in = in.typeForTest("work")
	in = in.submitForTest() // name done → token stage
	if in.Stage() != addKeyStageToken {
		t.Fatalf("after name submit, stage = %v, want token", in.Stage())
	}
	in = in.typeForTest("sk-ant-oat01-XYZ")
	in = in.submitForTest()
	if !in.Done() {
		t.Fatal("after token submit, should be Done")
	}
	if in.Name() != "work" || in.Token() != "sk-ant-oat01-XYZ" {
		t.Fatalf("captured name=%q token=%q", in.Name(), in.Token())
	}
}

// TestUpdateAddKey_PasteRouted verifies that model.Update, when in addKeyView,
// properly routes a bracketed-paste KeyMsg (Paste=true, multiple Runes) into
// the addKey buffer. This is the integration-level repro for VCD-57 paste bug.
//
// bubbletea v1.3.10: bracketed paste is ON by default. A paste arrives as
// tea.KeyMsg{Type: tea.KeyRunes, Runes: [...], Paste: true}.
// The bug: Update only handled tea.KeyMsg (ok — paste IS a KeyMsg), but
// handleKey had `len(s)==1` guard that drops multi-rune paste strings.
// Additionally, the bracketed-paste KeyMsg.String() returns "[token]" (with
// surrounding brackets added by bubbletea), which also has len > 1 and starts
// with '[', so it can never match a single printable character.
func TestUpdateAddKey_PasteRouted(t *testing.T) {
	m := NewMenuModelForTest(AuthState{LoggedIn: true})
	m = m.SetAddKeyView()

	// Advance to token stage by typing a name and pressing enter.
	for _, ch := range "mykey" {
		m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = m2.(model)
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(model)
	if m.addKey.Stage() != addKeyStageToken {
		t.Fatalf("expected token stage, got %v — name-entry broke", m.addKey.Stage())
	}

	// Now paste a multi-rune token via a bracketed-paste KeyMsg (Paste: true).
	const pastedToken = "sk-ant-oat01-PASTETEST"
	pasteMsg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(pastedToken),
		Paste: true,
	}
	m2, _ = m.Update(pasteMsg)
	m = m2.(model)

	if m.AddKeyBuf() != pastedToken {
		t.Fatalf("addKey.buf after paste = %q, want %q — paste was lost (bug reproduced)", m.AddKeyBuf(), pastedToken)
	}
}
