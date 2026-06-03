package welcome

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/makscee/void-code/internal/provider"
)

func TestBuildProviderRowsIncludesGranted(t *testing.T) {
	granted := []ProviderRowInfo{
		{ID: "deepseek", Name: "DeepSeek"},
		{ID: "plat-2", Name: "Platform 2"},
	}
	rows := buildProviderRows([]string{"mykey"}, granted)

	var haveDeepseek, havePlat2, haveMyKey bool
	var haveBareRelay bool
	for _, r := range rows {
		// Each granted provider must appear as "Relay: <Name>", NOT bare name.
		if r.prov.Kind == provider.RelayProvider && r.prov.ID == "deepseek" && r.label == "Relay: DeepSeek" {
			haveDeepseek = true
		}
		if r.prov.Kind == provider.RelayProvider && r.prov.ID == "plat-2" && r.label == "Relay: Platform 2" {
			havePlat2 = true
		}
		// Bare "Relay" row (non-addKey) must NOT be present when granted providers are listed.
		if !r.addKey && r.prov.Kind == provider.Relay {
			haveBareRelay = true
		}
		if r.prov.Kind == provider.NamedKey && r.prov.Name == "mykey" {
			haveMyKey = true
		}
	}
	if !haveDeepseek || !havePlat2 || !haveMyKey {
		t.Fatalf("rows missing expected entries: deepseek=%v plat2=%v mykey=%v\nrows=%+v",
			haveDeepseek, havePlat2, haveMyKey, rows)
	}
	if haveBareRelay {
		t.Fatalf("bare Relay row must not appear when granted providers are listed; rows=%+v", rows)
	}
}

// TestBuildProviderRowsRelayPrefixFallback tests that when a provider has an empty Name
// the ID is used with the "Relay: " prefix.
func TestBuildProviderRowsRelayPrefixFallback(t *testing.T) {
	granted := []ProviderRowInfo{{ID: "plat-3", Name: ""}}
	rows := buildProviderRows(nil, granted)
	var found bool
	for _, r := range rows {
		if r.prov.Kind == provider.RelayProvider && r.prov.ID == "plat-3" {
			if r.label != "Relay: plat-3" {
				t.Fatalf("label = %q, want %q", r.label, "Relay: plat-3")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("plat-3 row not found")
	}
}

func TestBuildProviderRowsNoGrantedIsBaseline(t *testing.T) {
	// ungranted user: empty granted list → no RelayProvider rows, only Relay/Plain/Add (+ any keys).
	rows := buildProviderRows(nil, nil)
	for _, r := range rows {
		if r.prov.Kind == provider.RelayProvider {
			t.Fatalf("unexpected RelayProvider row for ungranted user: %+v", r)
		}
	}
}

func TestProvidersModel_RowsFromKeys(t *testing.T) {
	keys := []string{"work", "personal"}
	m := NewProvidersModelForTest(keys, "relay")
	// Expected rows (no granted): key:work, key:personal, Plain Claude Code, + Add key…
	// Bare "Relay" is gone — relay routes are only shown when granted providers exist.
	if got := m.RowCount(); got != 4 {
		t.Fatalf("RowCount = %d, want 4 (no bare Relay row)", got)
	}
	if m.RowLabel(2) != "Plain Claude Code" {
		t.Errorf("row2 = %q, want Plain Claude Code", m.RowLabel(2))
	}
	if m.RowLabel(3) != "+ Add key…" {
		t.Errorf("row3 = %q, want + Add key…", m.RowLabel(3))
	}
}

func TestProvidersModel_RowsWithGranted(t *testing.T) {
	keys := []string{"work"}
	granted := []ProviderRowInfo{{ID: "deepseek", Name: "DeepSeek"}}
	m := NewProvidersModelWithGrantedForTest(keys, granted, "prov:deepseek")
	// Expected rows: Relay: DeepSeek, key:work, Plain Claude Code, + Add key…
	if got := m.RowCount(); got != 4 {
		t.Fatalf("RowCount = %d, want 4", got)
	}
	if m.RowLabel(0) != "Relay: DeepSeek" {
		t.Errorf("row0 = %q, want Relay: DeepSeek", m.RowLabel(0))
	}
	if !m.RowIsActive(0) {
		t.Error("Relay: DeepSeek row should be active")
	}
}

func TestProvidersModel_SelectMarksActive(t *testing.T) {
	m := NewProvidersModelForTest([]string{"work"}, "key:work")
	// No bare Relay row; rows: key:work(0), Plain(1), +Add(2).
	// The active row (key:work) is at index 0.
	if !m.RowIsActive(0) {
		t.Error("key:work should be the active row (index 0)")
	}
	if m.RowIsActive(1) {
		t.Error("Plain should not be active when key:work is active")
	}
}

func TestProvidersModel_RowIsDeleteable(t *testing.T) {
	// key:work (index 0), Plain (index 1), and + Add key… (index 2) — no bare Relay row.
	// A named key (index 0) MUST be deleteable; Plain and +Add are not.
	m := NewProvidersModelForTest([]string{"work"}, "relay")
	// Rows: key:work(0), Plain(1), +Add key…(2)
	if !m.RowIsDeletable(0) {
		t.Error("named key row should be deletable")
	}
	if m.RowIsDeletable(1) {
		t.Error("Plain row should not be deletable")
	}
	if m.RowIsDeletable(2) {
		t.Error("+ Add key… row should not be deletable")
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
