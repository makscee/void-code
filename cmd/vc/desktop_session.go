package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
	"github.com/spf13/cobra"
)

type desktopSessionPlan struct {
	nodePath  string
	args, env []string
	// Everything the preparation wants the user to see but was not willing to
	// fail over. It travels on the plan because the stream to say it on belongs
	// to the command, not to the preparation: the desktop app reads the
	// command's error stream, and a line written to os.Stderr here would land in
	// a process nobody is watching.
	warnings []string
}
type desktopSessionDeps struct {
	loadToken       func() (string, error)
	resolveConfig   func() config.Config
	authGate        func(string, string, *http.Client) (auth.MeResult, bool, error)
	resolveCA       func(config.Config) (string, error)
	reconcilePi     func() (string, error)
	reconcileSearch func(bool) (managedWebSearchState, error)
	reconcileUI     func() (string, error)
	seedUIDefaults  func() error
	now             func() time.Time
	run             func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error
}

func defaultDesktopSessionDeps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken:       func() (string, error) { token, _, err := auth.Load(); return token, err },
		resolveConfig:   config.OSResolve,
		authGate:        authGate,
		resolveCA:       resolveCA,
		reconcilePi:     reconcileManagedPiExtension,
		reconcileSearch: reconcileManagedWebSearch,
		reconcileUI:     reconcileManagedPiUIExtension,
		seedUIDefaults:  ensurePiDesktopUIDefaults,
		now:             time.Now,
		run:             runDesktopSessionProcess,
	}
}
func newDesktopSessionCommand(deps desktopSessionDeps) *cobra.Command {
	var nodePath, piEntry, runtimeName, codexSession string
	cmd := &cobra.Command{Use: "desktop-session [--runtime pi] --node <absolute-node> --pi-entry <absolute-cli.js> -- <pi-args...> | desktop-session --runtime codex [--codex-session <id>] --", Short: "Launch a private Pi or Codex runtime for a desktop chat", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		var plan desktopSessionPlan
		var err error
		switch runtimeName {
		case "pi":
			if codexSession != "" {
				return fmt.Errorf("desktop-session: --codex-session is only for --runtime codex")
			}
			plan, err = prepareDesktopSession(nodePath, piEntry, args, deps)
		case "codex":
			plan, err = prepareDesktopCodexSession(codexSession, args, deps)
		default:
			return fmt.Errorf("desktop-session: unknown runtime %q; want pi or codex", runtimeName)
		}
		if err != nil {
			return fmt.Errorf("desktop-session: %w", err)
		}
		for _, warning := range plan.warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), warning)
		}
		return deps.run(cmd.Context(), plan, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
	cmd.Flags().StringVar(&runtimeName, "runtime", "pi", "the chat's runtime: pi or codex")
	cmd.Flags().StringVar(&nodePath, "node", "", "absolute path to the package-owned Node executable (pi)")
	cmd.Flags().StringVar(&piEntry, "pi-entry", "", "absolute path to the package-owned Pi CLI entrypoint (pi)")
	cmd.Flags().StringVar(&codexSession, "codex-session", "", "Codex session id to resume (codex)")
	return cmd
}

var desktopSessionCmd = newDesktopSessionCommand(defaultDesktopSessionDeps())

func init() { rootCmd.AddCommand(desktopSessionCmd) }
func prepareDesktopSession(nodePath, piEntry string, piArgs []string, deps desktopSessionDeps) (desktopSessionPlan, error) {
	if err := validateDesktopPiArgs(piArgs); err != nil {
		return desktopSessionPlan{}, err
	}
	if err := validateDesktopRuntime("Node executable", nodePath, true); err != nil {
		return desktopSessionPlan{}, err
	}
	if err := validateDesktopRuntime("Pi entrypoint", piEntry, false); err != nil {
		return desktopSessionPlan{}, err
	}
	token, err := deps.loadToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable; run `vc login`")
	}
	cfg := deps.resolveConfig()
	// The access check, not sign-in: this asks who the token belongs to and
	// whether they are let in, and in production that answer comes from a
	// different service than the one serving the device-authorization routes.
	me, reached, err := deps.authGate(token, cfg.AccessCheckHost, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable: %w", err)
	}
	// The wallet never refuses the session — a refused desktop-session exits,
	// and the app then shows "Chat stopped… check your network" instead of
	// Relay's own 402. Nor is its notice a warning on the command's error
	// stream: the app shows that in the terminal Pi's fullscreen then clears.
	// It travels to Pi in the plan's environment.
	notice := ""
	if reached {
		notice = launchNotice(me, time.Now())
	}
	var warnings []string
	extensionPath, err := deps.reconcilePi()
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi transport unavailable: %w", err)
	}
	if extensionPath == "" {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi transport is disabled")
	}
	if _, err := deps.reconcileSearch(true); err != nil {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi web search unavailable: %w", err)
	}
	if deps.reconcileUI != nil {
		uiPath, uiErr := deps.reconcileUI()
		if uiErr != nil {
			warnings = append(warnings, fmt.Sprintf("vc: warning: managed Pi compact UI was not reconciled: %v", uiErr))
		} else if uiPath != "" && deps.seedUIDefaults != nil {
			if settingsErr := deps.seedUIDefaults(); settingsErr != nil {
				warnings = append(warnings, fmt.Sprintf("vc: warning: Pi compact UI defaults were not seeded: %v", settingsErr))
			}
		}
	}
	// The same seed runSpawn does, in the same place and on the same terms —
	// the desktop app never goes through runSpawn, so without this line the
	// default model reaches only the people who open a terminal. It sits behind
	// the access check on purpose: a token that was refused must not leave a
	// mark in anyone's Pi settings. Unlike everything else here, its failure is
	// a warning: an unreadable settings.json is not worth the user's session.
	if err := ensurePiDefaultModel(); err != nil {
		warnings = append(warnings, fmt.Sprintf("vc: warning: Pi default model was not seeded: %v", err))
	}
	caPath, err := deps.resolveCA(cfg)
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("relay CA unavailable: %w", err)
	}
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
	env = setDesktopEnv(env, "PI_SKIP_VERSION_CHECK", "1")
	env = setDesktopEnv(env, "VC_DESKTOP_SESSION", "1")
	env = withLaunchNotice(env, notice)
	return desktopSessionPlan{nodePath: nodePath, args: append([]string{piEntry}, buildPiArgs(piArgs, extensionPath)...), env: env, warnings: warnings}, nil
}

