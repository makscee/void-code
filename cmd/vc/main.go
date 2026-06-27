// Binary vc — void-code relay harness for Claude Code.
//
// Version is injected at build time:
//
//	go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=v0.0.1" ./cmd/vc
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	term "github.com/charmbracelet/x/term"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/ccjson"
	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/claudebin"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/harness/direct"
	"github.com/makscee/void-code/internal/harness/relay"
	"github.com/makscee/void-code/internal/keystore"
	"github.com/makscee/void-code/internal/provider"
	"github.com/makscee/void-code/internal/update"
	"github.com/makscee/void-code/internal/version"
	"github.com/makscee/void-code/internal/welcome"
	"github.com/spf13/cobra"
)

// warnStyle is used for the subscription expiry hard-block message.
var warnStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#F59E0B")).
	Bold(true)

// meCache holds a cached result from FetchMe to avoid repeated auth-host calls.
var (
	meCacheResult *auth.MeResult
	meCacheExpiry time.Time
)

func main() {
	// Clean up any leftover .old binary from a previous Windows self-update.
	update.CleanOldBinary()

	// --version short-circuit before Cobra parses anything.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("vc %s\n", version.Version)
			return
		}
	}

	// --raw: skip title/menu screen and pass tty straight to claude.
	// Parse --raw early (before cobra.Execute) so we can skip the welcome gate.
	// Relay auth is one-shot env injection at Spawn time; vc need not stay
	// resident, so no pty-proxy is required — cmd.Run() passthrough is enough.
	hasRaw := false
	for _, a := range os.Args[1:] {
		if a == "--raw" {
			hasRaw = true
			break
		}
		if a == "--" {
			break // everything after -- is for claude
		}
	}

	// Persistent landing screen — shown on bare `vc` invocation (no sub-command).
	// Checks auth state, shows banner, waits for any keypress.
	// Any keypress → logged-in: spawn claude; logged-out: run login.
	// Skipped for sub-commands (login/logout/status/update) so automation works.
	// Skipped when --raw is set (jump straight to spawn, no TUI).
	subCmds := map[string]bool{"login": true, "logout": true, "status": true, "update": true, "hook": true, "doctor": true, "statusline": true}
	hasSubCmd := len(os.Args) > 1 && subCmds[os.Args[1]]
	if !hasSubCmd && !hasRaw {
		state := resolveAuthState()
		// Interactive only when stdin is a TTY AND --non-interactive was not
		// passed. cobra has not parsed flags yet at this point, so scan os.Args
		// for the flag directly (mirrors the early --raw scan above). When not
		// interactive, the title screen is skipped — same effect as --raw, but
		// the gate still distinguishes logged-in (spawn) from logged-out (fail).
		interactive := isStdinTTY() && !hasNonInteractiveArg()
		switch decideGate(interactive, state.LoggedIn) {
		case gateFailAuth:
			// Non-interactive (non-TTY) context with no usable token: fail fast
			// instead of hanging in the login picker or device-flow poll loop.
			// Automation callers (void-os, subagents, scripts) re-auth manually.
			fmt.Fprintln(os.Stderr, "vc: auth failed: session token missing or expired — re-authenticate with `vc login`")
			os.Exit(1)
		case gateSpawn:
			// Non-interactive + logged in: skip the welcome TUI entirely. It
			// blocks on a keypress that a non-TTY stdin can never deliver, which
			// hangs automation callers forever. Fall straight through to spawn.
		case gateShowWelcome:
			// Interactive terminal: async update probe, then the landing screen.
			nudge := launchUpdateCheck()
			if nudge != "" {
				state.UpdateNudge = nudge
			}
		menuLoop:
			for {
				keyNames, _ := keystore.ListKeys()
				activeProv := provider.Load()
				// Fetch the granted provider list from void-auth (VCD-72).
				// Failure degrades to an empty list — relay/deepseek baseline preserved.
				var grantedRows []welcome.ProviderRowInfo
				fetchOK := false
				if tok, _ := auth.LoadAndMigrate(); strings.TrimSpace(tok) != "" {
					cfg := config.OSResolve()
					if infos, gErr := auth.FetchProviders(cfg.AuthHost, tok, &http.Client{Timeout: 10 * time.Second}); gErr == nil {
						for _, pi := range infos {
							grantedRows = append(grantedRows, welcome.ProviderRowInfo{ID: pi.ID, Name: pi.Name})
						}
						fetchOK = true
					} else {
						fmt.Fprintf(os.Stderr, "vc: could not fetch providers (%v) — showing relay default only\n", gErr)
					}
				} else {
					// No token — still reconcile non-relay-provider labels (derived locally).
					fetchOK = true
				}
				// VCD-79: reconcile + persist active provider label from live list so
				// pre-existing prov:<id> configs (no label) self-heal on first launch.
				if fetchOK {
					granted := make([]provider.GrantedEntry, len(grantedRows))
					for i, r := range grantedRows {
						granted[i] = provider.GrantedEntry{ID: r.ID, Name: r.Name}
					}
					_ = provider.ReconcileLabel(granted)
				}
				cb := welcome.Callbacks{
					KeyNames:            keyNames,
					ActiveProvider:      activeProv.String(),
					ActiveProviderLabel: provider.LoadLabel(),
					GrantedProviders:    grantedRows,
					OnSelect: func(p provider.Provider) error {
						return provider.Save(p)
					},
					OnSelectLabel: func(label string) error {
						return provider.SaveLabel(label)
					},
					OnAddKey: func(name, token string) error {
						return keystore.AddKey(name, token)
					},
					OnDeleteKey: func(name string) error {
						return keystore.DeleteKey(name)
					},
				}
				result, err := welcome.Run(state, cb)
				if err != nil {
					// welcome.Run already handled non-TTY fallback; ignore error here.
					_ = err
				}
				switch result {
				case welcome.RunDoctor:
					fmt.Println()
					if derr := runDoctor(); derr != nil {
						fmt.Fprintf(os.Stderr, "vc: doctor: %v\n", derr)
					}
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop // re-show menu
				case welcome.RunStatusline:
					fmt.Println()
					runInstallStatuslineMenu(os.Stdout)
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop // re-show menu
				case welcome.RunProfile:
					fmt.Println()
					{
						cfg := config.OSResolve()
						token, _, _ := auth.Load()
						client := &http.Client{Timeout: 10 * time.Second}
						openProfile(cfg.AuthHost, token, client, func(u string) {
							_ = browser.OpenURL(u, os.Stdout)
						})
					}
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop // re-show menu
				case welcome.RunLogin:
					if lerr := runLoginInteractive(); lerr != nil {
						fmt.Fprintf(os.Stderr, "vc: login failed: %v\n", lerr)
						os.Exit(1)
					}
					break menuLoop // after login → fall through to spawn
				case welcome.Quit:
					os.Exit(0)
				default: // SpawnClaude
					break menuLoop
				}
			}
		}
		// Fall through to Execute() which calls runSpawn via rootCmd.
	}

	Execute()
}

