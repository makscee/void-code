// Binary vc — void-code relay harness for Claude Code and Pi.
//
// Version is injected at build time:
//
//	go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=v0.0.1" ./cmd/vc
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	term "github.com/charmbracelet/x/term"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/ccjson"
	"github.com/makscee/void-code/internal/ccsettings"
	"github.com/makscee/void-code/internal/claudebin"
	"github.com/makscee/void-code/internal/codexbin"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/harness/direct"
	"github.com/makscee/void-code/internal/harness/relay"
	"github.com/makscee/void-code/internal/harnesschoice"
	"github.com/makscee/void-code/internal/keystore"
	"github.com/makscee/void-code/internal/pibin"
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

	spawnHarness             = harness.Spawn
	exitProcess              = os.Exit
	currentLaunchDiagnostics = newLaunchDiagnostics(false, time.Now, io.Discard)
	claudeIsInstalled        = claudebin.IsInstalled
	codexIsInstalled         = codexbin.IsInstalled
	piIsInstalled            = pibin.IsInstalled
)

func main() {
	// Desktop owns a private runtime and performs no vc update work, including
	// Windows cleanup left by an ordinary self-update.
	if shouldCleanOldBinary(os.Args) {
		update.CleanOldBinary()
	}

	// --version short-circuit before Cobra parses anything.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("vc %s\n", version.Version)
			return
		}
	}

	// --raw: skip title/menu screen and pass tty straight to the active harness.
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
			break // everything after -- is for the active harness
		}
	}

	// Persistent landing screen — shown on bare `vc` invocation (no sub-command).
	// Checks auth state, shows banner, waits for any keypress.
	// Any keypress → logged-in: spawn active harness; logged-out: run login.
	// Skipped for sub-commands (login/logout/status/update) so automation works.
	// Skipped when --raw is set (jump straight to spawn, no TUI).
	subCmds := map[string]bool{"login": true, "logout": true, "status": true, "update": true, "hook": true, "doctor": true, "statusline": true, "pi-bootstrap": true, "desktop-session": true}
	hasSubCmd := len(os.Args) > 1 && subCmds[os.Args[1]]
	if !hasSubCmd && !hasRaw {
		currentLaunchDiagnostics = newLaunchDiagnosticsFromEnv(time.Now, os.Stderr)
		state, token, authHost, localSource := resolveLocalAuthStateWithSource()
		currentLaunchDiagnostics.record(phaseLocalStateLoad, outcomeComplete, localSource)
		currentLaunchPreflight = startLaunchPreflight(token, authHost, true, defaultLaunchPreflightDeps())
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
			// Provider discovery gates only the first render, up to the existing
			// bounded probe deadline, so newly issued grants are visible immediately.
			firstRender := true
		menuLoop:
			for {
				keyNames, _ := keystore.ListKeys()
				activeProv := provider.Load()
				activeLabel := provider.LoadLabel()
				activeHarness := harnesschoice.Load()
				var grantedRows []welcome.ProviderRowInfo
				providerResult, providerReady := currentLaunchPreflight.providerForRender(token, authHost, firstRender)
				firstRender = false
				if result := providerResult; providerReady && result.err == nil {
					grantedRows = result.rows
					granted := make([]provider.GrantedEntry, len(grantedRows))
					for i, r := range grantedRows {
						granted[i] = provider.GrantedEntry{ID: r.ID, Name: r.Name}
					}
					_ = provider.ReconcileLabel(granted)
					activeLabel = provider.LoadLabel()
					if d := compat.Reconcile(activeHarness, activeProv, activeLabel, result.grants); d.Changed {
						_ = harnesschoice.Save(d.Harness)
						_ = provider.Save(d.Provider)
						_ = provider.SaveLabel(d.ProviderLabel)
						activeHarness, activeProv, activeLabel = d.Harness, d.Provider, d.ProviderLabel
						if d.Warning != "" {
							fmt.Fprintln(os.Stderr, "vc: "+d.Warning)
						}
					}
				}
				if nudge, ready := currentLaunchPreflight.updateIfReady(); ready && nudge != "" {
					state.UpdateNudge = nudge
				}
				cb := welcome.Callbacks{
					KeyNames:            keyNames,
					ActiveProvider:      activeProv.String(),
					ActiveProviderLabel: activeLabel,
					GrantedProviders:    grantedRows,
					ActiveHarness:       activeHarness.String(),
					ActiveHarnessLabel:  activeHarness.Label(),
					ClaudeInstalled:     claudeIsInstalled(),
					CodexInstalled:      codexIsInstalled(),
					PiInstalled:         piIsInstalled(),
					OnSelectHarness: func(h harnesschoice.Choice) error {
						return harnesschoice.Save(h)
					},
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
				result, err := runWelcomeCommandTransition(state, cb, rootCmd, os.Args[1:])
				if result == welcome.SpawnClaude {
					if err != nil {
						handleExecuteError(err)
					}
					return // root RunE already spawned the selected harness exactly once
				}
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
				case welcome.RunInstallPi:
					fmt.Println()
					runInstallPi(os.Stdout)
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop // re-show menu
				case welcome.RunInstallClaude:
					fmt.Println()
					runInstallClaude(os.Stdout)
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop // re-show menu
				case welcome.RunInstallCodex:
					fmt.Println()
					runInstallCodex(os.Stdout)
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
					// Login replaces the credential, so the logged-out preflight cannot
					// be reused. Reload all auth inputs and gate the next render on one
					// fresh, bounded provider probe for the newly issued token.
					state, token, authHost, currentLaunchPreflight = refreshLaunchAfterLogin(defaultLaunchPreflightDeps())
					firstRender = true
					continue menuLoop
				case welcome.Quit:
					os.Exit(0)
				default: // SpawnClaude
					break menuLoop
				}
			}
		}
		// Non-interactive and post-login paths fall through to Cobra execution.
	}

	Execute()
}

