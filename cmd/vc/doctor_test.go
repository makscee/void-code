package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── classifyStatusLine tests ─────────────────────────────────────────────────

func writeSettings(t *testing.T, dir string, v any) string {
	t.Helper()
	p := filepath.Join(dir, "settings.json")
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestClassifyStatusLine_Absent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	// No file at all.
	got := classifyStatusLine(p, "/usr/local/bin/vc statusline")
	if got != slAbsent {
		t.Errorf("missing file: got %v, want slAbsent", got)
	}

	// File exists but no statusLine key.
	writeSettings(t, dir, map[string]any{"theme": "light"})
	got = classifyStatusLine(p, "/usr/local/bin/vc statusline")
	if got != slAbsent {
		t.Errorf("no statusLine key: got %v, want slAbsent", got)
	}
}

func TestClassifyStatusLine_Installed(t *testing.T) {
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/usr/local/bin/vc statusline",
		},
	})
	got := classifyStatusLine(p, "/usr/local/bin/vc statusline")
	if got != slInstalled {
		t.Errorf("installed: got %v, want slInstalled", got)
	}
}

func TestClassifyStatusLine_InstalledDifferentPath(t *testing.T) {
	// Same binary moved — still ours because command ends with " statusline".
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/new/path/vc statusline",
		},
	})
	// Current execPath differs from what's written but it's still "ours" (ends with " statusline").
	got := classifyStatusLine(p, "/usr/local/bin/vc statusline")
	if got != slInstalled {
		t.Errorf("different vc path but still ours: got %v, want slInstalled", got)
	}
}

func TestClassifyStatusLine_Foreign(t *testing.T) {
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "~/.claude/my-statusline.sh",
		},
	})
	got := classifyStatusLine(p, "/usr/local/bin/vc statusline")
	if got != slForeign {
		t.Errorf("foreign: got %v, want slForeign", got)
	}
}

// ─── budgetLeft tests ─────────────────────────────────────────────────────────

func TestBudgetLeft_NilIsHidden(t *testing.T) {
	_, show := budgetLeft(nil)
	if show {
		t.Error("nil pct must not show budget line")
	}
}

func TestBudgetLeft_Zero(t *testing.T) {
	pct := 0.0
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=0 should show budget line")
	}
	if n != 100 {
		t.Errorf("pct=0: want 100%% left, got %d", n)
	}
}

func TestBudgetLeft_Half(t *testing.T) {
	pct := 50.0
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=50 should show budget line")
	}
	if n != 50 {
		t.Errorf("pct=50: want 50%% left, got %d", n)
	}
}

func TestBudgetLeft_Rounding(t *testing.T) {
	pct := 73.6
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=73.6 should show budget line")
	}
	// round(100 - 73.6) = round(26.4) = 26
	if n != 26 {
		t.Errorf("pct=73.6: want 26%% left, got %d", n)
	}
}

func TestBudgetLeft_RoundingUp(t *testing.T) {
	pct := 73.4
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=73.4 should show budget line")
	}
	// round(100 - 73.4) = round(26.6) = 27
	if n != 27 {
		t.Errorf("pct=73.4: want 27%% left, got %d", n)
	}
}

func TestBudgetLeft_FullyUsed(t *testing.T) {
	pct := 100.0
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=100 should show budget line (0%% left)")
	}
	if n != 0 {
		t.Errorf("pct=100: want 0%% left, got %d", n)
	}
}

func TestBudgetLeft_Over100(t *testing.T) {
	pct := 110.0
	n, show := budgetLeft(&pct)
	if !show {
		t.Error("pct=110 should show budget line")
	}
	if n != 0 {
		t.Errorf("pct=110: want 0%% left (clamp), got %d", n)
	}
}
