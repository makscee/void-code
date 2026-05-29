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

// contextFace returns the brainrot emoji face for total_input_tokens.
// Mirrors cv-statusline.sh thresholds exactly.
//
//	< 60k  → 🤓
//	60-120k → 😐
//	120-150k → 😵‍💫
//	150-180k → 🥴
//	>= 180k → 💀
func contextFace(tokens int) string {
	switch {
	case tokens < 60000:
		return "🤓"
	case tokens < 120000:
		return "😐"
	case tokens < 150000:
		return "😵‍💫" // 😵‍💫 (zwj sequence)
	case tokens < 180000:
		return "🥴"
	default:
		return "💀"
	}
}

// contextTokensFmt formats a token count compactly: 1200 → "1k", 143210 → "143k", 1200000 → "1.2M".
func contextTokensFmt(tokens int) string {
	if tokens >= 1_000_000 {
		v := float64(tokens) / 1_000_000
		// One decimal, strip trailing zero: 1.50M → "1.5M", 2.00M → "2M"
		s := fmt.Sprintf("%.1f", v)
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
		return s + "M"
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%dk", tokens/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

// contextSegment returns the brainrot context segment string.
// Uses total_input_tokens from context_window. Returns "—" if absent (no API response yet).
func contextSegment(in statusInput) string {
	cw := in.ContextWindow
	tokens := cw.TotalInputTokens
	// Absent = no API response yet: all fields zero.
	if tokens == 0 && cw.ContextWindowSize == 0 {
		return "—"
	}
	return contextFace(tokens) + " " + contextTokensFmt(tokens)
}

// renderSegments builds the one-line status bar. Pure — no I/O.
// Order: ctx | budget | sub
func renderSegments(in statusInput, d segData) string {
	parts := []string{}

	// Segment 1: brainrot context window — emoji face + compact token count.
	parts = append(parts, contextSegment(in))

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

	// Segment 2 (budget %): call /v1/vc/me via auth.FetchMe to get budget pct.
	// Degrade-safe: hidden when endpoint absent / budget_usd=0 (pct null) / error.
	if pct, ok := fetchBudgetPct(cfg.AuthHost, token); ok {
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

// fetchBudgetPct calls GET <authHost>/v1/vc/me (via auth.FetchMe) and returns
// (pct as int, ok). Returns ok=false on ANY error, non-200, missing budget
// fields, or pct==null (budget_usd=0, no cap) → segment hidden.
// 2-second timeout keeps the statusline snappy.
func fetchBudgetPct(authHost, token string) (int, bool) {
	client := &http.Client{Timeout: 2 * time.Second}
	me, err := auth.FetchMe(authHost, token, client)
	if err != nil || me.Pct == nil {
		// err → server error / network / 401; Pct nil → no budget set (no cap)
		return 0, false
	}
	return int(*me.Pct), true
}
