package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/clackui"
	"github.com/makscee/void-code/internal/claudebin"
	"github.com/makscee/void-code/internal/provider"
	"github.com/spf13/cobra"
)

// minNodeMajor is the minimum node major version required by @anthropic-ai/claude-code.
// Sourced from claude-code engines.node >=18.0.0 (verified 2026-05-31, version 2.1.158).
const minNodeMajor = 18

// ─── statusLine classification ────────────────────────────────────────────────

type slStatus int

const (
	slAbsent    slStatus = iota // no statusLine key in settings.json (or file absent)
	slInstalled                 // statusLine is ours (command ends with " statusline")
	slForeign                   // statusLine exists but is a foreign command
)

// classifyStatusLine reads settingsPath and returns the statusLine classification.
// slCmd is the expected command string (e.g. "/abs/vc statusline") — used to match
// against the " statusline" suffix ownership key, same as EnsureStatusLine.
func classifyStatusLine(settingsPath, slCmd string) slStatus {
	_ = slCmd // ownership key is suffix-based, not value-based
	data, err := os.ReadFile(settingsPath)
	if os.IsNotExist(err) {
		return slAbsent
	}
	if err != nil {
		return slAbsent // treat read errors as absent for doctor purposes
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return slAbsent // unparseable — treat as absent (do not clobber)
	}
	sl, ok := obj["statusLine"].(map[string]any)
	if !ok || sl == nil {
		return slAbsent
	}
	cmd, _ := sl["command"].(string)
	if strings.HasSuffix(cmd, " statusline") {
		return slInstalled
	}
	return slForeign
}

// ─── budget-left math ─────────────────────────────────────────────────────────

// budgetLeft returns (N% left, show) where N = round(100 - pct), clamped to [0,100].
// Returns (0, false) when pct is nil (no budget cap).
func budgetLeft(pct *float64) (int, bool) {
	if pct == nil {
		return 0, false
	}
	left := 100.0 - *pct
	if left < 0 {
		left = 0
	}
	return int(math.Round(left)), true
}

// ─── doctor check type ────────────────────────────────────────────────────────

type checkResult struct {
	name     string
	status   string   // "✓", "✗", "!"
	message  string
	fix      func() error // nil if no fix available (interactive yes/no prompt)
	guidance []string     // non-interactive extra lines printed after the check (no prompt)
}

// ─── clack rail rendering for doctor ─────────────────────────────────────────

// doctorRailLine emits a rail line to stdout.
func doctorRailLine(prefix, content string) {
	fmt.Println(clackui.RailLine(prefix, content))
}

// renderCheckLine renders a single check on the rail with appropriate icon styling.
func renderCheckLine(c checkResult) string {
	var icon string
	switch c.status {
	case "✓":
		icon = clackui.OkStyle.Render("✓")
	case "✗":
		icon = clackui.FailStyle.Render("✗")
	default: // "!"
		icon = clackui.WarnStyle.Render("!")
	}
	// Extract just the name part for compact rail display, fallback to full message.
	label := c.name
	detail := c.message
	// Strip "<name>: " prefix from message to avoid duplication when name != message.
	if after, found := strings.CutPrefix(detail, label+": "); found {
		detail = after
	}
	return "  " + icon + "  " + clackui.InfoTextStyle.Render(label) +
		"   " + clackui.HintStyle.Render(detail)
}

// RenderDoctorChecksForTest is an exported test helper that renders check lines
// without running the interactive prompt — used from doctor_test.go.
func RenderDoctorChecksForTest(checks []checkResult) string {
	var sb strings.Builder
	sb.WriteString(clackui.RailLine("┌", "  "+clackui.TitleStyle.Render("doctor")))
	sb.WriteString("\n")
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")
	for _, c := range checks {
		sb.WriteString(clackui.RailLine("│", renderCheckLine(c)))
		sb.WriteString("\n")
	}
	return sb.String()
}

// ─── bubbletea confirm model (Yes/No prompt) ─────────────────────────────────

