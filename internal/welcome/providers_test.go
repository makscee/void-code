package welcome

import "testing"

func TestProvidersModel_RowsFromKeys(t *testing.T) {
	keys := []string{"work", "personal"}
	m := NewProvidersModelForTest(keys, "relay")
	// Expected rows: Relay, key:work, key:personal, Plain Claude Code, + Add key…
	if got := m.RowCount(); got != 5 {
		t.Fatalf("RowCount = %d, want 5", got)
	}
	if m.RowLabel(0) != "Relay (void-relay)" {
		t.Errorf("row0 = %q", m.RowLabel(0))
	}
	if m.RowLabel(3) != "Plain Claude Code" {
		t.Errorf("row3 = %q", m.RowLabel(3))
	}
	if m.RowLabel(4) != "+ Add key…" {
		t.Errorf("row4 = %q", m.RowLabel(4))
	}
}

func TestProvidersModel_SelectMarksActive(t *testing.T) {
	m := NewProvidersModelForTest([]string{"work"}, "key:work")
	// The active row (key:work at index 1) should be flagged active.
	if !m.RowIsActive(1) {
		t.Error("key:work should be the active row")
	}
	if m.RowIsActive(0) {
		t.Error("relay should not be active when key:work is active")
	}
}

func TestProvidersModel_RowIsDeleteable(t *testing.T) {
	// Relay (index 0), Plain (index 2), and + Add key… (index 3) must NOT be deleteable.
	// A named key (index 1) MUST be deleteable.
	m := NewProvidersModelForTest([]string{"work"}, "relay")
	// Rows: Relay(0), key:work(1), Plain(2), +Add key…(3)
	if m.RowIsDeletable(0) {
		t.Error("Relay row should not be deletable")
	}
	if !m.RowIsDeletable(1) {
		t.Error("named key row should be deletable")
	}
	if m.RowIsDeletable(2) {
		t.Error("Plain row should not be deletable")
	}
	if m.RowIsDeletable(3) {
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
