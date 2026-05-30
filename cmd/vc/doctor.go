package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/clackui"
	"github.com/spf13/cobra"
)

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
	name    string
	status  string // "✓", "✗", "!"
	message string
	fix     func() error // nil if no fix available
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
type confirmModel struct {
	question string
	cursor   int  // 0 = Yes, 1 = No
	chosen   bool
	quitting bool
}

func newConfirmModel(question string) confirmModel {
	return confirmModel{question: question, cursor: 1} // default = No
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
	// ◆  <question>
	sb.WriteString(clackui.RailLine("◆", "  "+clackui.InfoTextStyle.Render(m.question)))
	sb.WriteString("\n")
	// │  ●  Yes  /  │  ○  No
	opts := []string{"Yes", "No"}
	for i, opt := range opts {
		if i == m.cursor {
			sb.WriteString(clackui.RailLine("│", "  "+clackui.SelectedItemStyle.Render("●  "+opt)))
		} else {
			sb.WriteString(clackui.RailLine("│", "  "+clackui.UnselectedItemStyle.Render("○  "+opt)))
		}
		sb.WriteString("\n")
	}
	sb.WriteString(clackui.RailLine("│", "  "+clackui.HintStyle.Render("↑/↓ · enter")))
	sb.WriteString("\n")
	return sb.String()
}

// promptConfirm shows a clack-style ◆ Yes/No selector and returns true if the
// user chose Yes. Returns false on any quit/cancel. Non-TTY (bubbletea error)
// falls back to returning false (the safe default-no).
func promptConfirm(question string) bool {
	m := newConfirmModel(question)
	p := tea.NewProgram(m)
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

	// ┌  doctor
	doctorRailLine("┌", "  "+clackui.TitleStyle.Render("doctor"))
	// │
	doctorRailLine("│", "")

	for _, c := range checks {
		// │  ✓/✗/!  <name>   <detail>
		doctorRailLine("│", renderCheckLine(c))

		if c.fix != nil {
			// Blank rail line before the prompt.
			doctorRailLine("│", "")
			if promptConfirm("Install the void-code statusline now?") {
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
	}

	// │
	doctorRailLine("│", "")
	// └  done
	doctorRailLine("└", "  "+clackui.HintStyle.Render("done"))

	return nil
}

// buildChecks assembles the slice of checks. Extensible — add more check funcs here.
func buildChecks(settingsPath, slCmd string) []checkResult {
	return []checkResult{
		checkStatusLine(settingsPath, slCmd),
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