func shouldCleanOldBinary(args []string) bool {
	return len(args) < 2 || args[1] != "desktop-session"
}

// gateDecision is the outcome of the bare-launch interactivity/auth gate.
type gateDecision int

const (
	// gateShowWelcome: interactive terminal — render the landing screen.
	gateShowWelcome gateDecision = iota
	// gateSpawn: non-TTY but logged in — skip welcome, spawn active harness directly.
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

// resolveLocalAuthState reads only local token/cache state so the welcome screen
// can render before any optional network request completes.
func resolveLocalAuthState() (welcome.AuthState, string, string) {
	state, token, authHost, _ := resolveLocalAuthStateWithSource()
	return state, token, authHost
}

func refreshLaunchAfterLogin(deps launchPreflightDeps) (welcome.AuthState, string, string, *launchPreflight) {
	state, token, authHost, source := resolveLocalAuthStateWithSource()
	deps.diagnostics.record(phaseLocalStateLoad, outcomeComplete, source)
	return state, token, authHost, startLaunchPreflight(token, authHost, false, deps)
}

func resolveLocalAuthStateWithSource() (welcome.AuthState, string, string, launchSource) {
	token, err := auth.LoadAndMigrate()
	cfg := config.OSResolve()
	if err != nil || token == "" {
		return welcome.AuthState{LoggedIn: false}, token, cfg.AuthHost, sourceLocal
	}
	if cached, ok := readMeCache(cfg.AuthHost, token, time.Now()); ok {
		if cached.Stale {
			return staleMeResultToState(cached.Me), token, cfg.AuthHost, sourceStale
		}
		return meResultToState(cached.Me), token, cfg.AuthHost, sourceFresh
	}
	// Token presence is sufficient for the local landing screen. The in-flight
	// admission probe remains authoritative for legacy credentials at Start.
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true}, token, cfg.AuthHost, sourceLocal
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
	httpClient := &http.Client{Timeout: authProbeTimeout}
	cached, err := cachedFetchMeState(cfg.AuthHost, token, httpClient)
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			return welcome.AuthState{LoggedIn: false}
		}
		return staleMeResultToState(cached.Me)
	}

	// Cache result for 5 minutes.
	meCacheResult = &cached.Me
	meCacheExpiry = time.Now().Add(5 * time.Minute)
	return meResultToState(cached.Me)
}