// gateDecision is the outcome of the bare-launch interactivity/auth gate.
type gateDecision int

const (
	// gateShowWelcome: interactive terminal — render the landing screen.
	gateShowWelcome gateDecision = iota
	// gateSpawn: non-TTY but logged in — skip welcome, spawn claude directly.
	gateSpawn
	// gateFailAuth: non-TTY and not logged in — cannot run login UI, fail fast.
	gateFailAuth
)

// decideGate picks the bare-launch path from interactivity + auth state.
// The welcome TUI requires a TTY stdin (it waits for a keypress); when stdin is
// not a terminal it must never be entered, or automation callers hang forever.
func decideGate(stdinTTY, loggedIn bool) gateDecision {
	if !stdinTTY {
		if loggedIn {
			return gateSpawn
		}
		return gateFailAuth
	}
	return gateShowWelcome
}

// resolveAuthState checks token presence and fetches /v1/vc/me for sub-days.
// Never fatal — on any error it returns a graceful degraded state.
func resolveAuthState() welcome.AuthState {
	token, err := auth.LoadAndMigrate()
	if err != nil {
		return welcome.AuthState{LoggedIn: false}
	}
	if token == "" {
		return welcome.AuthState{LoggedIn: false}
	}

	// Check 5-minute in-memory cache first.
	if meCacheResult != nil && time.Now().Before(meCacheExpiry) {
		return meResultToState(*meCacheResult)
	}

	cfg := config.OSResolve()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	me, err := auth.FetchMe(cfg.AuthHost, token, httpClient)
	if err != nil {
		// Auth host unreachable or token invalid — show degraded state.
		if err == auth.ErrNotLoggedIn {
			return welcome.AuthState{LoggedIn: false}
		}
		// Network error — still logged in; degrade gracefully.
		return welcome.AuthState{
			LoggedIn: true,
			Identity: "(unknown)",
		}
	}

	// Cache result for 5 minutes.
	meCacheResult = &me
	meCacheExpiry = time.Now().Add(5 * time.Minute)
	return meResultToState(me)
}

