package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/permguard"
	"github.com/spf13/cobra"
)

// hookCmd is a hidden internal subcommand registered as a Claude Code
// PreToolUse callback.  CC pipes the event JSON on stdin; we write the
// hookSpecificOutput JSON on stdout and exit 0.
var hookCmd = &cobra.Command{
	Use:    "hook",
	Hidden: true,
	Short:  "Internal: Claude Code PreToolUse permission hook (deepseek relay, no Anthropic egress)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHook(os.Stdin, os.Stdout)
	},
}

func init() { rootCmd.AddCommand(hookCmd) }

// ccEvent is the subset of the CC PreToolUse JSON event we consume.
type ccEvent struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

// runHook reads one PreToolUse event from r and writes the decision to w.
// It NEVER exits non-zero on policy decisions — deny is expressed in the
// JSON payload, not the process exit code.  Any internal error → fail-open.
func runHook(r io.Reader, w io.Writer) error {
	var ev ccEvent
	if err := json.NewDecoder(r).Decode(&ev); err != nil {
		// Malformed input: fail-open rather than blocking claude.
		return writeDecision(w, permguard.Decision{Decision: "allow", Reason: "hook: decode error, fail-open"})
	}

	g, err := permguard.Load()
	if err != nil {
		return writeDecision(w, permguard.Decision{Decision: "allow", Reason: "hook: rules load error, fail-open"})
	}

	// Wire the relay LLM classifier if we can resolve proxy + token.
	base, token := relayFallbackParams()
	if base != "" {
		g.SetClassifier(permguard.NewRelayClassifier(base, token))
	}

	d := g.Classify(permguard.Event{
		ToolName: ev.ToolName,
		Command:  ev.ToolInput.Command,
		FilePath: ev.ToolInput.FilePath,
		Cwd:      ev.Cwd,
	})
	return writeDecision(w, d)
}

// writeDecision encodes the CC hookSpecificOutput JSON to w.
func writeDecision(w io.Writer, d permguard.Decision) error {
	type hookOut struct {
		HookEventName      string `json:"hookEventName"`
		PermissionDecision string `json:"permissionDecision"`
		Reason             string `json:"permissionDecisionReason,omitempty"`
	}
	payload := map[string]any{
		"hookSpecificOutput": hookOut{
			HookEventName:      "PreToolUse",
			PermissionDecision: d.Decision,
			Reason:             d.Reason,
		},
	}
	return json.NewEncoder(w).Encode(payload)
}

// relayFallbackParams extracts the relay proxy URL and token from the
// environment that CC propagates to hook subprocesses.
//
// vc injects HTTPS_PROXY=http://<host> into claude's env; CC passes that
// env through to PreToolUse hook processes.  The token is loaded from
// ~/.void-code/token via the existing auth loader.
//
// Returns ("","") when unavailable — caller uses rules-only (fail-open) mode.
func relayFallbackParams() (base, token string) {
	proxy := os.Getenv("HTTPS_PROXY")
	if proxy == "" {
		proxy = os.Getenv("https_proxy")
	}
	if proxy == "" {
		return "", ""
	}
	// Hard-refuse if proxy looks like Anthropic (should never happen, but guard).
	if strings.Contains(strings.ToLower(proxy), "api.anthropic.com") {
		return "", ""
	}
	tok, _ := auth.LoadAndMigrate()
	return proxy, tok
}
