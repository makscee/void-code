package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Context segment: brainrot emoji meter (mirrors cv-statusline.sh thresholds)

func TestContextFace_Thresholds(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "🤓"},
		{59999, "🤓"},
		{60000, "😐"},
		{119999, "😐"},
		{120000, "😵‍💫"}, // 😵‍💫 zwj sequence
		{149999, "😵‍💫"},
		{150000, "🥴"},
		{179999, "🥴"},
		{180000, "💀"},
		{999999, "💀"},
	}
	for _, c := range cases {
		if got := contextFace(c.tokens); got != c.want {
			t.Errorf("contextFace(%d) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestContextTokensFmt(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1k"},
		{5000, "5k"},
		{143210, "143k"},
		{1200000, "1.2M"},
		{1000000, "1M"},
		{2000000, "2M"},
	}
	for _, c := range cases {
		if got := contextTokensFmt(c.tokens); got != c.want {
			t.Errorf("contextTokensFmt(%d) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

func TestStatusLineContextSegment_ShowsFaceAndTokens(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 5000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, segData{balanceKnown: false})
	// Expect brainrot face + compact tokens, not "ctx N%"
	if !strings.Contains(got, "🤓") {
		t.Fatalf("want emoji face 🤓, got %q", got)
	}
	if !strings.Contains(got, "5k") {
		t.Fatalf("want '5k', got %q", got)
	}
}

func TestStatusLineContextSegment_EscalatesAtThreshold(t *testing.T) {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 143000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, segData{balanceKnown: false})
	if !strings.Contains(got, "143k") {
		t.Fatalf("want '143k', got %q", got)
	}
	// 143k is in 120-150k band → 😵‍💫
	if !strings.Contains(got, "😵") {
		t.Fatalf("want dizzy face emoji for 143k, got %q", got)
	}
}

func TestStatusLineContextSegment_AbsentRendersDash(t *testing.T) {
	in := statusInput{} // no context_window at all (fresh session, no API response)
	got := renderSegments(in, segData{balanceKnown: false})
	if !strings.Contains(got, "—") {
		t.Fatalf("want '—' when context absent, got %q", got)
	}
}

// VCD-56: $ balance segment tests (Option A — plain $X.XX, no sub-days).

func ptrF(v float64) *float64 { return &v }

func TestStatusLineBalance_Known(t *testing.T) {
	d := segData{balanceUsd: ptrF(12.4), balanceKnown: true}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if !strings.Contains(got, "$12.40") {
		t.Errorf("renderSegments = %q, want it to contain $12.40", got)
	}
}

func TestStatusLineBalance_Unknown(t *testing.T) {
	d := segData{balanceKnown: false}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if strings.Contains(got, "$") {
		t.Errorf("renderSegments = %q, must hide $ when balance unknown", got)
	}
}

func TestStatusLineNoSubDays(t *testing.T) {
	d := segData{balanceUsd: ptrF(5), balanceKnown: true}
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 1000
	in.ContextWindow.ContextWindowSize = 200000
	got := renderSegments(in, d)
	if strings.Contains(got, "sub ") {
		t.Errorf("renderSegments = %q, sub-days segment must be gone", got)
	}
}

// ─── VCD-62: Phase 3 — merge renderer ────────────────────────────────────────

// sampleStatusJSON returns a minimal CC statusLine stdin JSON for testing.
func sampleStatusJSON() []byte {
	in := statusInput{}
	in.ContextWindow.TotalInputTokens = 5000
	in.ContextWindow.ContextWindowSize = 200000
	b, _ := json.Marshal(in)
	return b
}

// writePriorFile writes a statusline-prior.json under the given dir.
func writePriorFile(t *testing.T, dir, cmd string) {
	t.Helper()
	prior := struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}{"command", cmd}
	b, _ := json.Marshal(prior)
	if err := os.WriteFile(filepath.Join(dir, "statusline-prior.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunPriorCommand_EchoOutput(t *testing.T) {
	out, err := runPriorCommand("echo PRIOR", []byte("{}"))
	if err != nil {
		t.Fatalf("runPriorCommand: %v", err)
	}
	if strings.TrimSpace(out) != "PRIOR" {
		t.Fatalf("expected PRIOR, got %q", out)
	}
}

func TestRunPriorCommand_ErrorReturnsErr(t *testing.T) {
	_, err := runPriorCommand("exit 1", []byte("{}"))
	if err == nil {
		t.Fatal("expected error from failing command")
	}
}

func TestRunStatuslineMerge_ComposesOutput(t *testing.T) {
	// Point HOME to a temp dir so ~/.void-code/statusline-prior.json is under our control.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	// Create ~/.void-code/ dir and write a prior file.
	vcDir := filepath.Join(tmpHome, ".void-code")
	if err := os.MkdirAll(vcDir, 0700); err != nil {
		t.Fatal(err)
	}
	writePriorFile(t, vcDir, "echo CUSTOM-BAR")

	var out bytes.Buffer
	if err := runStatuslineMerge(bytes.NewReader(sampleStatusJSON()), &out); err != nil {
		t.Fatalf("runStatuslineMerge: %v", err)
	}
	result := out.String()
	if !strings.Contains(result, "CUSTOM-BAR") {
		t.Errorf("expected CUSTOM-BAR in output, got %q", result)
	}
	// vc segment should also be present (emoji from 5k tokens)
	if !strings.Contains(result, "🤓") {
		t.Errorf("expected vc emoji segment in output, got %q", result)
	}
}

func TestRunStatuslineMerge_PriorFailFallsBackToVcOnly(t *testing.T) {
	// Point HOME to a temp dir with a failing prior command.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	vcDir := filepath.Join(tmpHome, ".void-code")
	if err := os.MkdirAll(vcDir, 0700); err != nil {
		t.Fatal(err)
	}
	writePriorFile(t, vcDir, "exit 99")

	var out bytes.Buffer
	if err := runStatuslineMerge(bytes.NewReader(sampleStatusJSON()), &out); err != nil {
		t.Fatalf("runStatuslineMerge: %v", err)
	}
	result := out.String()
	// Must still produce a vc line — never blank, never error.
	if strings.TrimSpace(result) == "" {
		t.Fatal("runStatuslineMerge produced empty output on prior failure")
	}
}

func TestRunStatuslineMerge_NoPriorFile_FallsBackToPlain(t *testing.T) {
	// No prior file at all → plain vc output.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	var out bytes.Buffer
	if err := runStatuslineMerge(bytes.NewReader(sampleStatusJSON()), &out); err != nil {
		t.Fatalf("runStatuslineMerge: %v", err)
	}
	result := out.String()
	if strings.TrimSpace(result) == "" {
		t.Fatal("runStatuslineMerge produced empty output without prior file")
	}
}

// ─── VCD-64: TTY-bypass demo + menu item ─────────────────────────────────────

// TestRunStatuslineDemo_OutputsLineWithEmoji verifies that runStatuslineDemo prints
// a non-empty line containing the context emoji and the "(demo)" suffix.
// This is the function used when vc statusline is invoked from a TTY (no CC stdin).
func TestRunStatuslineDemo_OutputsLineWithEmoji(t *testing.T) {
	var out bytes.Buffer
	runStatuslineDemo(&out)
	result := out.String()
	if strings.TrimSpace(result) == "" {
		t.Fatal("runStatuslineDemo: empty output")
	}
	// 45k tokens → 🤓 face
	if !strings.Contains(result, "🤓") {
		t.Errorf("runStatuslineDemo: expected 🤓 emoji for 45k tokens, got %q", result)
	}
	// Must include the demo label so user understands it's not live data.
	if !strings.Contains(result, "demo") {
		t.Errorf("runStatuslineDemo: expected '(demo' suffix, got %q", result)
	}
}

// ─── VCD-69: Context-token color bands (green/yellow/red at 80k/120k) ─────────

func TestContextColorTier_Boundaries(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "green"},
		{79999, "green"},
		{80000, "yellow"},
		{119999, "yellow"},
		{120000, "red"},
		{200000, "red"},
	}
	for _, c := range cases {
		got := contextColorTier(c.tokens)
		if got != c.want {
			t.Errorf("contextColorTier(%d) = %q, want %q", c.tokens, got, c.want)
		}
	}
}

// ─── VCD-64 REVISION: Install statusline menu item ───────────────────────────

// TestInstallStatuslineMenu_FreshInstall verifies that installStatuslineMenu writes
// the statusLine entry when settings.json is absent (fresh user).
func TestInstallStatuslineMenu_FreshInstall(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	slCmd := "/usr/local/bin/vc statusline"

	var out bytes.Buffer
	installStatuslineMenu(&out, settingsPath, slCmd)

	result := out.String()
	if !strings.Contains(result, "✓") {
		t.Errorf("expected ✓ in output for fresh install, got %q", result)
	}
	if !strings.Contains(result, "installed") {
		t.Errorf("expected 'installed' in output, got %q", result)
	}

	// Verify settings.json was actually written with our statusLine entry.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if !strings.Contains(string(data), slCmd) {
		t.Errorf("settings.json does not contain slCmd %q: %s", slCmd, data)
	}
}