func meResultToState(me auth.MeResult) welcome.AuthState {
	identity := me.Email
	if identity == "" {
		identity = me.UserID
	}
	return welcome.AuthState{
		LoggedIn:   true,
		Identity:   identity,
		BalanceUsd: me.BalanceUsd, // nil when VCD-55 not yet deployed → degrade safely
	}
}

// openProfile mints a vc-web-session and opens the auto-login redeem URL in the
// browser. On ANY failure (empty token, mint error) it falls back to opening
// the bare ProfileURL so the button never dead-ends (VCD-80). The opener seam
// allows tests to capture the chosen URL without launching a browser.
func openProfile(authHost, token string, httpClient *http.Client, open func(string)) {
	token = strings.TrimSpace(token)
	if token != "" {
		if ws, err := auth.MintWebSession(authHost, token, httpClient); err == nil {
			open(browser.RedeemURL(ws.Token))
			return
		}
	}
	open(browser.ProfileURL)
}

// runSpawn is the default RunE for rootCmd — no sub-command means "launch claude".
func runSpawn(cmd *cobra.Command, args []string) error {
	// Pre-flight: verify claude CLI is reachable BEFORE doing any auth work.
	// vc is a wrapper over claude; if claude is absent nothing can work, and
	// we must surface a clear message rather than a raw cobra/exec error.
	if !claudebin.IsInstalled() {
		fmt.Fprintln(os.Stderr, claudebin.MissingMessage())
		os.Exit(127)
	}

	cfg := config.OSResolve()

	// Load token with legacy cv fallback: tries ~/.void-code/token first,
	// then falls back to ~/.claudev/token and silently migrates on success.
	token, _ := auth.LoadAndMigrate()

	// Pre-spawn auth gate: verify token before handing control to claude.
	// A missing or rejected token must surface a friendly message here — not a
	// raw 401 error buried inside the claude UI.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	me, reached, err := authGate(token, cfg.AuthHost, httpClient)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	// VCD-49 budget gate: only when reached + pct present (degrade-safe).
	// VCD-65: subscriptionGate removed — budgetGate is the sole client-side gate.
	// Hard-block at ≥100%; soft warn (print, still spawn) at 80–99%.
	// pct==nil = no budget / server absent → proceed without warning.
	if reached && me.Pct != nil {
		if d := budgetGate(me.Pct, nil); d.Block {
			fmt.Fprintln(os.Stderr, warnStyle.Render(d.Message))
			os.Exit(1)
		} else if d.Warn {
			fmt.Fprintln(os.Stderr, warnStyle.Render(d.Message))
		}
	}

	// Check and update @anthropic-ai/claude-code before spawning.
	// This prevents claude-code's own auto-update from failing inside the proxy.
	// Silent on network failures; only prints on actual update or hard error.
	launchCCUpdateCheck()

	// resolveCA still runs so the relay CA is fetched/cached as before
	// (FetchCA plumbing retained; cleanup is follow-up). The path is no longer
	// fed into the spawn env: CC now reaches the relay via ANTHROPIC_BASE_URL,
	// not a global HTTPS_PROXY, so NODE_EXTRA_CA_CERTS is no longer emitted.
	if _, err := resolveCA(cfg); err != nil {
		// Non-fatal: warn and continue.
		fmt.Fprintf(os.Stderr, "vc: warning: cannot resolve relay CA: %v\n", err)
	}

	active := provider.Load()
	env, err := buildSpawnEnv(active, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vc: %v\n  falling back to relay. Fix the provider in the Providers menu.\n", err)
		env = relay.BuildEnv(os.Environ(), cfg.RelayScheme, cfg.RelayHost, token)
	}

	// Pre-seed ~/.claude.json if absent so Claude Code skips first-run onboarding,
	// and mark the current working directory as a trusted folder. Folder trust is
	// load-bearing: CC does not load ~/.claude/settings.json (bypassPermissions,
	// hooks) until the working dir is trusted, so a fresh project folder otherwise
	// drops CC into `auto` mode and fires its safety classifier — a model sub-call
	// the relay can't serve ("<model> not accessible" when running a script). On
	// Windows the trust dialog also fails to persist upstream, so we always seed.
	if home, err := os.UserHomeDir(); err == nil {
		claudeJSON := filepath.Join(home, ".claude.json")
		if err := ccjson.EnsureDefaults(claudeJSON); err != nil {
			fmt.Fprintf(os.Stderr, "vc: warning: cannot pre-seed ~/.claude.json: %v\n", err)
		}
		if cwd, err := os.Getwd(); err == nil {
			if err := ccjson.EnsureFolderTrust(claudeJSON, ccjson.TrustKeys(cwd)...); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot pre-seed folder trust: %v\n", err)
			}
		}
	}

	// Permission posture: belt-and-suspenders so a tool call never triggers
	// Claude Code's server-side safety classifier (a model sub-call the relay
	// can't serve — it surfaces as "<model> not accessible" mid-task):
	//   1. bypassPermissions + skip-confirm — runs every tool with no prompt and
	//      no classifier WHEN the user stays in bypass mode.
	//   2. always-allow `vc hook` PreToolUse hook — short-circuits the classifier
	//      in EVERY mode (auto/acceptEdits/default), so even if the user cycles
	//      off bypass (shift+tab) a bash command is still allowed locally with
	//      ZERO model sub-call (VCD-70 always-allow; VCD-46 proved CC honors it
	//      in auto mode). The hook is the durable fix; #1 alone left `auto` mode
	//      hitting the flaky classifier. Both only load once the folder is
	//      trusted — hence the folder-trust pre-seed above.
	// ccSettingsPath, when non-empty, is a vc-owned settings file passed to claude
	// via --settings. It loads regardless of folder trust (unlike
	// ~/.claude/settings.json), so the always-allow hook + bypass posture it
	// carries take effect even in a fresh/untrusted folder — making `auto` mode
	// classifier-free everywhere. See ccsettings.WriteManagedSettings.
	var ccSettingsPath string
	if execPath, err := os.Executable(); err == nil {
		hookCmd := ccsettings.HookCmd(ccsettings.ForwardSlash(execPath))

		// FIX B (Path 3 — guard, not full removal): the always-allow PreToolUse
		// hook is belt-and-suspenders that only matters in `auto` mode (reachable
		// via shift+tab off bypass); native bypassPermissions does NOT cover that
		// residual case, so we keep the hook for ASCII + space-free exec paths.
		// But when execPath has non-ASCII (Cyrillic) or a space, CC's Windows spawn
		// of the hook command fails and spams a garbled CP1251 "hook failed" banner
		// on every tool call. There, seed NO hook and strip any prior one — the
		// user falls back to native bypass-only. The check is on the ORIGINAL
		// execPath (pre-ForwardSlash) so the space test sees real characters.
		hookSafe := ccsettings.PathHookSafe(execPath)

		settingsPath, pathErr := ccsettings.SettingsPath()
		if pathErr == nil {
			if err := ccsettings.EnsureAllowAllPermissions(settingsPath); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot set allow-all permissions: %v\n", err)
			}
			// FIX A: skip CC's hardcoded claude.ai WebFetch safety preflight so
			// relay users (who can't reach claude.ai) don't fail every WebFetch.
			if err := ccsettings.EnsureSkipWebFetchPreflight(settingsPath); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot set skipWebFetchPreflight: %v\n", err)
			}
			// Suppress the warn-level "claude.ai connectors are disabled because
			// ANTHROPIC_API_KEY ... takes precedence" nag for relay/BYO users.
			if err := ccsettings.EnsureDisableClaudeAiConnectors(settingsPath); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot set disableClaudeAiConnectors: %v\n", err)
			}
			if hookSafe {
				if err := ccsettings.EnsureHook(settingsPath, hookCmd); err != nil {
					fmt.Fprintf(os.Stderr, "vc: warning: cannot install always-allow hook: %v\n", err)
				}
			} else if err := ccsettings.RemoveHook(settingsPath); err != nil {
				// Non-ASCII/spaced path: strip any broken hook from a prior install.
				fmt.Fprintf(os.Stderr, "vc: warning: cannot remove stale hook: %v\n", err)
			}
			// Install the statusLine command (non-clobbering — leaves user's foreign statusLine untouched).
			slCmd := ccsettings.StatusLineCmd(ccsettings.ForwardSlash(execPath))
			if err := ccsettings.EnsureStatusLine(settingsPath, slCmd); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot install statusLine: %v\n", err)
			}
		}

		// Trust-independent layer: write vc's full posture (bypass defaults +
		// skip-prompt + skipWebFetchPreflight, and the always-allow hook only when
		// the exec path is hook-safe) to ~/.void-code/cc-settings.json and pass it
		// via --settings below. This is what guarantees `auto` mode never calls the
		// classifier even when ~/.claude/settings.json hasn't loaded.
		if cacheDir, cerr := config.CacheDir(); cerr == nil {
			p := filepath.Join(cacheDir, "cc-settings.json")
			if err := ccsettings.WriteManagedSettings(p, hookCmd, hookSafe); err != nil {
				fmt.Fprintf(os.Stderr, "vc: warning: cannot write managed CC settings: %v\n", err)
			} else {
				ccSettingsPath = p
			}
		}
	}

	// Build the claude argv. Two trust-independent layers (see permmode.go and
	// ccsettings.WriteManagedSettings):
	//   1. --permission-mode bypassPermissions → session STARTS in bypass (no
	//      classifier), unless the user picked a posture explicitly.
	//   2. --settings <cc-settings.json> → delivers the always-allow PreToolUse
	//      hook + skip-prompt regardless of folder trust, so if the user shift+tab's
	//      into `auto` mode every tool is still approved locally with ZERO model
	//      sub-call (no "<model> temporarily unavailable" classifier error).
	spawnArgs := ensureBypassPermissionMode(args)
	if ccSettingsPath != "" {
		spawnArgs = append([]string{"--settings", ccSettingsPath}, spawnArgs...)
	}

	if err := harness.Spawn(context.Background(), "claude", spawnArgs, env); err != nil {
		// Post-spawn not-found fallback (should be caught by pre-flight above,
		// but defend against race conditions such as claude being removed between
		// the pre-flight check and the actual spawn).
		if claudebin.IsNotFoundErr(err) {
			fmt.Fprintln(os.Stderr, claudebin.MissingMessage())
			os.Exit(127)
		}
		// Propagate claude's exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// buildSpawnEnv selects the claude env for the active provider.
//   - Relay         → relay.BuildEnv (ANTHROPIC_BASE_URL + pool token).
//   - RelayProvider → relay.BuildEnv + ANTHROPIC_CUSTOM_HEADERS=x-void-provider: <id>
//     (VCD-72: CC emits this header; void-relay (VRL-61) resolves credential + base_url)
//   - NamedKey      → direct.NamedKeyEnv with the saved OAuth token (relay bypassed).
//   - Plain         → direct.PlainEnv (native CC auth, no injection).
func buildSpawnEnv(p provider.Provider, parent []string, relayScheme, relayHost, token string) ([]string, error) {
	switch p.Kind {
	case provider.Plain:
		return direct.PlainEnv(parent), nil
	case provider.NamedKey:
		key, err := keystore.GetKey(p.Name)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}
		return direct.NamedKeyEnv(parent, key), nil
	case provider.RelayProvider:
		// Relay path + x-void-provider header so void-relay injects the right credential.
		// CC reads ANTHROPIC_CUSTOM_HEADERS (Name: Value, newline-separated) and emits
		// them on every Anthropic request. The credential never reaches the client.
		env := relay.BuildEnv(parent, relayScheme, relayHost, token)
		env = append(env, "ANTHROPIC_CUSTOM_HEADERS=x-void-provider: "+p.ID)
		return env, nil
	default: // Relay
		return relay.BuildEnv(parent, relayScheme, relayHost, token), nil
	}
}

