package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// ─── clack rail rendering tests for doctor ───────────────────────────────────

func TestDoctorRailStructure_AllGreen(t *testing.T) {
	checks := []checkResult{
		{name: "statusline", status: "✓", message: "statusline: installed"},
	}
	out := RenderDoctorChecksForTest(checks)

	// Top cap
	if !strings.Contains(out, "┌") {
		t.Errorf("doctor rail missing top cap ┌:\n%s", out)
	}
	// Rail
	if !strings.Contains(out, "│") {
		t.Errorf("doctor rail missing │:\n%s", out)
	}
	// Title
	if !strings.Contains(out, "doctor") {
		t.Errorf("doctor rail missing 'doctor' title:\n%s", out)
	}
	// Check name
	if !strings.Contains(out, "statusline") {
		t.Errorf("doctor rail missing check name 'statusline':\n%s", out)
	}
	// Ok icon
	if !strings.Contains(out, "✓") {
		t.Errorf("doctor rail missing ok icon ✓:\n%s", out)
	}
}

func TestDoctorRailStructure_Absent(t *testing.T) {
	checks := []checkResult{
		{name: "statusline", status: "✗", message: "statusline: not installed"},
	}
	out := RenderDoctorChecksForTest(checks)

	if !strings.Contains(out, "✗") {
		t.Errorf("doctor rail missing fail icon ✗:\n%s", out)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("doctor rail missing 'not installed' detail:\n%s", out)
	}
}

func TestDoctorRailStructure_Foreign(t *testing.T) {
	checks := []checkResult{
		{name: "statusline", status: "!", message: "statusline: a different statusLine is configured — leaving untouched"},
	}
	out := RenderDoctorChecksForTest(checks)

	if !strings.Contains(out, "!") {
		t.Errorf("doctor rail missing warn icon !:\n%s", out)
	}
	if !strings.Contains(out, "leaving untouched") {
		t.Errorf("doctor rail missing 'leaving untouched' detail:\n%s", out)
	}
}

func TestRenderCheckLine_OkStatus(t *testing.T) {
	c := checkResult{name: "statusline", status: "✓", message: "statusline: installed"}
	out := renderCheckLine(c)
	if !strings.Contains(out, "✓") {
		t.Errorf("renderCheckLine ok: missing ✓ in %q", out)
	}
	if !strings.Contains(out, "statusline") {
		t.Errorf("renderCheckLine ok: missing name in %q", out)
	}
	if !strings.Contains(out, "installed") {
		t.Errorf("renderCheckLine ok: missing detail 'installed' in %q", out)
	}
}

func TestRenderCheckLine_FailStatus(t *testing.T) {
	c := checkResult{name: "statusline", status: "✗", message: "statusline: not installed"}
	out := renderCheckLine(c)
	if !strings.Contains(out, "✗") {
		t.Errorf("renderCheckLine fail: missing ✗ in %q", out)
	}
	if !strings.Contains(out, "not installed") {
		t.Errorf("renderCheckLine fail: missing 'not installed' detail in %q", out)
	}
}

func TestRenderCheckLine_WarnStatus(t *testing.T) {
	c := checkResult{name: "statusline", status: "!", message: "statusline: a different statusLine is configured — leaving untouched"}
	out := renderCheckLine(c)
	if !strings.Contains(out, "!") {
		t.Errorf("renderCheckLine warn: missing ! in %q", out)
	}
	if !strings.Contains(out, "leaving untouched") {
		t.Errorf("renderCheckLine warn: missing 'leaving untouched' detail in %q", out)
	}
}

func TestConfirmModel_DefaultIsNo(t *testing.T) {
	m := newConfirmModel("Install now?", "")
	if m.cursor != 1 {
		t.Errorf("default cursor = %d, want 1 (No)", m.cursor)
	}
}

