package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/makscee/void-code/internal/ccsettings"
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

	for _, c := range checks {
		printCheck(c)
		if c.fix != nil {
			if promptYN(fmt.Sprintf("Install the void-code statusline now?")) {
				if err := c.fix(); err != nil {
					fmt.Fprintf(os.Stderr, "  error: %v\n", err)
					continue
				}
				// Re-verify after fix.
				after := classifyStatusLine(settingsPath, slCmd)
				if after == slInstalled {
					fmt.Printf("  %s statusline: installed\n", green("✓"))
				} else {
					fmt.Printf("  %s statusline: install may have failed — run `vc doctor` again\n", yellow("!"))
				}
			}
		}
	}

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

func printCheck(c checkResult) {
	var icon string
	switch c.status {
	case "✓":
		icon = green(c.status)
	case "✗":
		icon = red(c.status)
	default:
		icon = yellow(c.status)
	}
	fmt.Printf("  %s %s\n", icon, c.message)
}

// promptYN asks a [y/N] question and returns true only if the user types 'y' or 'Y'.
func promptYN(question string) bool {
	fmt.Printf("  %s [y/N] ", question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		ans := strings.TrimSpace(scanner.Text())
		return strings.EqualFold(ans, "y")
	}
	return false
}

// ─── minimal ANSI colour helpers ─────────────────────────────────────────────

func green(s string) string  { return "\033[32m" + s + "\033[0m" }
func yellow(s string) string { return "\033[33m" + s + "\033[0m" }
func red(s string) string    { return "\033[31m" + s + "\033[0m" }