// subscriptionDecision is the pure outcome of a spawn-gate check.
// VCD-65: subscriptionGate removed; this struct is kept because budgetGate returns it.
type subscriptionDecision struct {
	Block   bool   // true → do NOT spawn; print Message; exit non-zero
	Warn    bool   // true → spawn, but show Message as a soft banner warning
	Message string // user-facing copy (lipgloss styling applied by caller)
}

// budgetGate maps budget pct + budget_usd to a spawn decision (VCD-49).
//
//	pct == nil   → no budget / server absent → clean (degrade-safe)
//	pct < 80     → clean
//	80 <= pct < 100 → warn (spawn, show message)
//	pct >= 100   → hard block
//
// budgetUsd is used to format the block message; may be nil (falls back to generic copy).
func budgetGate(pct *float64, budgetUsd *float64) subscriptionDecision {
	if pct == nil {
		return subscriptionDecision{}
	}
	p := *pct
	switch {
	case p >= 100:
		// Operator constraint (2026-05-30): user-facing copy — percentages only, no dollar values.
		_ = budgetUsd // dollar amount intentionally not shown to user
		return subscriptionDecision{Block: true, Message: "Monthly budget reached — message @makscee on Telegram to top up."}
	case p >= 80:
		return subscriptionDecision{
			Warn:    true,
			Message: fmt.Sprintf("Budget at %.0f%% — top up via @makscee on Telegram before you hit the cap.", p),
		}
	default:
		return subscriptionDecision{}
	}
}