// prepareDesktopCodexSession readies a desktop chat on Codex: the same
// admission as the CLI and in the same order — token, access check, the
// ChatGPT grant, and only then the install — then Codex in the chat's folder
// (the process cwd, which the managed config trusts) with the desktop's status
// channel and the hook executable in its environment. codexArgs must be empty:
// the lifecycle is --codex-session, anything else would be someone else's
// authority over Codex.
func prepareDesktopCodexSession(codexSession string, codexArgs []string, deps desktopSessionDeps) (desktopSessionPlan, error) {
	if len(codexArgs) != 0 {
		return desktopSessionPlan{}, fmt.Errorf("Codex arguments %q are not allowed; desktop-session accepts only --codex-session", codexArgs)
	}
	if codexSession != "" && !desktopUUIDPattern.MatchString(codexSession) {
		return desktopSessionPlan{}, fmt.Errorf("--codex-session %q is not a Codex session id", codexSession)
	}
	token, err := deps.loadToken()
	if err != nil || strings.TrimSpace(token) == "" {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable; run `vc login`")
	}
	cfg := deps.resolveConfig()
	me, reached, err := deps.authGate(token, cfg.AccessCheckHost, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable: %w", err)
	}
	var warnings []string
	if reached {
		// Codex has no place for vc's notice inside its session, so it is
		// said before Codex takes the terminal, as in the CLI.
		if notice := launchNotice(me, deps.now()); notice != "" {
			warnings = append(warnings, "vc: "+notice)
		}
	}
	folder, err := os.Getwd()
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("chat folder unavailable: %w", err)
	}
	// Codex sees its cwd resolved (getcwd), so the trust names that spelling.
	if resolved, resolveErr := filepath.EvalSymlinks(folder); resolveErr == nil {
		folder = resolved
	}
	prepared, err := prepareCodex(cfg, token, folder, os.Stderr)
	if err != nil {
		return desktopSessionPlan{}, err
	}
	env := prepared.env
	// buildCodexSpawnEnv drops every VC_* variable; the chat's status channel
	// is handed back explicitly, for `vc codex-hook`.
	for _, key := range []string{"VC_DESKTOP_STATUS_PATH", "VC_DESKTOP_CHAT_ID", "VC_DESKTOP_STATUS_GENERATION"} {
		if value := os.Getenv(key); value != "" {
			env = setDesktopEnv(env, key, value)
		}
	}
	env = setDesktopEnv(env, "VC_DESKTOP_SESSION", "1")
	args := []string{codexNoDaemon}
	if codexSession != "" {
		args = append(args, "resume", codexSession)
	}
	return desktopSessionPlan{nodePath: prepared.exe, args: args, env: env, warnings: warnings}, nil
}

var desktopPiArgs = map[string]bool{"--continue": false, "-c": false, "--resume": false, "-r": false, "--session": true, "--session-id": true, "--fork": true, "--no-session": false, "--name": true, "-n": true}

func validateDesktopPiArgs(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, hasEquals := strings.Cut(arg, "=")
		needsValue, allowed := desktopPiArgs[name]
		if !allowed {
			return fmt.Errorf("Pi argument %q is not allowed; desktop-session accepts only session lifecycle flags", name)
		}
		if needsValue {
			if hasEquals {
				return fmt.Errorf("Pi argument %q requires a separate value argument", name)
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return fmt.Errorf("Pi argument %q requires a value", name)
			}
			i++
		} else if hasEquals {
			return fmt.Errorf("Pi argument %q does not take a value", name)
		}
	}
	return nil
}
func setDesktopEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, key) {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}
func validateDesktopRuntime(name, path string, executable bool) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s path must be absolute", name)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s unavailable at %s", name, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", name, path)
	}
	if executable && runtime.GOOS != "windows" && info.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("%s is not executable: %s", name, path)
	}
	return nil
}
func runDesktopSessionProcess(ctx context.Context, plan desktopSessionPlan, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, plan.nodePath, plan.args...)
	cmd.Env = plan.env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return desktopProcessExitError{exitErr.ExitCode()}
		}
		if ctx.Err() != nil {
			return fmt.Errorf("desktop-session canceled: %w", ctx.Err())
		}
		return fmt.Errorf("desktop-session launch failed: %w", err)
	}
	return nil
}

type desktopProcessExitError struct{ code int }

func (e desktopProcessExitError) Error() string {
	return fmt.Sprintf("runtime exited with status %d", e.code)
}
func (e desktopProcessExitError) ExitCode() int { return e.code }