// confirmModel is a minimal bubbletea model for a clack-style Yes/No selector.
// Default selection = No (index 1), matching the old [y/N] default.
//
// header holds the pre-rendered lines that precede the ◆ prompt (e.g. the
// ┌  doctor cap + check lines). Including them in View() keeps the whole
// transcript visible in a single inline bubbletea render pass — no alt-screen,
// no garbled ^0 escape artifacts from mixing fmt.Println with TUI redraws.
type confirmModel struct {
	question string
	header   string // pre-rendered lines above the ◆ prompt (e.g. ┌ doctor + checks)
	cursor   int    // 0 = Yes, 1 = No
	chosen   bool
	quitting bool
}

func newConfirmModel(question, header string) confirmModel {
	return confirmModel{question: question, header: header, cursor: 1} // default = No
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k", "left", "h":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j", "right", "l":
		if m.cursor < 1 {
			m.cursor++
		}
	case "enter", " ":
		m.chosen = true
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() string {
	if m.quitting {
		return ""
	}
	var sb strings.Builder
	// Pre-rendered header: ┌  doctor + blank rail + check lines + blank rail.
	// Embedding it here keeps the full layout in one bubbletea render, avoiding
	// ^0 escape artifacts that occur when fmt.Println and TUI redraws interleave.
	if m.header != "" {
		sb.WriteString(m.header)
	}
	// ◆  <question>
	sb.WriteString(clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render(m.question)))
	sb.WriteString("\n")
	// │  ○  Yes  /  │  ●  No  (cursor-driven)
	opts := []string{"Yes", "No"}
	for i, opt := range opts {
		if i == m.cursor {
			sb.WriteString(clackui.RailLine("│", "  "+clackui.SelectedItemStyle.Render("●  "+opt)))
		} else {
			sb.WriteString(clackui.RailLine("│", "  "+clackui.UnselectedItemStyle.Render("○  "+opt)))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")
	// └  ↑/↓ · enter  (bottom cap)
	sb.WriteString(clackui.RailLine("└", "  "+clackui.HintStyle.Render("↑/↓ · enter")))
	sb.WriteString("\n")
	return sb.String()
}

// buildConfirmHeader builds the pre-rendered header string for the confirmModel:
// ┌  doctor
// │
// │  <check lines>
// │
// This is passed into confirmModel so its View() renders the complete layout
// without any fmt.Println calls preceding the bubbletea program (which would
// produce garbled ^0 escape artifacts).
func buildConfirmHeader(checks []checkResult) string {
	var sb strings.Builder
	sb.WriteString(clackui.RailLine("┌", "  "+clackui.TitleStyle.Render("doctor")))
	sb.WriteString("\n")
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")
	for _, c := range checks {
		sb.WriteString(clackui.RailLine("│", renderCheckLine(c)))
		sb.WriteString("\n")
	}
	sb.WriteString(clackui.RailLine("│", ""))
	sb.WriteString("\n")
	return sb.String()
}

// promptConfirm shows a clack-style ◆ Yes/No selector with the given header
// rendered above the prompt. Returns true if the user chose Yes.
// Returns false on any quit/cancel or non-TTY (safe default-no).
func promptConfirm(question, header string) bool {
	m := newConfirmModel(question, header)
	// Run inline (no alt-screen) so the full transcript is visible as one block.
	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	out, err := p.Run()
	if err != nil {
		return false // non-TTY fallback = No
	}
	fm, ok := out.(confirmModel)
	if !ok || !fm.chosen || fm.cursor != 0 {
		return false
	}
	return true
}

// ─── doctor command ───────────────────────────────────────────────────────────

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check vc setup and fix common issues",
	Long: `Run diagnostic checks on your vc installation and offer to fix any issues found.

Currently checks:
  statusline   Claude Code statusLine renderer (context · budget · sub days)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDoctor()
	},
}

func init() { rootCmd.AddCommand(doctorCmd) }

func runDoctor() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("doctor: cannot resolve vc binary path: %w", err)
	}
	execPath = ccsettings.ForwardSlash(execPath)

	settingsPath, err := ccsettings.SettingsPath()
	if err != nil {
		return fmt.Errorf("doctor: cannot resolve settings path: %w", err)
	}

	slCmd := ccsettings.StatusLineCmd(execPath)

	checks := buildChecks(settingsPath, slCmd)

	// Determine whether any check needs an interactive fix prompt.
	// If yes: render the entire layout (header + checks + blank + prompt) inside
	// a single bubbletea program so no fmt.Println/TUI interleaving produces
	// garbled ^0 escape artifacts (defect 1 fix).
	// If no fix needed: just print everything to stdout directly.
	needsFix := false
	for _, c := range checks {
		if c.fix != nil {
			needsFix = true
			break
		}
	}

	if needsFix {
		// Build the pre-rendered header (┌  doctor + blank + check lines + blank).
		// This is passed into confirmModel.View() so the full layout is one block.
		header := buildConfirmHeader(checks)

		for _, c := range checks {
			if c.fix == nil {
				continue
			}
			// The bubbletea confirm renders the complete view including header,
			// the ◆ question line, and the ○/● options (defect 2 fix: question present).
			if promptConfirm("Install void-code statusline now?", header) {
				if err := c.fix(); err != nil {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					continue
				}
				// Re-verify after fix.
				after := classifyStatusLine(settingsPath, slCmd)
				doctorRailLine("│", "")
				if after == slInstalled {
					doctorRailLine("│", "  "+clackui.OkStyle.Render("✓")+"  "+clackui.InfoTextStyle.Render("statusline")+"   "+clackui.HintStyle.Render("installed"))
				} else {
					doctorRailLine("│", "  "+clackui.WarnStyle.Render("!")+"  "+clackui.InfoTextStyle.Render("statusline")+"   "+clackui.HintStyle.Render("install may have failed — run `vc doctor` again"))
				}
			}
		}
		// Bottom cap after the confirm + any post-fix lines.
		doctorRailLine("│", "")
		doctorRailLine("└", "  "+clackui.HintStyle.Render("done"))
	} else {
		// No interactive fix needed — print directly to stdout.
		// ┌  doctor
		doctorRailLine("┌", "  "+clackui.TitleStyle.Render("doctor"))
		// │
		doctorRailLine("│", "")
		for _, c := range checks {
			doctorRailLine("│", renderCheckLine(c))
			// Print any non-interactive guidance lines (e.g. install instructions).
			for _, g := range c.guidance {
				doctorRailLine("│", "    "+clackui.HintStyle.Render(g))
			}
		}
		// │
		doctorRailLine("│", "")
		// └  done
		doctorRailLine("└", "  "+clackui.HintStyle.Render("done"))
	}

	return nil
}

// checkNode verifies that node is present and >= minNodeMajor.
func checkNode() checkResult {
	path, err := exec.LookPath("node")
	if err != nil {
		var guidance []string
		if runtime.GOOS == "darwin" {
			guidance = append(guidance, "re-run the vc install script: curl -fsSL https://auth.makscee.ru/vc/install.sh | sh")
			guidance = append(guidance, "or install via Homebrew: brew install node")
		} else if runtime.GOOS == "windows" {
			guidance = append(guidance, "re-run the vc install script to auto-install node")
			guidance = append(guidance, "or download from https://nodejs.org")
		} else {
			guidance = append(guidance, "install node >= "+strconv.Itoa(minNodeMajor)+" from https://nodejs.org")
		}
		return checkResult{
			name:     "node",
			status:   "✗",
			message:  fmt.Sprintf("node: not found (requires v%d+)", minNodeMajor),
			guidance: guidance,
		}
	}
	// Parse major version.
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return checkResult{
			name:    "node",
			status:  "✗",
			message: "node: found but --version failed",
		}
	}
	verStr := strings.TrimSpace(string(out))
	verStr = strings.TrimPrefix(verStr, "v")
	parts := strings.SplitN(verStr, ".", 2)
	major, convErr := strconv.Atoi(parts[0])
	if convErr != nil || major < minNodeMajor {
		var guidance []string
		guidance = append(guidance, fmt.Sprintf("node v%s found but requires v%d+ — re-run vc install script to upgrade", verStr, minNodeMajor))
		return checkResult{
			name:     "node",
			status:   "✗",
			message:  fmt.Sprintf("node: v%s (requires v%d+)", verStr, minNodeMajor),
			guidance: guidance,
		}
	}
	return checkResult{
		name:    "node",
		status:  "✓",
		message: "node: v" + verStr,
	}
}

// checkNpm verifies that npm is present on PATH.
func checkNpm() checkResult {
	npmBin := "npm"
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("npm.cmd"); err == nil {
			npmBin = "npm.cmd"
		}
	}
	path, err := exec.LookPath(npmBin)
	if err != nil && runtime.GOOS == "windows" {
		// fallback check for npm
		path, err = exec.LookPath("npm")
	}
	if err != nil {
		var guidance []string
		guidance = append(guidance, "npm is bundled with node — re-run vc install script to install node")
		guidance = append(guidance, "or download from https://nodejs.org")
		return checkResult{
			name:     "npm",
			status:   "✗",
			message:  "npm: not found",
			guidance: guidance,
		}
	}
	return checkResult{
		name:    "npm",
		status:  "✓",
		message: "npm: found at " + path,
	}
}

// checkBinOnPath verifies that ~/.void-code/bin is in the current $PATH.
func checkBinOnPath() checkResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return checkResult{
			name:    "bin on PATH",
			status:  "✗",
			message: "bin on PATH: cannot resolve home dir",
		}
	}
	binDir := filepath.Join(home, ".void-code", "bin")
	pathEnv := os.Getenv("PATH")
	// Split on OS path separator and check each entry.
	found := false
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry == binDir {
			found = true
			break
		}
	}
	if !found {
		return checkResult{
			name:    "bin on PATH",
			status:  "✗",
			message: "bin on PATH: " + binDir + " not in $PATH",
			guidance: []string{
				"open a new terminal (PATH is set for new shells)",
				"or run: source ~/.zshrc (zsh) / source ~/.bash_profile (bash)",
			},
		}
	}
	return checkResult{
		name:    "bin on PATH",
		status:  "✓",
		message: "bin on PATH: " + binDir,
	}
}

// checkNpmGlobalOnPath verifies that the npm global bin dir is in PATH on Windows.
// Only run on windows (claude shim lives at %APPDATA%\npm).
func checkNpmGlobalOnPath() checkResult {
	npmGlobal := filepath.Join(os.Getenv("APPDATA"), "npm")
	pathEnv := os.Getenv("PATH")
	found := false
	for _, entry := range filepath.SplitList(pathEnv) {
		if strings.EqualFold(entry, npmGlobal) {
			found = true
			break
		}
	}
	if !found {
		return checkResult{
			name:    "npm global on PATH",
			status:  "✗",
			message: "npm global on PATH: " + npmGlobal + " not in PATH",
			guidance: []string{
				"open a new terminal to pick up the updated PATH",
				"or re-run the vc install script to repair",
			},
		}
	}
	return checkResult{
		name:    "npm global on PATH",
		status:  "✓",
		message: "npm global on PATH: " + npmGlobal,
	}
}

// checkShellRcGap verifies on Darwin that the current shell's rc file contains
// the vc installer PATH marker so new terminals pick up ~/.void-code/bin.
func checkShellRcGap() checkResult {
	home, err := os.UserHomeDir()
	if err != nil {
		return checkResult{name: "shell rc", status: "✗", message: "shell rc: cannot resolve home dir"}
	}
	shellBin := os.Getenv("SHELL")
	if shellBin == "" {
		return checkResult{name: "shell rc", status: "!", message: "shell rc: $SHELL not set — cannot check"}
	}

	marker := "# added by vc installer"
	var rcFile string
	switch {
	case strings.HasSuffix(shellBin, "zsh"):
		rcFile = filepath.Join(home, ".zshrc")
	case strings.HasSuffix(shellBin, "bash"):
		if runtime.GOOS == "darwin" {
			rcFile = filepath.Join(home, ".bash_profile")
		} else {
			rcFile = filepath.Join(home, ".bashrc")
		}
	case strings.HasSuffix(shellBin, "fish"):
		rcFile = filepath.Join(home, ".config", "fish", "config.fish")
	default:
		rcFile = filepath.Join(home, ".profile")
	}

	data, err := os.ReadFile(rcFile)
	if err != nil {
		binDir := filepath.Join(home, ".void-code", "bin")
		return checkResult{
			name:    "shell rc",
			status:  "✗",
			message: "shell rc: " + rcFile + " not found — PATH marker missing",
			fix: func() error {
				var line string
				if strings.HasSuffix(shellBin, "fish") {
					line = "\n" + marker + "\nset -gx PATH " + binDir + " $PATH\n"
				} else {
					line = "\n" + marker + "\nexport PATH=\"" + binDir + ":$PATH\"\n"
				}
				f, err := os.OpenFile(rcFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = f.WriteString(line)
				return err
			},
		}
	}
	if strings.Contains(string(data), marker) {
		return checkResult{name: "shell rc", status: "✓", message: "shell rc: PATH marker present in " + rcFile}
	}
	binDir := filepath.Join(home, ".void-code", "bin")
	return checkResult{
		name:    "shell rc",
		status:  "✗",
		message: "shell rc: PATH marker missing from " + rcFile,
		fix: func() error {
			var line string
			if strings.HasSuffix(shellBin, "fish") {
				line = "\n" + marker + "\nset -gx PATH " + binDir + " $PATH\n"
			} else {
				line = "\n" + marker + "\nexport PATH=\"" + binDir + ":$PATH\"\n"
			}
			f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString(line)
			return err
		},
	}
}

// buildChecks assembles the slice of checks. Extensible — add more check funcs here.
func buildChecks(settingsPath, slCmd string) []checkResult {
	checks := []checkResult{
		checkNode(),
		checkNpm(),
		checkBinOnPath(),
		checkClaudeCLI(),
		checkStatusLine(settingsPath, slCmd),
		checkActiveProvider(),
	}
	if runtime.GOOS == "windows" {
		checks = append(checks, checkNpmGlobalOnPath())
	}
	if runtime.GOOS == "darwin" {
		checks = append(checks, checkShellRcGap())
	}
	return checks
}

// checkClaudeCLI verifies that the claude binary is reachable on PATH.
// vc is a wrapper over claude; if claude is missing vc cannot function at all.
func checkClaudeCLI() checkResult {
	path, err := claudebin.Resolve()
	if err == nil {
		return checkResult{
			name:    "claude CLI",
			status:  "✓",
			message: "claude CLI: found at " + path,
		}
	}
	// Build per-OS install guidance lines for display after the failed check.
	var guidance []string
	guidance = append(guidance, "")
	for _, line := range strings.Split(claudebin.InstallInstructions(), "\n") {
		guidance = append(guidance, line)
	}
	return checkResult{
		name:     "claude CLI",
		status:   "✗",
		message:  "claude CLI: not found in PATH",
		guidance: guidance,
	}
}

// checkActiveProvider reports which auth source claude will launch with.
func checkActiveProvider() checkResult {
	p := provider.Load()
	return checkResult{
		name:    "provider",
		status:  "✓",
		message: "provider: " + p.Label(),
	}
}

func checkStatusLine(settingsPath, slCmd string) checkResult {
	switch classifyStatusLine(settingsPath, slCmd) {
	case slInstalled:
		return checkResult{
			name:    "statusline",
			status:  "✓",
			message: "statusline: installed",
		}
	case slForeign:
		return checkResult{
			name:    "statusline",
			status:  "!",
			message: "statusline: a different statusLine is configured — leaving untouched",
		}
	default: // slAbsent
		return checkResult{
			name:    "statusline",
			status:  "✗",
			message: "statusline: not installed",
			fix: func() error {
				return ccsettings.EnsureStatusLine(settingsPath, slCmd)
			},
		}
	}
}