// authGate validates the session token before spawning claude.
//
// Rules:
//   - token absent → error (not logged in)
//   - token present, auth server returns 401 → error (token rejected)
//   - token present, server reachable → returns (me, true, nil)
//   - token present, network/server error → returns (zero, false, nil) — transient blip, do not block
//
// Returns reached=true only when the server responded successfully.
// VCD-65: SubDaysLeft removed; budgetGate uses reached to distinguish transient blip.
func authGate(token, authHost string, httpClient *http.Client) (auth.MeResult, bool, error) {
	if token == "" {
		return auth.MeResult{}, false, fmt.Errorf("Not logged in. Run `vc login` to authenticate (email, pairing code, or --code <ACCESS-CODE>).")
	}

	me, err := auth.FetchMe(authHost, token, httpClient)
	if err == nil {
		return me, true, nil
	}
	if err == auth.ErrNotLoggedIn {
		return auth.MeResult{}, false, fmt.Errorf("Session token rejected by auth server (likely expired or revoked).\nRun `vc login` to re-authenticate.")
	}
	// Network / server error — don't block; transient blip.
	return auth.MeResult{}, false, nil
}

// resolveCA determines the relay CA path in priority order:
//  1. VC_RELAY_CA env override (cfg.CAOverride).
//  2. Cached file at ~/.void-code/relay-ca.pem (FetchCA returns it if present,
//     fetches from <authHost>/vc/relay-ca.pem otherwise).
//  3. On network failure, write the embedded fallback CA to the cache dir
//     so first-run-offline always has a working CA.
func resolveCA(cfg config.Config) (string, error) {
	if cfg.CAOverride != "" {
		return cfg.CAOverride, nil
	}

	cacheDir, err := config.CacheDir()
	if err != nil {
		return writeFallbackCA("")
	}

	caPath, err := relay.FetchCA(http.DefaultClient, cfg.AuthHost, cacheDir)
	if err != nil {
		// Network unavailable or server error — fall back to embedded CA.
		return writeFallbackCA(cacheDir)
	}
	return caPath, nil
}

