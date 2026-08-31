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
	now             func() time.Time
	run             func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error
}

func defaultDesktopSessionDeps() desktopSessionDeps {
	return desktopSessionDeps{func() (string, error) { token, _, err := auth.Load(); return token, err }, config.OSResolve, authGate, resolveCA, reconcileManagedPiExtension, reconcileManagedWebSearch, time.Now, runDesktopSessionProcess}
}
func newDesktopSessionCommand(deps desktopSessionDeps) *cobra.Command {
	var nodePath, piEntry string
	cmd := &cobra.Command{Use: "desktop-session --node <absolute-node> --pi-entry <absolute-cli.js> -- <pi-args...>", Short: "Launch a private Pi runtime", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		plan, err := prepareDesktopSession(nodePath, piEntry, args, deps)
		if err != nil {
			return fmt.Errorf("desktop-session: %w", err)
		}
		for _, warning := range plan.warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), warning)
		}
		return deps.run(cmd.Context(), plan, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
	cmd.Flags().StringVar(&nodePath, "node", "", "absolute path to the package-owned Node executable")
	cmd.Flags().StringVar(&piEntry, "pi-entry", "", "absolute path to the package-owned Pi CLI entrypoint")
	_ = cmd.MarkFlagRequired("node")
	_ = cmd.MarkFlagRequired("pi-entry")
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
	if reached && me.Pct != nil {
		if decision := budgetGate(me.Pct, nil); decision.Block {
			return desktopSessionPlan{}, fmt.Errorf("%s", decision.Message)
		}
	}
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
	// The same seed runSpawn does, in the same place and on the same terms —
	// the desktop app never goes through runSpawn, so without this line the
	// default model reaches only the people who open a terminal. It sits behind
	// the access check on purpose: a token that was refused must not leave a
	// mark in anyone's Pi settings. Unlike everything else here, its failure is
	// a warning: an unreadable settings.json is not worth the user's session.
	var warnings []string
	if err := ensurePiDefaultModel(); err != nil {
		warnings = append(warnings, fmt.Sprintf("vc: warning: Pi default model was not seeded: %v", err))
	}
	caPath, err := deps.resolveCA(cfg)
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("relay CA unavailable: %w", err)
	}
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
	env = setDesktopEnv(env, "PI_SKIP_VERSION_CHECK", "1")
	return desktopSessionPlan{nodePath: nodePath, args: append([]string{piEntry}, buildPiArgs(piArgs, extensionPath)...), env: env, warnings: warnings}, nil
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
	return fmt.Sprintf("Pi exited with status %d", e.code)
}
func (e desktopProcessExitError) ExitCode() int { return e.code }