func TestConfirmModel_ViewContainsYesNo(t *testing.T) {
	m := newConfirmModel("Install the void-code statusline now?", "")
	out := m.View()
	if !strings.Contains(out, "Yes") {
		t.Errorf("confirm view missing 'Yes':\n%s", out)
	}
	if !strings.Contains(out, "No") {
		t.Errorf("confirm view missing 'No':\n%s", out)
	}
	// ◆ prompt marker
	if !strings.Contains(out, "◆") {
		t.Errorf("confirm view missing ◆ prompt marker:\n%s", out)
	}
	// selected item marker ●
	if !strings.Contains(out, "●") {
		t.Errorf("confirm view missing ● selected marker:\n%s", out)
	}
	// unselected item marker ○
	if !strings.Contains(out, "○") {
		t.Errorf("confirm view missing ○ unselected marker:\n%s", out)
	}
}

// TestConfirmModel_ViewContainsHeader verifies the regression fix: the confirm
// model's View() includes the pre-rendered header (┌  doctor + checks) so the
// bubbletea program renders the complete layout in one pass — no ^0 garble.
func TestConfirmModel_ViewContainsHeader(t *testing.T) {
	checks := []checkResult{
		{name: "statusline", status: "✗", message: "statusline: not installed"},
	}
	header := buildConfirmHeader(checks)
	m := newConfirmModel("Install void-code statusline now?", header)
	out := m.View()

	// Header must contain the ┌  doctor top cap.
	if !strings.Contains(out, "┌") {
		t.Errorf("confirm view missing ┌ top cap (header not embedded):\n%s", out)
	}
	if !strings.Contains(out, "doctor") {
		t.Errorf("confirm view missing 'doctor' title:\n%s", out)
	}
	// Check line must be present above the ◆ prompt.
	if !strings.Contains(out, "not installed") {
		t.Errorf("confirm view missing check detail 'not installed':\n%s", out)
	}
	// The ◆ question line must be present (defect 2 fix).
	if !strings.Contains(out, "◆") {
		t.Errorf("confirm view missing ◆ prompt marker:\n%s", out)
	}
	if !strings.Contains(out, "Install void-code statusline now?") {
		t.Errorf("confirm view missing question text:\n%s", out)
	}
	// Bottom cap must be └, not │.
	if !strings.Contains(out, "└") {
		t.Errorf("confirm view missing └ bottom cap:\n%s", out)
	}
}

