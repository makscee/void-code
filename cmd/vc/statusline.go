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

	"github.com/charmbracelet/lipgloss"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
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

// segData carries network-derived and config-derived segments so the pure renderer stays testable.
// balanceKnown=false → hide the $ segment (auth error / field absent / not logged in).
// providerLabel is always non-empty (defaults to the Relay label when nothing is persisted).
type segData struct {
	balanceUsd    *float64
	balanceKnown  bool
	providerLabel string // active provider display label, read from config (no network call)
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

// contextColorTier returns the color band for total_input_tokens.
// Bands: green < 80k, yellow 80k–120k, red >= 120k.
func contextColorTier(tokens int) string {
	switch {
	case tokens < 80000:
		return "green"
	case tokens < 120000:
		return "yellow"
	default:
		return "red"
	}
}

// contextColor returns a lipgloss.Style for the context segment based on token count.
// Green < 80k (healthy), yellow 80k–120k (approaching cap), red+bold >= 120k (over cap).
func contextColor(tokens int) lipgloss.Style {
	switch contextColorTier(tokens) {
	case "yellow":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // ANSI yellow
	case "red":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // ANSI red, bold
	default: // green
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // ANSI green
	}
}

// contextSegment returns the brainrot context segment string, colored by token count.
// Uses total_input_tokens from context_window. Returns "—" if absent (no API response yet).
func contextSegment(in statusInput) string {
	cw := in.ContextWindow
	tokens := cw.TotalInputTokens
	// Absent = no API response yet: all fields zero.
	if tokens == 0 && cw.ContextWindowSize == 0 {
		return "—"
	}
	text := contextFace(tokens) + " " + contextTokensFmt(tokens)
	return contextColor(tokens).Render(text)
}

// renderSegments builds the one-line status bar. Pure — no I/O.
// Order: ctx | $balance (optional) | provider
func renderSegments(in statusInput, d segData) string {
	parts := []string{contextSegment(in)} // brainrot context emoji — unchanged

	// Segment 2: $ balance. Hidden when balanceKnown=false (not logged in / field absent).
	if d.balanceKnown && d.balanceUsd != nil {
		parts = append(parts, fmt.Sprintf("$%.2f", *d.balanceUsd))
	}

	// Segment 3: active provider label. Always shown — never blank.
	if d.providerLabel != "" {
		parts = append(parts, d.providerLabel)
	}

	return strings.Join(parts, " | ")
}

var statuslineMerge bool

var statuslineCmd = &cobra.Command{
	Use:    "statusline",
	Hidden: true,
	Short:  "Internal: Claude Code statusLine renderer (context · $ balance)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// When stdin is an interactive terminal there is no CC JSON to decode;
		// reading would block forever. Print a demo preview instead.
		if isStdinTTY() {
			runStatuslineDemo(os.Stdout)
			return nil
		}
		if statuslineMerge {
			return runStatuslineMerge(os.Stdin, os.Stdout)
		}
		return runStatusline(os.Stdin, os.Stdout)
	},
}

// runStatuslineDemo prints a static preview of the statusline bar.
// Used when vc statusline is invoked interactively (TTY stdin) — no CC JSON feed.
func runStatuslineDemo(w io.Writer) {
	// Synthetic "demo" context: 45k tokens, known balance placeholder.
	demo := statusInput{}
	demo.ContextWindow.TotalInputTokens = 45000
	demo.ContextWindow.ContextWindowSize = 200000
	d := fetchSegData() // fetch real balance if logged in; otherwise hidden
	fmt.Fprintln(w, renderSegments(demo, d)+" (demo — pipe CC statusLine JSON to see live data)")
}

// installStatuslineMenu runs the statusline install flow for a given settingsPath
// and slCmd. It reuses checkStatusLine (doctor.go, same package) which handles
// all three cases:
//   - already installed → report "already current"
//   - absent            → install directly (EnsureStatusLine, no prompt needed)
//   - foreign           → 3-way merge/override/skip prompt (VCD-62 logic)
//
// The result is reported to w. The caller is responsible for the "press enter"
// prompt after this returns. Separated from runInstallStatuslineMenu for testability.
func installStatuslineMenu(w io.Writer, settingsPath, slCmd string) {
	c := checkStatusLine(settingsPath, slCmd)
	switch {
	case c.fix == nil && c.fixSelect == nil:
		// Already installed (or merge-wrapper) — nothing to do.
		fmt.Fprintln(w, "  ✓  statusline already current")

	case c.fix != nil:
		// Absent — install directly, no prompt needed.
		if err := c.fix(); err != nil {
			fmt.Fprintln(w, "  install statusline: error:", err)
			return
		}
		fmt.Fprintln(w, "  ✓  statusline installed")

	case c.fixSelect != nil:
		// Foreign statusLine — offer merge/override/skip exactly as doctor does.
		header := buildConfirmHeader([]checkResult{c})
		choice, ok := promptSelect(c.message, header, c.selectOptions)
		if !ok {
			fmt.Fprintln(w, "  statusline install cancelled")
			return
		}
		if err := c.fixSelect(choice); err != nil {
			fmt.Fprintln(w, "  install statusline: error:", err)
			return
		}
		switch choice {
		case 0:
			fmt.Fprintln(w, "  ✓  statusline merged (vc wraps existing)")
		case 1:
			fmt.Fprintln(w, "  ✓  statusline installed (existing replaced)")
		default:
			fmt.Fprintln(w, "  skipped — existing statusline kept")
		}
	}
}

// runInstallStatuslineMenu resolves the binary path + settings path and delegates
// to installStatuslineMenu. Called from the title-screen "Install statusline" item.
func runInstallStatuslineMenu(w io.Writer) {
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(w, "  install statusline: cannot resolve binary path:", err)
		return
	}
	settingsPath, err := ccsettings.SettingsPath()
	if err != nil {
		fmt.Fprintln(w, "  install statusline: cannot resolve settings path:", err)
		return
	}
	slCmd := ccsettings.StatusLineCmd(ccsettings.ForwardSlash(execPath))
	installStatuslineMenu(w, settingsPath, slCmd)
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

// statuslineHTTPClient returns the HTTP client the statusline uses to reach the
// auth host. It MUST bypass any inherited HTTPS_PROXY.
//
// The statusline runs as a child of `claude`, whose environment carries
// HTTPS_PROXY=<relay> for relay / relay-provider harnesses (Plain strips it).
// A default client honors that proxy via ProxyFromEnvironment and tunnels the
// /v1/vc/me request through the relay — which only fronts the model upstream,
// not auth.makscee.ru — so the call fails and the $ balance segment silently
// disappears on relay/deepseek providers (it worked under Plain only because
// Plain strips HTTPS_PROXY). Proxy:nil forces a direct dial to the auth host.
func statuslineHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
}

// fetchSegData fetches network-derived segment data and reads config-derived fields.
// Returns unknown sentinels on any failure — never blocks the CC UI.
func fetchSegData() segData {
	d := newSegDataUnknown()

	// Provider label: read from config file — no network call.
	d.providerLabel = provider.LoadLabel()

	cfg := config.OSResolve()
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return d // not logged in → $ hidden
	}
	token = strings.TrimSpace(token)
	me, err := auth.FetchMe(cfg.AuthHost, token, statuslineHTTPClient())
	if err != nil || me.BalanceUsd == nil {
		return d // error / field absent → hide $ segment
	}
	d.balanceUsd = me.BalanceUsd
	d.balanceKnown = true
	return d
}
