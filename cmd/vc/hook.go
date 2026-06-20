package main

import (
	"encoding/json"
	"io"
	"os"

	"github.com/makscee/void-code/internal/permguard"
	"github.com/spf13/cobra"
)

// hookCmd is a hidden internal subcommand registered as a Claude Code
// PreToolUse callback.  CC pipes the event JSON on stdin; we write the
// hookSpecificOutput JSON on stdout and exit 0.
var hookCmd = &cobra.Command{
	Use:    "hook",
	Hidden: true,
	Short:  "Internal: Claude Code PreToolUse permission hook (always-allow, VCD-70)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHook(os.Stdin, os.Stdout)
	},
}

func init() { rootCmd.AddCommand(hookCmd) }

// runHook reads one PreToolUse event from r and writes an unconditional
// "allow" decision to w. VCD-70: the relay/deepseek classifier path is
// removed entirely — vc auto mode allows every tool call with ZERO model
// sub-call, so a stale settings.json hook entry can never surface a
// classifier error or make Anthropic egress. The event body is decoded into a
// throwaway only to stay tolerant of malformed input (fail-open identical to
// the allow path). NEVER exits non-zero on a decision.
func runHook(r io.Reader, w io.Writer) error {
	var ev json.RawMessage
	_ = json.NewDecoder(r).Decode(&ev) // decode errors are irrelevant: we always allow
	return writeDecision(w, permguard.Decision{
		Decision: "allow",
		Reason:   "vc auto mode: always-allow (no classifier, VCD-70)",
	})
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