// writeFallbackCA writes the build-time-embedded relay-ca.pem to cacheDir
// (creating the directory as needed) and returns the path.
// If cacheDir is empty a temp file is used.
func writeFallbackCA(cacheDir string) (string, error) {
	if len(relayCA) == 0 {
		return "", fmt.Errorf("relay: embedded CA is empty")
	}

	var dest string
	if cacheDir == "" {
		f, err := os.CreateTemp("", "vc-relay-ca-*.pem")
		if err != nil {
			return "", fmt.Errorf("relay: temp CA: %w", err)
		}
		dest = f.Name()
		if _, err := f.Write(relayCA); err != nil {
			f.Close()
			return "", fmt.Errorf("relay: temp CA write: %w", err)
		}
		return dest, f.Close()
	}

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", fmt.Errorf("relay: mkdir cache: %w", err)
	}
	dest = filepath.Join(cacheDir, "relay-ca.pem")
	if err := os.WriteFile(dest, relayCA, 0600); err != nil {
		return "", fmt.Errorf("relay: write fallback CA: %w", err)
	}
	return dest, nil
}

// isFdTTY reports whether the given file descriptor refers to a terminal.
// Uses charmbracelet/x/term which handles platform differences (Unix + Windows).
func isFdTTY(fd int) bool {
	return term.IsTerminal(uintptr(fd))
}

// isStdinTTY reports whether os.Stdin is an interactive terminal.
// Returns false when stdin is a pipe, file, or /dev/null (automation/subagent context).
func isStdinTTY() bool {
	return isFdTTY(int(os.Stdin.Fd()))
}
