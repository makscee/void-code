package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/ccsettings"
)

// TestFixGuidance_AbsentStatusLine verifies that a check carrying a plain `fix`
// (absent statusline) yields a `vc doctor --fix` guidance line in report-only
// mode — the report tells the user how to apply the fix instead of prompting.
func TestFixGuidance_AbsentStatusLine(t *testing.T) {
	dir := t.TempDir()
	c := checkStatusLine(filepath.Join(dir, "settings.json"), "/abs/vc statusline")
	if c.fix == nil {
		t.Fatal("absent statusline: expected fix != nil")
	}
	g := fixGuidance(c)
	if len(g) == 0 {
		t.Fatal("absent statusline: expected fix guidance, got none")
	}
	joined := strings.Join(g, "\n")
	if !strings.Contains(joined, "vc doctor --fix") {
		t.Errorf("guidance should point at `vc doctor --fix`, got: %q", joined)
	}
	if !strings.Contains(joined, "install") {
		t.Errorf("absent-statusline guidance should mention install, got: %q", joined)
	}
}

// TestFixGuidance_ForeignStatusLine verifies the foreign (fixSelect) case yields
// a merge-oriented `vc doctor --fix` guidance line.
func TestFixGuidance_ForeignStatusLine(t *testing.T) {
	dir := t.TempDir()
	p := writeSettings(t, dir, map[string]any{
		"statusLine": map[string]any{"type": "command", "command": "echo CUSTOM"},
	})
	c := checkStatusLine(p, "/abs/vc statusline")
	if c.fixSelect == nil {
		t.Fatal("foreign statusline: expected fixSelect != nil")
	}
	g := fixGuidance(c)
	joined := strings.Join(g, "\n")
	if !strings.Contains(joined, "vc doctor --fix") {
		t.Errorf("foreign guidance should point at `vc doctor --fix`, got: %q", joined)
	}
	if !strings.Contains(joined, "merge") {
		t.Errorf("foreign guidance should mention merge, got: %q", joined)
	}
}

// TestFixGuidance_NoFix verifies a passing check (no fix) produces no guidance —
// nothing to apply, nothing to print.
func TestFixGuidance_NoFix(t *testing.T) {
	c := checkResult{name: "node", status: "✓", message: "node: v20"}
	if g := fixGuidance(c); len(g) != 0 {
		t.Errorf("no-fix check should yield no guidance, got: %v", g)
	}
}

// TestRunDoctor_ReportOnly_NeverErrors verifies the default doctor run completes
// (exit 0 / nil error) without opening a prompt. Because no bubbletea program is
// constructed, this returns promptly even though the test stdin is not a TTY —
// the report-only path can never block. HOME is redirected so the run is
// hermetic and never touches the developer's real settings.
func TestRunDoctor_ReportOnly_NeverErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := runDoctor(); err != nil {
		t.Fatalf("runDoctor() report-only returned error: %v", err)
	}
}

// TestRunDoctorFix_InstallsAbsentStatusLine verifies `vc doctor --fix` applies
// the absent-statusline install without any prompt: after runDoctorFix the
// settings file classifies as slInstalled. HOME is redirected so SettingsPath
// resolves into the temp dir.
func TestRunDoctorFix_InstallsAbsentStatusLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	settingsPath, err := ccsettings.SettingsPath()
	if err != nil {
		t.Fatalf("SettingsPath: %v", err)
	}
	// Precondition: absent.
	if got := classifyStatusLine(settingsPath, "ignored"); got != slAbsent {
		t.Fatalf("precondition: want slAbsent, got %v", got)
	}

	if err := runDoctorFix(); err != nil {
		t.Fatalf("runDoctorFix() returned error: %v", err)
	}

	// Postcondition: statusline installed (no prompt was opened).
	if got := classifyStatusLine(settingsPath, "ignored"); got != slInstalled {
		t.Errorf("after --fix: classify = %v, want slInstalled", got)
	}
}
