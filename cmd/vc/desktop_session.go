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
	"sync"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/compat"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
	"github.com/spf13/cobra"
)

type desktopSessionPlan struct {
	nodePath string
	args     []string
	env      []string
}

type desktopSessionDeps struct {
	loadToken       func() (string, error)
	resolveConfig   func() config.Config
	authGate        func(string, string, *http.Client) (auth.MeResult, bool, error)
	fetchGrants     func(string, string, *http.Client) ([]compat.Grant, error)
	now             func() time.Time
	resolveCA       func(config.Config) (string, error)
	reconcilePi     func() (string, error)
	reconcileSearch func(bool) (managedWebSearchState, error)
	loadProvider    func() provider.Provider
	loadLabel       func() string
	run             func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error
}

func defaultDesktopSessionDeps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken:       auth.LoadAndMigrate,
		resolveConfig:   config.OSResolve,
		authGate:        authGate,
		fetchGrants:     fetchDesktopGrants,
		now:             time.Now,
		resolveCA:       resolveCA,
		reconcilePi:     reconcileManagedPiExtension,
		reconcileSearch: reconcileManagedWebSearch,
		loadProvider:    provider.Load,
		loadLabel:       provider.LoadLabel,
		run:             runDesktopSessionProcess,
	}
}

func newDesktopSessionCommand(deps desktopSessionDeps) *cobra.Command {
	var nodePath, piEntry string
	cmd := &cobra.Command{
		Use:   "desktop-session --node <absolute-node> --pi-entry <absolute-cli.js> -- <pi-args...>",
		Short: "Launch a private Node/Pi runtime without changing the selected harness",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := prepareDesktopSession(nodePath, piEntry, args, deps)
			if err != nil {
				return fmt.Errorf("desktop-session: %w", err)
			}
			return deps.run(cmd.Context(), plan, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&nodePath, "node", "", "absolute path to the package-owned Node executable")
	cmd.Flags().StringVar(&piEntry, "pi-entry", "", "absolute path to the package-owned Pi CLI entrypoint")
	_ = cmd.MarkFlagRequired("node")
	_ = cmd.MarkFlagRequired("pi-entry")
	return cmd
}

var desktopSessionCmd = newDesktopSessionCommand(defaultDesktopSessionDeps())

func init() {
	rootCmd.AddCommand(desktopSessionCmd)
}

func fetchDesktopGrants(authHost, token string, client *http.Client) ([]compat.Grant, error) {
	infos, err := cachedFetchProviders(authHost, token, client)
	if err != nil {
		return nil, err
	}
	grants := make([]compat.Grant, 0, len(infos))
	for _, info := range infos {
		grants = append(grants, compat.Grant{ID: info.ID, Name: info.Name, Type: info.Type})
	}
	return grants, nil
}

func prepareDesktopSession(nodePath, piEntry string, piArgs []string, deps desktopSessionDeps) (desktopSessionPlan, error) {
	if deps.now == nil {
		deps.now = time.Now
	}
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
	started := deps.now()
	deadline := started.Add(authProbeTimeout)
	type authResult struct {
		me      auth.MeResult
		reached bool
		err     error
	}
	var authResultValue authResult
	var providerResult launchProviderResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		me, reached, err := deps.authGate(token, cfg.AuthHost, &http.Client{Timeout: remainingDesktopBudget(deadline, deps.now())})
		authResultValue = authResult{me: me, reached: reached, err: err}
	}()
	go func() {
		defer wg.Done()
		grants, err := deps.fetchGrants(cfg.AuthHost, token, &http.Client{Timeout: remainingDesktopBudget(deadline, deps.now())})
		providerResult = launchProviderResult{grants: grants, err: err}
		if err == nil {
			providerResult.kind = providerOutcomeSuccess
		}
	}()
	wg.Wait()
	me, reached, err := authResultValue.me, authResultValue.reached, authResultValue.err
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("authentication unavailable: %w", err)
	}
	if reached && me.Pct != nil {
		if decision := budgetGate(me.Pct, nil); decision.Block {
			return desktopSessionPlan{}, fmt.Errorf("%s", decision.Message)
		}
	}

	active := deps.loadProvider()
	label := deps.loadLabel()
	grants := providerResult.grants
	classGrants := grants
	if !providerResult.successful() {
		classGrants = nil
	}
	class := compat.ClassifyProvider(active, label, classGrants)
	if class != compat.ProviderChatGPT && class != compat.ProviderDeepSeek {
		return desktopSessionPlan{}, fmt.Errorf("active provider is not in the current Pi-compatible grants")
	}

	extensionPath, err := deps.reconcilePi()
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi provider unavailable: %w", err)
	}
	grantClass, exactGrant := compat.ExactGrantClass(active, grants)
	webEligible := providerResult.successful() && exactGrant && grantClass == compat.ProviderChatGPT
	if _, err := deps.reconcileSearch(webEligible); err != nil {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi web search unavailable: %w", err)
	}
	if extensionPath == "" {
		return desktopSessionPlan{}, fmt.Errorf("managed Pi provider is disabled")
	}
	caPath, err := deps.resolveCA(cfg)
	if err != nil {
		return desktopSessionPlan{}, fmt.Errorf("relay CA unavailable: %w", err)
	}

	env := buildPiSpawnEnv(active, os.Environ(), cfg.RelayScheme, cfg.RelayHost, token, caPath)
	env = setDesktopEnv(env, "PI_SKIP_VERSION_CHECK", "1")
	args := append([]string{piEntry}, piArgs...)
	switch class {
	case compat.ProviderChatGPT:
		env = append(env, "VC_PI_PROVIDER_KIND=codex")
		args = append([]string{piEntry}, buildPiVoidCodexArgs(piArgs, extensionPath)...)
	case compat.ProviderDeepSeek:
		env = append(env, "VC_PI_PROVIDER_KIND=deepseek")
		args = append([]string{piEntry}, buildPiVoidDeepSeekArgs(piArgs, extensionPath)...)
	}
	return desktopSessionPlan{nodePath: nodePath, args: args, env: env}, nil
}

// Desktop accepts only session lifecycle controls. Provider, model, credential,
// extension, and other Pi behavior remain owned by vc's managed launch contract.
var desktopPiArgs = map[string]bool{
	"--continue":   false,
	"-c":           false,
	"--resume":     false,
	"-r":           false,
	"--session":    true,
	"--session-id": true,
	"--fork":       true,
	"--no-session": false,
	"--name":       true,
	"-n":           true,
}

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

func remainingDesktopBudget(deadline, now time.Time) time.Duration {
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return time.Nanosecond
	}
	return remaining
}

func setDesktopEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			continue
		}
		out = append(out, entry)
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
			return desktopProcessExitError{code: exitErr.ExitCode()}
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
