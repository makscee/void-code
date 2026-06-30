package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harnesschoice"
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

func TestBuildChecks_IncludesProvider(t *testing.T) {
	// Redirect HOME so provider.Load reads from a clean temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	if !hasCheck(checks, "provider") {
		t.Fatal("doctor checks should include a 'provider' line")
	}
}

func hasCheck(checks []checkResult, name string) bool {
	for _, c := range checks {
		if c.name == name {
			return true
		}
	}
	return false
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
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Claude}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	if !hasCheck(checks, "claude CLI") {
		t.Fatal("doctor checks should include a 'claude CLI' line")
	}
}

func TestBuildChecks_DefaultPiUsesPiChecks(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	for _, name := range []string{"harness", "matrix", "pi CLI", "provider"} {
		if !hasCheck(checks, name) {
			t.Fatalf("default Pi doctor checks should include %q", name)
		}
	}
	for _, name := range []string{"node", "npm", "claude CLI", "statusline", "codex CLI"} {
		if hasCheck(checks, name) {
			t.Fatalf("default Pi doctor checks should not include %q", name)
		}
	}
}

func TestBuildChecks_PiActiveUsesPiChecks(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Pi}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	for _, name := range []string{"harness", "matrix", "pi CLI", "provider"} {
		if !hasCheck(checks, name) {
			t.Fatalf("Pi doctor checks should include %q", name)
		}
	}
	for _, name := range []string{"node", "npm", "claude CLI", "statusline"} {
		if hasCheck(checks, name) {
			t.Fatalf("Pi doctor checks should not include Claude-specific %q", name)
		}
	}
}

func TestBuildChecks_CodexActiveUsesCodexChecks(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Codex}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

	checks := buildChecks("/nonexistent/settings.json", "/abs/vc statusline")
	for _, name := range []string{"harness", "matrix", "node", "npm", "codex CLI", "provider"} {
		if !hasCheck(checks, name) {
			t.Fatalf("Codex doctor checks should include %q", name)
		}
	}
	for _, name := range []string{"claude CLI", "pi CLI", "statusline"} {
		if hasCheck(checks, name) {
			t.Fatalf("Codex doctor checks should not include %q", name)
		}
	}
}

func TestCheckCodexCLI_NotFound(t *testing.T) {
	t.Setenv("PATH", "")

	c := checkCodexCLI()
	if c.status != "✗" {
		t.Errorf("checkCodexCLI absent: want status=✗, got %q", c.status)
	}
	if c.name != "codex CLI" {
		t.Errorf("checkCodexCLI: want name='codex CLI', got %q", c.name)
	}
	if !strings.Contains(c.message, "not found") {
		t.Errorf("checkCodexCLI absent: message should mention 'not found', got: %s", c.message)
	}
	guidanceText := strings.Join(c.guidance, "\n")
	if !strings.Contains(guidanceText, "@openai/codex") {
		t.Errorf("checkCodexCLI absent: guidance should include Codex install instructions, got: %s", guidanceText)
	}
}

// ─── pi CLI check tests ───────────────────────────────────────────────────────

func TestCheckPiCLI_NotFound(t *testing.T) {
	t.Setenv("PATH", "")

	c := checkPiCLI()
	if c.status != "✗" {
		t.Errorf("checkPiCLI absent: want status=✗, got %q", c.status)
	}
	if c.name != "pi CLI" {
		t.Errorf("checkPiCLI: want name='pi CLI', got %q", c.name)
	}
	if !strings.Contains(c.message, "not found") {
		t.Errorf("checkPiCLI absent: message should mention 'not found', got: %s", c.message)
	}
	guidanceText := strings.Join(c.guidance, "\n")
	if !strings.Contains(guidanceText, "pi-coding-agent") {
		t.Errorf("checkPiCLI absent: guidance should include Pi install instructions, got: %s", guidanceText)
	}
}

