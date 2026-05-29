package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

// statusInput is the subset of CC's statusLine stdin JSON we consume.
// Schema verified against CC 2.1.157 (docs.claude.com statusline + operator statusline.sh).
type statusInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		TotalInputTokens    int `json:"total_input_tokens"`
		TotalOutputTokens   int `json:"total_output_tokens"`
		ContextWindowSize   int `json:"context_window_size"`
		UsedPercentage      int `json:"used_percentage"`
		RemainingPercentage int `json:"remaining_percentage"`
	} `json:"context_window"`
}

// segData carries the network-derived segments (budget %, sub days) so the
// pure renderer stays testable. Sentinel values signal "unknown → render hidden":
//   budgetPct  -1  → VCD-49 endpoint absent or error → hide segment
//   subDaysLeft -2  → auth error/not logged in → hide segment
//   subDaysLeft -1  → unlimited (real value from server)
//   subDaysLeft >=0 → days remaining
type segData struct {
	budgetPct   int // -1 unknown
	subDaysLeft int // -2 unknown; -1 unlimited; >=0 days
}

func newSegDataUnknown() segData { return segData{budgetPct: -1, subDaysLeft: -2} }

// contextPct returns (pct, ok). ok=false → context_window absent → render —.
func contextPct(in statusInput) (int, bool) {
	cw := in.ContextWindow
	if cw.UsedPercentage > 0 {
		return cw.UsedPercentage, true
	}
	if cw.ContextWindowSize > 0 && (cw.TotalInputTokens+cw.TotalOutputTokens) > 0 {
		used := cw.TotalInputTokens + cw.TotalOutputTokens
		return used * 100 / cw.ContextWindowSize, true
	}
	return 0, false
}

// renderSegments builds the one-line status bar. Pure — no I/O.
// Order: ctx | budget | sub
func renderSegments(in statusInput, d segData) string {
	parts := []string{}

	// Segment 1: context window usage.
	if pct, ok := contextPct(in); ok {
		parts = append(parts, fmt.Sprintf("ctx %d%%", pct))
	} else {
		parts = append(parts, "ctx —")
	}

	// Segment 2: budget spent %. -1 unknown (VCD-49 absent) → hide.
	if d.budgetPct >= 0 {
		parts = append(parts, fmt.Sprintf("budget %d%%", d.budgetPct))
	}

	// Segment 3: subscription days left. -2 unknown → hide; -1 unlimited; >=0 days.
	switch {
	case d.subDaysLeft == -2:
		// hidden
	case d.subDaysLeft == -1:
		parts = append(parts, "sub ∞")
	default:
		parts = append(parts, fmt.Sprintf("sub %dd", d.subDaysLeft))
	}

	return strings.Join(parts, " | ")
}

var statuslineCmd = &cobra.Command{
	Use:    "statusline",
	Hidden: true,
	Short:  "Internal: Claude Code statusLine renderer (context % · budget % · sub days)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatusline(os.Stdin, os.Stdout)
	},
}

func init() { rootCmd.AddCommand(statuslineCmd) }

// runStatusline reads one statusLine event from r, prints one line to w.
// NEVER errors out of band — a broken statusline must never break the CC UI.
// On any failure it prints a minimal line and returns nil.
func runStatusline(r io.Reader, w io.Writer) error {
	var in statusInput
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fmt.Fprintln(w, "vc") // fail-safe: never blank, never error
		return nil
	}
	d := fetchSegData()
	fmt.Fprintln(w, renderSegments(in, d))
	return nil
}

// fetchSegData fetches network-derived segment data.
// Returns unknown sentinels on any failure — never blocks the CC UI.
func fetchSegData() segData {
	d := newSegDataUnknown()

	cfg := config.OSResolve()
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return d // not logged in → sub hidden, budget hidden
	}
	token = strings.TrimSpace(token)

	if n, ok := fetchSubDaysLeft(cfg.AuthHost, token); ok {
		d.subDaysLeft = n
	}

	// Segment 2 (budget %): VCD-49 endpoint not yet deployed → graceful-degrade.
	// TODO(VCD-49): wire fetchBudgetPct once /v1/usage/me is deployed.
	// fetchBudgetPct returns false unconditionally until VCD-49 lands.
	_ = cfg // cfg retained for future use by fetchBudgetPct
	if pct, ok := fetchBudgetPct(token); ok {
		d.budgetPct = pct
	}

	return d
}

// fetchSubDaysLeft calls GET /v1/vc/me (reuses auth.FetchMe pattern) and returns (subDaysLeft, ok).
func fetchSubDaysLeft(authHost, token string) (int, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	me, err := auth.FetchMe(authHost, token, client)
	if err != nil {
		return 0, false
	}
	return me.SubDaysLeft, true
}

// fetchBudgetPct calls VCD-49's GET /v1/usage/me and returns (pct, ok).
// Returns ok=false on ANY error / non-200 / missing endpoint → segment hidden.
// Ships dormant (always returns false) until VCD-49 deploys and provides a
// KeysHost config field + base URL to call against.
// TODO(VCD-49): add keysHost param, resolve base URL, uncomment the HTTP call.
func fetchBudgetPct(_ string) (int, bool) {
	// VCD-49 endpoint not yet deployed — graceful-degrade: segment stays hidden.
	return 0, false
}
