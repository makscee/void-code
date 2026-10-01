package main

import (
	"context"
	"errors"
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
	// webSearch is the web-search install running alongside Pi, or nil.
	webSearch *backgroundWebSearchInstall
}
type desktopSessionDeps struct {
	loadToken       func() (string, error)
	resolveConfig   func() config.Config
	authGate        func(string, string, *http.Client) (auth.MeResult, bool, error)
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
		reconcilePi:     reconcileManagedPiExtension,
		reconcileSearch: prepareSessionWebSearch,
		reconcileUI:     reconcileManagedPiUIExtension,
		seedUIDefaults:  ensurePiDesktopUIDefaults,
		now:             time.Now,
		run:             runDesktopSessionProcess,
	}
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
		runErr := deps.run(cmd.Context(), plan, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		finishWebSearch(plan.webSearch, cmd.ErrOrStderr())
		return runErr
	}}
	cmd.Flags().StringVar(&nodePath, "node", "", "absolute path to the package-owned Node executable")
	cmd.Flags().StringVar(&piEntry, "pi-entry", "", "absolute path to the package-owned Pi CLI entrypoint")
	_ = cmd.MarkFlagRequired("node")
	_ = cmd.MarkFlagRequired("pi-entry")
	return cmd
}

var desktopSessionCmd = newDesktopSessionCommand(defaultDesktopSessionDeps())

func init() { rootCmd.AddCommand(desktopSessionCmd) }

// prepareDesktopSession is the desktop's view over prepareSession: it checks
// the runtime the app hands over, then asks the core for a strict session with
// the compact UI. The managed transport and web search are required here,
// since the app has no fallback to offer.
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
	plan, err := prepareSession(sessionRequest{
		piArgs: piArgs,
		resolveRuntime: func() (sessionRuntime, error) {
			return sessionRuntime{path: nodePath, entry: piEntry}, nil
		},
		requireManaged: true,
		compactUI:      true,
		env:            [][2]string{{"PI_SKIP_VERSION_CHECK", "1"}, {"VC_DESKTOP_SESSION", "1"}},
	}, deps.core())
	var access sessionAccessError
	if errors.Is(err, errNotLoggedIn) {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable; run `vc login`")
	}
	if errors.As(err, &access) {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable: %w", access.err)
	}
	if err != nil {
		return desktopSessionPlan{}, err
	}
	return desktopSessionPlan{nodePath: plan.path, args: plan.args, env: plan.env, warnings: plan.warnings, webSearch: plan.webSearch}, nil
}

// core is the shared session core's dependencies, taken from the desktop's.
func (d desktopSessionDeps) core() sessionDeps {
	c := defaultSessionDeps()
	c.loadToken, c.resolveConfig, c.authGate = d.loadToken, d.resolveConfig, d.authGate
	c.reconcilePi, c.reconcileSearch, c.reconcileUI, c.seedUIDefaults = d.reconcilePi, d.reconcileSearch, d.reconcileUI, d.seedUIDefaults
	if d.now != nil {
		c.now = d.now
	}
	return c
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
