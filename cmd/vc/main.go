// Binary vc — void-code subscription console for Pi.
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
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	term "github.com/charmbracelet/x/term"
	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/harness/direct"
	"github.com/makscee/void-code/internal/harness/relay"
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
)

// welcomeGateSkippingSubCommands names the sub-commands that run instead of the
// landing screen. A command missing from here is not merely un-prettified: on a
// non-TTY stdin the gate fails the process before Cobra ever dispatches, so its
// caller reads "auth failed" on stderr and exit 1 where its own output was
// supposed to be.
//
// That is why access-request is on the list. The desktop spawns it precisely
// when nobody is signed in or nobody has been granted access — which is the
// exact condition the gate refuses on — and the whole point of the command is
// that those two cases arrive as a state on stdout the button can read.
//
// The list is a package-level var rather than a local so a test can pin it
// against the commands actually registered on rootCmd.
var welcomeGateSkippingSubCommands = map[string]bool{
	"login": true, "logout": true, "status": true, "update": true,
	"hook": true, "doctor": true, "statusline": true, "pi-bootstrap": true,
	"desktop-session": true, "access-request": true,
}

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

	// --raw: skip title screen and pass tty straight to Pi.
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
			break // everything after -- is for Pi
		}
	}

	// Persistent landing screen — shown on bare `vc` invocation (no sub-command).
	// Checks auth state, shows banner, waits for any keypress.
	// Any keypress → logged-in: spawn Pi; logged-out: run login.
	// Skipped for sub-commands (login/logout/status/update) so automation works.
	// Skipped when --raw is set (jump straight to spawn, no TUI).
	hasSubCmd := len(os.Args) > 1 && welcomeGateSkippingSubCommands[os.Args[1]]
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
		menuLoop:
			for {
				if nudge, ready := currentLaunchPreflight.updateIfReady(); ready && nudge != "" {
					state.UpdateNudge = nudge
				}
				result, err := runWelcomeCommandTransition(state, welcome.Callbacks{}, rootCmd, os.Args[1:])
				if result == welcome.SpawnPi {
					if err != nil {
						handleExecuteError(err)
					}
					return
				}
				switch result {
				case welcome.RunDoctor:
					fmt.Println()
					if derr := runDoctor(); derr != nil {
						fmt.Fprintf(os.Stderr, "vc: doctor: %v\n", derr)
					}
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop
				case welcome.RunProfile:
					cfg := config.OSResolve()
					token, _, _ := auth.Load()
					openProfile(cfg.AuthHost, token, &http.Client{Timeout: 10 * time.Second}, func(u string) { _ = browser.OpenURL(u, os.Stdout) })
					fmt.Println("\n  press enter to return to the menu…")
					bufio.NewScanner(os.Stdin).Scan()
					continue menuLoop
				case welcome.RunLogin:
					if lerr := runLoginInteractive(); lerr != nil {
						fmt.Fprintf(os.Stderr, "vc: login failed: %v\n", lerr)
						os.Exit(1)
					}
					state, token, authHost, currentLaunchPreflight = refreshLaunchAfterLogin(defaultLaunchPreflightDeps())
					_ = token
					_ = authHost
					continue menuLoop
				case welcome.Quit:
					os.Exit(0)
				default:
					_ = err
					continue menuLoop
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
	// gateSpawn: non-TTY but logged in — skip welcome and spawn Pi directly.
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

// The host returned here is handed straight to startLaunchPreflight, which
// passes it to authGate — so it is the access check's host, not sign-in's, in
// both branches. A token-less launch returns it too: the preflight is started
// either way, and a host that changes after login would make the started
// preflight unreusable.
func resolveLocalAuthStateWithSource() (welcome.AuthState, string, string, launchSource) {
	token, _, err := auth.Load()
	cfg := config.OSResolve()
	if err != nil || strings.TrimSpace(token) == "" {
		return welcome.AuthState{LoggedIn: false}, token, cfg.AccessCheckHost, sourceLocal
	}
	return welcome.AuthState{LoggedIn: true, IdentityUnverified: true}, token, cfg.AccessCheckHost, sourceLocal
}

// resolveAuthState checks token presence and fetches /v1/vc/me for sub-days.
// Never fatal — on any error it returns a graceful degraded state.
func resolveAuthState() welcome.AuthState {
	token, _, err := auth.Load()
	if err != nil || strings.TrimSpace(token) == "" {
		return welcome.AuthState{LoggedIn: false}
	}
	me, err := auth.FetchMe(config.OSResolve().AccessCheckHost, token, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return welcome.AuthState{LoggedIn: false, IdentityUnverified: true}
	}
	return meResultToState(me)
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
// existing dispatch; the Pi spawn executes Cobra so parsing and error behavior
// remain identical to every other root invocation.
func runWelcomeCommandTransition(state welcome.AuthState, cb welcome.Callbacks, cmd *cobra.Command, args []string) (welcome.RunResult, error) {
	result, err := runWelcomeScreen(state, cb)
	currentLaunchDiagnostics.record(phaseSelection, outcomeComplete, sourceLocal)
	if result != welcome.SpawnPi {
		return result, err
	}
	// Preserve the existing fallback: a welcome error with the spawn default
	// still proceeds through Cobra rather than replacing Cobra's error result.
	cmd.SetArgs(args)
	return result, cmd.Execute()
}

// runSpawn is the default RunE for rootCmd — no sub-command launches Pi.
func runSpawn(_ *cobra.Command, args []string) error {
	cfg := config.OSResolve()
	token, _, _ := auth.Load()

	// Admission is always live: cached identity and budget are only display hints,
	// never permission to start a paid session. The question is the access check
	// — who the token belongs to and whether they are let in — so it goes to the
	// access-check host, the same one the desktop session gate asks.
	me, reached, err := authGate(token, cfg.AccessCheckHost, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeRejected, sourceRejected)
		currentLaunchDiagnostics.flush()
		fmt.Fprintln(os.Stderr, err)
		exitProcess(1)
		return err
	}
	if reached && me.Pct != nil {
		if d := budgetGate(me.Pct, nil); d.Block {
			fmt.Fprintln(os.Stderr, warnStyle.Render(d.Message))
			exitProcess(1)
			return errors.New(d.Message)
		} else if d.Warn {
			fmt.Fprintln(os.Stderr, warnStyle.Render(d.Message))
		}
	}
	// Resolve the VC-managed entrypoint exactly once, after live admission and
	// before constructing token-bearing child environment. Never substitute a
	// PATH result here: that binary would inherit VC_AUTH_TOKEN.
	piPath, err := pibin.Resolve()
	if err != nil {
		return fmt.Errorf("%s: %w", pibin.MissingMessage(), err)
	}
	extPath, extErr := reconcileManagedPiExtension()
	if extErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: managed Pi provider was not reconciled: %v\n", extErr)
	}
	if _, webErr := reconcileManagedWebSearch(true); webErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: managed Pi web search was not reconciled: %v\n", webErr)
	}
	// A seeded default is a convenience, never a precondition: an unreadable or
	// hand-broken settings.json must still let Pi start.
	if modelErr := ensurePiDefaultModel(); modelErr != nil {
		fmt.Fprintf(os.Stderr, "vc: warning: Pi default model was not seeded: %v\n", modelErr)
	}
	caPath, err := resolveCA(cfg)
	if err != nil {
		return fmt.Errorf("cannot resolve relay CA (required for proxy TLS): %w", err)
	}
	if extPath == "" {
		extPath, err = ensurePiVoidCodexExtension()
		if err != nil {
			return fmt.Errorf("cannot write Pi relay extension: %w", err)
		}
	}
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
	currentLaunchDiagnostics.record(phaseSpawnHandoff, outcomeComplete, sourceLocal)
	currentLaunchDiagnostics.flush()
	return spawnHarness(context.Background(), piPath, buildPiArgs(nil, extPath), env)
}

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