func staleMeResultToState(me auth.MeResult) welcome.AuthState {
	identity := me.Email
	if identity == "" {
		identity = me.UserID
	}
	return welcome.AuthState{
		LoggedIn:           true,
		Identity:           identity,
		IdentityUnverified: true,
	}
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

func fetchCompatGrants(authHost, token string) ([]compat.Grant, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	infos, err := fetchProvidersLive(authHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return nil, err
	}
	grants := make([]compat.Grant, 0, len(infos))
	for _, pi := range infos {
		grants = append(grants, compat.Grant{ID: pi.ID, Name: pi.Name, Type: pi.Type})
	}
	return grants, nil
}

var welcomeProgramOptions []tea.ProgramOption

func runWelcomeScreen(state welcome.AuthState, cb welcome.Callbacks) (welcome.RunResult, error) {
	opts := welcomeProgramOptions
	if currentLaunchDiagnostics != nil && currentLaunchDiagnostics.enabled {
		opts = append(append([]tea.ProgramOption{}, opts...), tea.WithOutput(&firstRenderDiagnosticWriter{out: os.Stdout, diagnostics: currentLaunchDiagnostics}))
	}
	return welcome.RunWithOptions(state, cb, opts...)
}

type firstRenderDiagnosticWriter struct {
	out         io.Writer
	diagnostics *launchDiagnostics
	once        sync.Once
}

func (w *firstRenderDiagnosticWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { w.diagnostics.record(phaseFirstRender, outcomeComplete, sourceLocal) })
	return w.out.Write(p)
}

// runWelcomeCommandTransition is the bare-launch boundary from the production
// welcome program into Cobra. Non-spawn choices are returned to main for their
// existing dispatch; SpawnClaude executes Cobra so parsing and error behavior
// remain identical to every other root invocation.
func runWelcomeCommandTransition(state welcome.AuthState, cb welcome.Callbacks, cmd *cobra.Command, args []string) (welcome.RunResult, error) {
	result, err := runWelcomeScreen(state, cb)
	currentLaunchDiagnostics.record(phaseSelection, outcomeComplete, sourceLocal)
	if result != welcome.SpawnClaude {
		return result, err
	}
	// Preserve the existing fallback: a welcome error with the spawn default
	// still proceeds through Cobra rather than replacing Cobra's error result.
	cmd.SetArgs(args)
	return result, cmd.Execute()
}

func awaitSpawnAdmission(p *launchPreflight, token, authHost string) (auth.MeResult, bool, error) {
	if carriedMe, carriedReached, carriedErr, reused := p.awaitAuth(token, authHost); reused {
		return carriedMe, carriedReached, carriedErr
	}
	return authGate(token, authHost, &http.Client{Timeout: authProbeTimeout})
}

func spawnProviderOutcome(p *launchPreflight, token, authHost string) launchProviderResult {
	if p != nil {
		if carried, reused := p.awaitProvider(token, authHost); reused {
			return carried
		}
	}
	grants, err := fetchCompatGrants(authHost, token)
	if err != nil {
		return launchProviderResult{err: err}
	}
	return launchProviderResult{kind: providerOutcomeSuccess, grants: grants}
}