// TestInstallStatuslineMenu_AlreadyInstalled verifies that installStatuslineMenu
// reports "already current" without modifying the file when ours is already set.
func TestInstallStatuslineMenu_AlreadyInstalled(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	slCmd := "/usr/local/bin/vc statusline"

	// Pre-write our statusLine into settings.json.
	existing := `{"statusLine":{"type":"command","command":"/usr/local/bin/vc statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0600); err != nil {
		t.Fatal(err)
	}
	fi1, _ := os.Stat(settingsPath)

	var out bytes.Buffer
	installStatuslineMenu(&out, settingsPath, slCmd)

	result := out.String()
	if !strings.Contains(result, "already current") {
		t.Errorf("expected 'already current' in output, got %q", result)
	}

	// File must not be rewritten (idempotent).
	fi2, _ := os.Stat(settingsPath)
	if fi1.ModTime() != fi2.ModTime() {
		t.Error("installStatuslineMenu rewrote settings.json when already installed — should be no-op")
	}
}

// TestInstallStatuslineMenu_ForeignOffersFixSelect verifies that installStatuslineMenu
// produces a fixSelect (3-way prompt) when a foreign statusLine is present.
// We verify via checkStatusLine directly since promptSelect is interactive/TTY.
func TestInstallStatuslineMenu_ForeignCheckResult(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	slCmd := "/usr/local/bin/vc statusline"

	// Write a foreign statusLine.
	foreign := `{"statusLine":{"type":"command","command":"~/.claude/statusline.sh"}}`
	if err := os.WriteFile(settingsPath, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}

	c := checkStatusLine(settingsPath, slCmd)
	if c.fixSelect == nil {
		t.Fatal("foreign statusLine: expected fixSelect to be non-nil (3-way prompt)")
	}
	if len(c.selectOptions) != 3 {
		t.Fatalf("foreign statusLine: want 3 selectOptions (merge/override/skip), got %d: %v",
			len(c.selectOptions), c.selectOptions)
	}
}