// buildPiArgs adds only VC's transport extension. Pi receives all model and
// provider choice through its own native configuration and UI.
func buildPiArgs(args []string, extensionPath string) []string {
	out := make([]string, 0, len(args)+2)
	if extensionPath != "" {
		out = append(out, "-e", extensionPath)
	}
	return append(out, args...)
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
		"VC_BOOTSTRAP_EXECUTABLE":  true,
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
	// Do not let inherited PATH choose the authority that serves credentials to
	// the extension. os.Executable is the already-running VC binary.
	if executable, err := os.Executable(); err == nil && filepath.IsAbs(executable) {
		out = append(out, "VC_BOOTSTRAP_EXECUTABLE="+executable)
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

// authGate validates the session token before spawning Pi.
//
// Rules:
//   - token absent → error (not logged in)
//   - token present, auth server returns 401 → error (token rejected)
//   - token present, access refused (402) → auth.ErrAccessNotGranted, unwrapped
//   - token present, server reachable → returns (me, true, nil)
//   - token present, network/server error → error — admission cannot be authoritative
//
// Returns reached=true only when the server responded successfully.
func authGate(token, authHost string, httpClient *http.Client) (auth.MeResult, bool, error) {
	if token == "" {
		return auth.MeResult{}, false, fmt.Errorf("Not logged in. Run `vc login` to authenticate (email, pairing code, or --code <ACCESS-CODE>).")
	}

	me, err := auth.FetchMe(authHost, token, httpClient)
	if err == nil {
		return me, true, nil
	}
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return auth.MeResult{}, false, fmt.Errorf("Session token rejected by auth server (likely expired or revoked).\nRun `vc login` to re-authenticate.")
	}
	// A refusal is not a failed check. Neither neighbour fits it: the credential
	// worked, so sending the human back to sign-in cannot help, and the check was
	// not unavailable — it ran and answered, so repeating it changes nothing.
	// The sentinel travels unwrapped so callers branch on the outcome instead of
	// on prose, and so the wording stays the one runStatusJSON already reports.
	if errors.Is(err, auth.ErrAccessNotGranted) {
		return auth.MeResult{}, false, err
	}
	return auth.MeResult{}, false, fmt.Errorf("Session verification unavailable; try again: %w", err)
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