func TestBuildChecks_IncludesProvider(t *testing.T) {
	// Redirect HOME so provider.Load reads from a clean temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	var found bool
	for _, c := range checks {
		if c.name == "provider" {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor checks should include a 'provider' line")
	}
}

// ─── claude CLI check tests ───────────────────────────────────────────────────

func TestCheckClaudeCLI_NotFound(t *testing.T) {
	// Strip PATH so claude cannot be found.
	t.Setenv("PATH", "")

	c := checkClaudeCLI()
	if c.status != "✗" {
		t.Errorf("checkClaudeCLI absent: want status=✗, got %q", c.status)
	}
	if c.name != "claude CLI" {
		t.Errorf("checkClaudeCLI: want name='claude CLI', got %q", c.name)
	}
	if !strings.Contains(c.message, "not found") {
		t.Errorf("checkClaudeCLI absent: message should mention 'not found', got: %s", c.message)
	}
	// Install instructions appear in guidance lines (not the short message line).
	guidanceText := strings.Join(c.guidance, "\n")
	if !strings.Contains(guidanceText, "npm") {
		t.Errorf("checkClaudeCLI absent: guidance should include npm install instructions, got: %s", guidanceText)
	}
}

func TestCheckClaudeCLI_Found(t *testing.T) {
	// Use the real PATH — this test machine has claude installed.
	// If not installed, skip rather than fail (CI may not have it).
	c := checkClaudeCLI()
	if c.status == "✗" {
		t.Skip("claude not installed on this machine — skipping found-path test")
	}
	if c.status != "✓" {
		t.Errorf("checkClaudeCLI found: want status=✓, got %q", c.status)
	}
	if !strings.Contains(c.message, "found at") {
		t.Errorf("checkClaudeCLI found: message should say 'found at', got: %s", c.message)
	}
}

func TestBuildChecks_IncludesClaude(t *testing.T) {
	// Strip PATH so the claude check is deterministic (absent).
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	var found bool
	for _, c := range checks {
		if c.name == "claude CLI" {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor checks should include a 'claude CLI' line")
	}
}

// ─── checkNode tests ──────────────────────────────────────────────────────────

func TestCheckNode_Absent(t *testing.T) {
	t.Setenv("PATH", "")
	c := checkNode()
	if c.status != "✗" {
		t.Errorf("checkNode absent: want status=✗, got %q", c.status)
	}
	if c.name != "node" {
		t.Errorf("checkNode absent: want name='node', got %q", c.name)
	}
	if len(c.guidance) == 0 && c.fix == nil {
		t.Error("checkNode absent: must provide guidance or fix")
	}
}

func TestCheckNode_Present(t *testing.T) {
	// Use the real PATH — if node is installed and >= 18, expect ✓.
	// If node is missing on this machine, skip.
	c := checkNode()
	if c.status == "✗" {
		t.Skip("node not installed or below min version on this machine — skipping found test")
	}
	if c.status != "✓" {
		t.Errorf("checkNode present: want status=✓, got %q", c.status)
	}
	if !strings.Contains(c.message, "v") {
		t.Errorf("checkNode present: message should include version, got: %s", c.message)
	}
}

// ─── checkNpm tests ───────────────────────────────────────────────────────────

func TestCheckNpm_Absent(t *testing.T) {
	t.Setenv("PATH", "")
	c := checkNpm()
	if c.status != "✗" {
		t.Errorf("checkNpm absent: want status=✗, got %q", c.status)
	}
	if c.name != "npm" {
		t.Errorf("checkNpm absent: want name='npm', got %q", c.name)
	}
	if len(c.guidance) == 0 && c.fix == nil {
		t.Error("checkNpm absent: must provide guidance or fix")
	}
}

func TestCheckNpm_Present(t *testing.T) {
	c := checkNpm()
	if c.status == "✗" {
		t.Skip("npm not installed on this machine — skipping found test")
	}
	if c.status != "✓" {
		t.Errorf("checkNpm present: want status=✓, got %q", c.status)
	}
}

// ─── checkBinOnPath tests ─────────────────────────────────────────────────────

func TestCheckBinOnPath_Absent(t *testing.T) {
	// Use a PATH that definitely does not include ~/.void-code/bin.
	t.Setenv("PATH", "/usr/bin:/bin")
	c := checkBinOnPath()
	if c.status != "✗" {
		t.Errorf("checkBinOnPath absent: want status=✗, got %q (PATH=%s)", c.status, "/usr/bin:/bin")
	}
	if c.name != "bin on PATH" {
		t.Errorf("checkBinOnPath absent: want name='bin on PATH', got %q", c.name)
	}
	if len(c.guidance) == 0 && c.fix == nil {
		t.Error("checkBinOnPath absent: must provide guidance or fix")
	}
}

func TestCheckBinOnPath_Present(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve home dir")
	}
	binDir := filepath.Join(home, ".void-code", "bin")
	t.Setenv("PATH", "/usr/bin:"+binDir+":/bin")
	c := checkBinOnPath()
	if c.status != "✓" {
		t.Errorf("checkBinOnPath present: want status=✓, got %q", c.status)
	}
}

// ─── buildChecks includes new checks ─────────────────────────────────────────

func TestBuildChecks_IncludesNode(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	var found bool
	for _, c := range checks {
		if c.name == "node" {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor checks should include a 'node' check")
	}
}

func TestBuildChecks_IncludesNpm(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	var found bool
	for _, c := range checks {
		if c.name == "npm" {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor checks should include an 'npm' check")
	}
}

func TestBuildChecks_IncludesBinOnPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	var found bool
	for _, c := range checks {
		if c.name == "bin on PATH" {
			found = true
		}
	}
	if !found {
		t.Fatal("doctor checks should include a 'bin on PATH' check")
	}
}
