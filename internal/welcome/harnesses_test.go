package welcome

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestHarnessRowsShowPiWhenMissing(t *testing.T) {
	m := NewHarnessesModelForTest("claude", false)
	if got := m.RowCount(); got != 3 {
		t.Fatalf("RowCount = %d, want 3", got)
	}
	if got := m.RowLabel(2); got != "Pi (not installed)" {
		t.Fatalf("Pi row = %q, want missing label", got)
	}
	if !m.RowNeedsInstall(2) {
		t.Fatal("missing Pi row should return install action")
	}
}

func TestSelectingMissingPiReturnsInstallAction(t *testing.T) {
	m := newModel(AuthState{LoggedIn: true}, Callbacks{ActiveHarness: "claude", ClaudeInstalled: true, CodexInstalled: true, PiInstalled: false})
	m = m.SetCursor(1) // Harness row
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(model)
	if m.view != harnessesView {
		t.Fatalf("view = %v, want harnessesView", m.view)
	}
	m.harnesses.cursor = 2 // Pi (not installed)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(model)
	if !m.chosen || m.result != RunInstallPi {
		t.Fatalf("select missing Pi chosen=%v result=%v, want RunInstallPi", m.chosen, m.result)
	}
}
