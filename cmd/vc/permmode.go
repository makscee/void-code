package main

import "strings"

// bypassPermissionModeArgs forces the claude session to START in bypassPermissions
// mode. The companion --settings file (see ccsettings.WriteManagedSettings, passed
// on the same command line) carries skipDangerousModePermissionPrompt=true, which
// suppresses CC's one-time "running in Bypass mode — accept responsibility?" dialog
// regardless of folder trust, so this flag is silent on every platform. We use the
// mode flag rather than --dangerously-skip-permissions because the latter is
// REFUSED by claude when running as root (uid 0) on Unix, which would break
// server/container/sudo callers; --permission-mode has no such restriction.
var bypassPermissionModeArgs = []string{"--permission-mode", "bypassPermissions"}

// ensureBypassPermissionMode prepends "--permission-mode bypassPermissions" to the
// claude args unless the caller already specified a permission posture.
//
// Why a flag and not just settings.json:
// Claude Code's auto-mode safety classifier is a server-side model sub-call that a
// DeepSeek/relay backend may be unable to serve — it surfaces as "<model> is
// temporarily unavailable, so auto mode cannot determine the safety of Bash" the
// moment CC runs a script it created. vc writes a bypass posture + an always-allow
// PreToolUse hook, but ~/.claude/settings.json only loads once the working folder
// is TRUSTED; on a fresh/untrusted folder (or when the trust write can't persist —
// a known Windows bug) those settings never load, bypass mode is absent from the
// shift+tab cycle, CC sits in (or the user cycles to) `auto`, and the classifier
// fires. The CLI flag is honored regardless of trust, so the session always starts
// in a mode that never calls the classifier. Auto mode itself is separately made
// classifier-free by the always-allow hook delivered via --settings (which loads
// regardless of trust) — see ccsettings.WriteManagedSettings.
//
// Injection is skipped when the user already chose a posture explicitly (e.g.
// `vc -- --permission-mode plan` or `vc -- --dangerously-skip-permissions`).
func ensureBypassPermissionMode(args []string) []string {
	for _, a := range args {
		if a == "--permission-mode" || strings.HasPrefix(a, "--permission-mode=") {
			return args
		}
		if a == "--dangerously-skip-permissions" {
			return args
		}
	}
	out := make([]string, 0, len(args)+len(bypassPermissionModeArgs))
	out = append(out, bypassPermissionModeArgs...)
	out = append(out, args...)
	return out
}