// runSpawn is the default RunE for rootCmd — no sub-command means "launch active harness".
func runSpawn(cmd *cobra.Command, args []string) error {
	activeHarness := harnesschoice.Load()

	cfg := config.OSResolve()

	// Load token with legacy cv fallback: tries ~/.void-code/token first,
	// then falls back to ~/.claudev/token and silently migrates on success.
	token, _ := auth.LoadAndMigrate()

	// Pre-spawn auth gate: verify token before handing control to the active harness.
	// A missing or rejected token must surface a friendly message here — not a
	// raw 401 error buried inside the harness UI.
	me, reached, err := awaitSpawnAdmission(currentLaunchPreflight, token, cfg.AuthHost)
	if err != nil {
		currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeRejected, sourceRejected)
		currentLaunchDiagnostics.flush()
		fmt.Fprintln(os.Stderr, err.Error())
		exitProcess(1)
		return err
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

	active := provider.Load()
	activeLabel := provider.LoadLabel()
	providerOutcome := spawnProviderOutcome(currentLaunchPreflight, token, cfg.AuthHost)
	compatGrants := providerOutcome.grants
	if providerOutcome.err != nil && activeHarness.Kind == harnesschoice.Pi {
		fmt.Fprintf(os.Stderr, "vc: warning: managed Pi web search unavailable: provider discovery failed: %v\n", providerOutcome.err)
	}
	// Unknown/error outcomes preserve the durable selection exactly. Only a
	// successful current response (including confirmed empty) is authoritative.
	if providerOutcome.successful() {
		if d := compat.Reconcile(activeHarness, active, activeLabel, compatGrants); d.Changed {
			_ = harnesschoice.Save(d.Harness)
			_ = provider.Save(d.Provider)
			_ = provider.SaveLabel(d.ProviderLabel)
			activeHarness, active, activeLabel = d.Harness, d.Provider, d.ProviderLabel
			if d.Warning != "" {
				fmt.Fprintln(os.Stderr, "vc: "+d.Warning)
			}
		}
	}

	managedPiPath, managedPiErr := reconcileManagedPiExtension()
	if managedPiErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: managed Pi provider was not reconciled: %v\n", managedPiErr)
	}
	activeGrantClass, exactActiveGrant := compat.ExactGrantClass(active, compatGrants)
	webEligible := providerOutcome.successful() && activeHarness.Kind == harnesschoice.Pi && exactActiveGrant && activeGrantClass == compat.ProviderChatGPT
	if _, webErr := reconcileManagedWebSearch(webEligible); webErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: managed Pi web search was not reconciled: %v\n", webErr)
	}

	if err := ensureSelectedHarnessInstalled(activeHarness); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(127)
	}

	// Check and update @anthropic-ai/claude-code before spawning Claude Code.
	// This prevents claude-code's own auto-update from failing inside the proxy.
	// Silent on network failures; only prints on actual update or hard error.
	if activeHarness.Kind == harnesschoice.Claude {
		launchCCUpdateCheck()
	}

	// Resolve the relay CA: NODE_EXTRA_CA_CERTS must point at it so CC trusts the
	// relay's MITM proxy TLS. resolveCA falls back to the embedded CA on network
	// failure, so an error here means no CA at all — fatal, because the HTTPS_PROXY
	// transport cannot validate the proxy without it.
	caPath, err := resolveCA(cfg)
	if err != nil {
		return fmt.Errorf("cannot resolve relay CA (required for proxy TLS): %w", err)
	}

	var env []string
	if activeHarness.Kind == harnesschoice.Pi {
		env = buildPiSpawnEnv(active, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
		extPath := ""
		if managedPiPath != "" && hasPiFlag(args, "--no-extensions") {
			// Explicitly requested extension isolation still keeps vc's provider,
			// matching the historical vc --raw contract.
			extPath = managedPiPath
		}
		if managedPiPath == "" {
			// Opt-out, conflict, or permissions failure must not break wrapped vc.
			extPath, err = ensurePiVoidCodexExtension()
			if err != nil {
				return fmt.Errorf("cannot write Pi relay extension: %w", err)
			}
		}
		switch compat.ClassifyProvider(active, activeLabel, compatGrants) {
		case compat.ProviderChatGPT:
			env = append(env, "VC_PI_PROVIDER_KIND=codex")
			args = buildPiVoidCodexArgs(args, extPath)
		case compat.ProviderDeepSeek:
			env = append(env, "VC_PI_PROVIDER_KIND=deepseek")
			args = buildPiVoidDeepSeekArgs(args, extPath)
		}
		return spawnSelectedHarness(activeHarness, args, env)
	}
	if activeHarness.Kind == harnesschoice.Codex {
		env = buildCodexSpawnEnv(active, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
		spawnArgs := buildCodexArgs(args, cfg.RelayScheme, cfg.RelayHost)
		return spawnSelectedHarness(activeHarness, spawnArgs, env)
	}
	env, err = buildSpawnEnv(active, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vc: %v\n  falling back to relay. Fix the provider in the Providers menu.\n", err)
		env = relay.BuildEnv(os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
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

	return spawnSelectedHarness(activeHarness, spawnArgs, env)
}

func wrappedBinaryFor(h harnesschoice.Choice) string {
	switch h.Kind {
	case harnesschoice.Claude:
		return "claude"
	case harnesschoice.Codex:
		return "codex"
	default:
		return "pi"
	}
}

func ensureSelectedHarnessInstalled(h harnesschoice.Choice) error {
	switch h.Kind {
	case harnesschoice.Claude:
		if !claudeIsInstalled() {
			return fmt.Errorf("%s", claudebin.MissingMessage())
		}
	case harnesschoice.Codex:
		if !codexIsInstalled() {
			return fmt.Errorf("%s", codexbin.MissingMessage())
		}
	default:
		if !piIsInstalled() {
			return fmt.Errorf("%s", pibin.MissingMessage())
		}
	}
	return nil
}

func spawnSelectedHarness(h harnesschoice.Choice, args []string, env []string) error {
	currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	currentLaunchDiagnostics.flush()
	wrapped := wrappedBinaryFor(h)
	if err := spawnHarness(context.Background(), wrapped, args, env); err != nil {
		// Post-spawn not-found fallback (should be caught by pre-flight above,
		// but defend against race conditions such as the harness being removed
		// between the pre-flight check and the actual spawn).
		if claudebin.IsNotFoundErr(err) {
			switch h.Kind {
			case harnesschoice.Claude:
				fmt.Fprintln(os.Stderr, claudebin.MissingMessage())
			case harnesschoice.Codex:
				fmt.Fprintln(os.Stderr, codexbin.MissingMessage())
			default:
				fmt.Fprintln(os.Stderr, pibin.MissingMessage())
			}
			os.Exit(127)
		}
		// Propagate the harness exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

func runInstallPi(out io.Writer) {
	fmt.Fprintln(out, pibin.InstallInstructions())
}

func runInstallClaude(out io.Writer) {
	fmt.Fprintln(out, claudebin.InstallInstructions())
}

func runInstallCodex(out io.Writer) {
	fmt.Fprintln(out, codexbin.InstallInstructions())
}

const (
	piVoidCodexProvider        = "void-codex"
	piVoidCodexDefaultModel    = "gpt-5.6-terra"
	piVoidDeepSeekProvider     = "void-deepseek"
	piVoidDeepSeekDefaultModel = "deepseek/deepseek-v4-pro"
	codexDefaultModel          = "gpt-5.6-terra"
)

var (
	piVoidCodexModels = []string{
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	}
	piVoidDeepSeekModels = []string{
		"deepseek/deepseek-v4-pro",
		"deepseek/deepseek-v4-flash",
	}
)

func buildPiVoidCodexArgs(args []string, extensionPath string) []string {
	model := resolvePiManagedModel(piVoidCodexDefaultModel, piVoidCodexModels)
	return buildPiRelayArgs(args, extensionPath, piVoidCodexProvider, model)
}

func buildPiVoidDeepSeekArgs(args []string, extensionPath string) []string {
	model := resolvePiManagedModel(piVoidDeepSeekDefaultModel, piVoidDeepSeekModels)
	return buildPiRelayArgs(args, extensionPath, piVoidDeepSeekProvider, model)
}

func buildPiRelayArgs(args []string, extensionPath, providerID, modelID string) []string {
	out := make([]string, 0, len(args)+6)
	if extensionPath != "" {
		out = append(out, "-e", extensionPath)
	}
	if !hasPiFlag(args, "--provider") {
		out = append(out, "--provider", providerID)
	}
	if !hasPiFlag(args, "--model") {
		out = append(out, "--model", modelID)
	}
	out = append(out, args...)
	return out
}

func hasPiFlag(args []string, name string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func ensurePiVoidCodexExtension() (string, error) {
	dir, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	extDir := filepath.Join(dir, "pi-void-codex")
	if err := os.MkdirAll(extDir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(extDir, "index.ts")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == piVoidCodexExtensionSource {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// buildSpawnEnv selects the claude env for the active provider.
//   - Relay         → relay.BuildEnv (HTTPS_PROXY + relay CA + pool token).
//   - RelayProvider → relay.BuildEnv + ANTHROPIC_CUSTOM_HEADERS=x-void-provider: <id>
//     (VCD-72: CC emits this header; void-relay (VRL-61) resolves credential + base_url)
//   - NamedKey      → direct.NamedKeyEnv with the saved OAuth token (relay bypassed).
//   - Plain         → direct.PlainEnv (native CC auth, no injection).
func buildSpawnEnv(p provider.Provider, parent []string, relayScheme, relayHost, token, caPath string) ([]string, error) {
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
		env := relay.BuildEnv(parent, relayScheme, relayHost, token, caPath)
		env = append(env, "ANTHROPIC_CUSTOM_HEADERS=x-void-provider: "+p.ID)
		return env, nil
	default: // Relay
		return relay.BuildEnv(parent, relayScheme, relayHost, token, caPath), nil
	}
}

func buildCodexArgs(args []string, relayScheme, relayHost string) []string {
	baseURL := fmt.Sprintf("%s://%s/codex", relayScheme, relayHost)
	prefix := []string{
		"-c", "model_provider=void",
		"-c", "model_providers.void.name=Void relay",
		"-c", "model_providers.void.base_url=" + baseURL,
		"-c", "model_providers.void.wire_api=responses",
		"-c", "model_providers.void.env_key=VC_AUTH_TOKEN",
		"-c", "model_providers.void.env_http_headers.x-void-provider=VC_RELAY_PROVIDER_ID",
		"-c", "model=" + codexDefaultModel,
	}
	out := make([]string, 0, len(prefix)+len(args))
	out = append(out, prefix...)
	out = append(out, args...)
	return out
}

func buildCodexSpawnEnv(p provider.Provider, parent []string, relayScheme, relayHost, token, caPath string) []string {
	strip := map[string]bool{
		"OPENAI_API_KEY":       true,
		"OPENAI_BASE_URL":      true,
		"OPENAI_ORG_ID":        true,
		"AZURE_OPENAI_API_KEY": true,
		"CHATGPT_ACCESS_TOKEN": true,
		"CHATGPT_ACCOUNT_ID":   true,
		"CHATGPT_API_KEY":      true,
		"CODEX_API_KEY":        true,
		"VC_AUTH_TOKEN":        true,
		"VC_RELAY_PROVIDER_ID": true,
		"VC_RELAY_URL":         true,
		"VC_RELAY_CA":          true,
	}
	base := direct.PlainEnv(parent)
	out := make([]string, 0, len(base)+6)
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if strip[k] {
			continue
		}
		out = append(out, e)
	}
	out = append(out,
		"VC_HARNESS=codex",
		"VC_PROVIDER=relay",
		fmt.Sprintf("VC_RELAY_URL=%s://%s", relayScheme, relayHost),
		"VC_RELAY_CA="+caPath,
		"VC_AUTH_TOKEN="+token,
		"VC_RELAY_PROVIDER_ID="+p.ID,
	)
	return out
}

// buildPiSpawnEnv strips client-provider secrets and exposes only vc-owned relay
// seams for Pi relay modes.
func buildPiSpawnEnv(p provider.Provider, parent []string, relayScheme, relayHost, token, caPath string) []string {
	strip := map[string]bool{
		"VC_HARNESS":               true,
		"VC_PROVIDER":              true,
		"VC_RELAY_PROVIDER_ID":     true,
		"VC_RELAY_URL":             true,
		"VC_RELAY_CA":              true,
		"VC_AUTH_TOKEN":            true,
		"ANTHROPIC_CUSTOM_HEADERS": true,
	}
	if p.Kind == provider.Relay || p.Kind == provider.RelayProvider {
		for _, k := range []string{
			"OPENAI_API_KEY",
			"OPENAI_BASE_URL",
			"OPENAI_ORG_ID",
			"AZURE_OPENAI_API_KEY",
			"CHATGPT_ACCESS_TOKEN",
			"CHATGPT_ACCOUNT_ID",
			"CHATGPT_API_KEY",
			"CODEX_API_KEY",
		} {
			strip[k] = true
		}
	}
	base := direct.PlainEnv(parent)
	out := make([]string, 0, len(base)+6)
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if strip[k] {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "VC_HARNESS=pi")
	switch p.Kind {
	case provider.RelayProvider:
		out = append(out,
			"VC_PROVIDER=relay",
			"VC_RELAY_PROVIDER_ID="+p.ID,
			fmt.Sprintf("VC_RELAY_URL=%s://%s", relayScheme, relayHost),
			"VC_RELAY_CA="+caPath,
			"VC_AUTH_TOKEN="+token,
		)
	case provider.Relay:
		out = append(out,
			"VC_PROVIDER=relay",
			"VC_RELAY_PROVIDER_ID=deepseek",
			fmt.Sprintf("VC_RELAY_URL=%s://%s", relayScheme, relayHost),
			"VC_RELAY_CA="+caPath,
			"VC_AUTH_TOKEN="+token,
		)
	default:
		out = append(out, "VC_PROVIDER=plain")
	}
	return out
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

	me, err := cachedFetchMe(authHost, token, httpClient)
	if err == nil {
		return me, true, nil
	}
	if err == auth.ErrNotLoggedIn {
		// Identity device credentials are opaque <session>.<secret> values. During
		// VI-12, /v1/vc/me remains a legacy-auth budget endpoint and cannot
		// validate them; the relay performs authoritative identity introspection
		// before serving any request. Do not reject a valid identity credential at
		// this obsolete preflight. Legacy credentials remain fail-closed here.
		if isIdentityToken(token) {
			return auth.MeResult{}, false, nil
		}
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
