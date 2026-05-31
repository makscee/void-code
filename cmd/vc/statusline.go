package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
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

// segData carries network-derived segments so the pure renderer stays testable.
// balanceKnown=false → hide the $ segment (auth error / field absent / not logged in).
type segData struct {
	balanceUsd   *float64
	balanceKnown bool
}

func newSegDataUnknown() segData { return segData{balanceKnown: false} }

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
// Order: ctx | $balance (optional)
func renderSegments(in statusInput, d segData) string {
	parts := []string{contextSegment(in)} // brainrot context emoji — unchanged

	// Segment 2: $ balance. Hidden when balanceKnown=false (not logged in / field absent).
	if d.balanceKnown && d.balanceUsd != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *d.balanceUsd))
	}

	return strings.Join(parts, " | ")
}

var statuslineMerge bool

var statuslineCmd = &cobra.Command{
	Use:    "statusline",
	Hidden: true,
	Short:  "Internal: Claude Code statusLine renderer (context · $ balance)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if statuslineMerge {
			return runStatuslineMerge(os.Stdin, os.Stdout)
		}
		return runStatusline(os.Stdin, os.Stdout)
	},
}

func init() {
	statuslineCmd.Flags().BoolVar(&statuslineMerge, "merge", false, "run prior statusLine command and prepend its output")
	rootCmd.AddCommand(statuslineCmd)
}

// runPriorCommand runs priorCmd via the OS shell, feeding stdin bytes to it,
// and returns the trimmed first line of stdout.
// On any error it returns ("", err) — callers must treat errors as empty output
// (never fail CC UI).
func runPriorCommand(priorCmd string, stdin []byte) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", priorCmd)
	} else {
		cmd = exec.Command("sh", "-c", priorCmd)
	}
	cmd.Stdin = bytes.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Trim trailing newline/whitespace; return first line only.
	line := strings.SplitN(strings.TrimRight(string(out), "\r\n"), "\n", 2)[0]
	return strings.TrimRight(line, "\r"), nil
}

// runStatuslineMerge implements "vc statusline --merge":
// reads stdin, runs the prior command (from statusline-prior.json) feeding the
// same stdin bytes, then composes prior output + vc segment on one line.
// NEVER errors out of band — mirrors runStatusline's fail-safe contract.
func runStatuslineMerge(r io.Reader, w io.Writer) error {
	// Buffer ALL of stdin so we can feed both the JSON decoder and the prior command.
	stdinBytes, err := io.ReadAll(r)
	if err != nil {
		fmt.Fprintln(w, "vc")
		return nil
	}

	// Decode vc's own segment.
	var in statusInput
	vcLine := "vc"
	if err := json.Unmarshal(stdinBytes, &in); err == nil {
		d := fetchSegData()
		vcLine = renderSegments(in, d)
	}

	// Run the prior command (if any).
	priorLine := ""
	if priorPath, err := config.StatusLinePriorPath(); err == nil {
		if data, err := os.ReadFile(priorPath); err == nil {
			var prior struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(data, &prior) == nil && prior.Command != "" {
				if out, err := runPriorCommand(prior.Command, stdinBytes); err == nil {
					priorLine = out
				}
				// On error: prior segment stays empty (fail-safe)
			}
		}
		// File absent or read error: priorLine stays empty
	}

	// Compose: "prior | vc" or just one side if the other is empty.
	switch {
	case priorLine == "":
		fmt.Fprintln(w, vcLine)
	case vcLine == "vc" && priorLine != "":
		// vc segment degenerate — just show prior
		fmt.Fprintln(w, priorLine+" | vc")
	default:
		fmt.Fprintln(w, priorLine+" | "+vcLine)
	}
	return nil
}

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
		return d // not logged in → $ hidden
	}
	token = strings.TrimSpace(token)
	client := &http.Client{Timeout: 2 * time.Second}
	me, err := auth.FetchMe(cfg.AuthHost, token, client)
	if err != nil || me.BalanceUsd == nil {
		return d // error / field absent → hide $ segment
	}
	d.balanceUsd = me.BalanceUsd
	d.balanceKnown = true
	return d
}