func TestCheckPiCLI_Found(t *testing.T) {
	c := checkPiCLI()
	if c.status == "✗" {
		t.Skip("pi not installed on this machine — skipping found-path test")
	}
	if c.status != "✓" {
		t.Errorf("checkPiCLI found: want status=✓, got %q", c.status)
	}
	if !strings.Contains(c.message, "found at") {
		t.Errorf("checkPiCLI found: message should say 'found at', got: %s", c.message)
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
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Claude}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

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
	if err := harnesschoice.Save(harnesschoice.Choice{Kind: harnesschoice.Claude}); err != nil {
		t.Fatalf("save active harness: %v", err)
	}

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

// ─── VCD-62: selectModel + checkStatusLine foreign/skip tests ────────────────

func TestSelectModel_ViewContainsOptions(t *testing.T) {
	opts := []string{"merge (keep existing + add vc)", "override (replace with vc)", "skip"}
	m := newSelectModel("How should vc handle your existing statusLine?", "", opts)
	out := m.View()

	// ◆ prompt marker
	if !strings.Contains(out, "◆") {
		t.Errorf("select view missing ◆ prompt marker:\n%s", out)
	}
	for _, opt := range opts {
		if !strings.Contains(out, opt) {
			t.Errorf("select view missing option %q:\n%s", opt, out)
		}
	}
	// Selected item marker ● (cursor=0 by default)
	if !strings.Contains(out, "●") {
		t.Errorf("select view missing ● selected marker:\n%s", out)
	}
	// Unselected item marker ○
	if !strings.Contains(out, "○") {
		t.Errorf("select view missing ○ unselected marker:\n%s", out)
	}
}

func TestSelectModel_ViewContainsHeader(t *testing.T) {
	checks := []checkResult{
		{name: "statusline", status: "!", message: "statusline: a different statusLine is configured"},
	}
	header := buildConfirmHeader(checks)
	opts := []string{"merge (keep existing + add vc)", "override (replace with vc)", "skip"}
	m := newSelectModel("How should vc handle your existing statusLine?", header, opts)
	out := m.View()

	// Header must embed ┌  doctor top cap
	if !strings.Contains(out, "┌") {
		t.Errorf("select view missing ┌ top cap (header not embedded):\n%s", out)
	}
	if !strings.Contains(out, "doctor") {
		t.Errorf("select view missing 'doctor' title:\n%s", out)
	}
	if !strings.Contains(out, "different statusLine") {
		t.Errorf("select view missing check detail in header:\n%s", out)
	}
}

func TestCheckStatusLine_Foreign_HasSelectOptions(t *testing.T) {
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo CUSTOM",
		},
	})

	c := checkStatusLine(p, "/abs/vc statusline")
	if c.status != "!" {
		t.Errorf("foreign: want status=!, got %q", c.status)
	}
	if len(c.selectOptions) != 3 {
		t.Fatalf("foreign: want 3 selectOptions, got %d: %v", len(c.selectOptions), c.selectOptions)
	}
	if c.fixSelect == nil {
		t.Fatal("foreign: want fixSelect != nil")
	}
	// Verify option labels contain the expected keywords.
	if !strings.Contains(c.selectOptions[0], "merge") {
		t.Errorf("option 0 should be merge, got %q", c.selectOptions[0])
	}
	if !strings.Contains(c.selectOptions[1], "override") {
		t.Errorf("option 1 should be override, got %q", c.selectOptions[1])
	}
	if !strings.Contains(c.selectOptions[2], "skip") {
		t.Errorf("option 2 should be skip, got %q", c.selectOptions[2])
	}
}

func TestCheckStatusLine_Foreign_Skipped_Informational(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo CUSTOM",
		},
	})

	// Mark skipped.
	if err := config.MarkStatusLineSkipped(); err != nil {
		t.Fatalf("MarkStatusLineSkipped: %v", err)
	}

	c := checkStatusLine(p, "/abs/vc statusline")
	// Status should be "!" (informational), NOT "✗".
	if c.status != "!" {
		t.Errorf("skipped foreign: want status=!, got %q", c.status)
	}
	if !strings.Contains(c.message, "skipped") {
		t.Errorf("skipped foreign: message should contain 'skipped', got %q", c.message)
	}
	// fixSelect must still be present (re-offer requirement).
	if c.fixSelect == nil {
		t.Fatal("skipped foreign: fixSelect must still be non-nil (re-offer)")
	}
}

func TestCheckStatusLine_Installed_Ours(t *testing.T) {
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/abs/vc statusline",
		},
	})

	c := checkStatusLine(p, "/abs/vc statusline")
	if c.status != "✓" {
		t.Errorf("installed: want status=✓, got %q", c.status)
	}
	if c.fix != nil || c.fixSelect != nil {
		t.Error("installed: no fix needed when already ours")
	}
}

func TestCheckStatusLine_InstalledMergeWrapper_Ours(t *testing.T) {
	// Merge wrapper form ("vc statusline --merge") must also be recognised as installed.
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "/abs/vc statusline --merge",
		},
	})

	c := checkStatusLine(p, "/abs/vc statusline")
	if c.status != "✓" {
		t.Errorf("merge wrapper: want status=✓, got %q", c.status)
	}
}

func TestCheckStatusLine_Absent_HasFix(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")

	c := checkStatusLine(p, "/abs/vc statusline")
	if c.status != "✗" {
		t.Errorf("absent: want status=✗, got %q", c.status)
	}
	if c.fix == nil {
		t.Fatal("absent: want fix != nil for absent statusLine")
	}
	if c.fixSelect != nil {
		t.Error("absent: fixSelect must be nil (uses Yes/No, not 3-way)")
	}
}

func TestCheckStatusLine_Merge_PostFixIsInstalled(t *testing.T) {
	// Simulate choosing merge: backup prior, SetStatusLine with merge cmd → classify installed.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{
			"type":    "command",
			"command": "echo CUSTOM",
		},
	})

	c := checkStatusLine(p, "/abs/vc statusline")
	if c.fixSelect == nil {
		t.Fatal("foreign: fixSelect must be non-nil")
	}
	// Apply merge choice (0).
	if err := c.fixSelect(0); err != nil {
		t.Fatalf("fixSelect merge: %v", err)
	}
	// Re-classify — should be installed (merge wrapper is ours).
	after := classifyStatusLine(p, "/abs/vc statusline")
	if after != slInstalled {
		t.Errorf("after merge: classify = %v, want slInstalled", after)
	}
}
