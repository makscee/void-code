package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/provider"
)

// prepareSession is the one prepare-and-launch core behind every Pi session:
// bare `vc` (runSpawn) and `vc desktop-session` both call it, so the desktop is
// a view over the same steps rather than a second copy of them. In order:
//
//  1. access check: a live admission, never a cached one
//  2. runtime: which Node/Pi to start, resolved only after admission
//  3. transport: the managed Pi extension that carries the relay
//  4. web search (an install it needs runs alongside Pi, see sessionPlan.webSearch)
//  5. compact UI (when the surface asks for it)
//  6. the default model seed
//  7. the child environment, with the wallet notice
//
// A surface decides only how its runtime is found, how strict it is about the
// managed pieces, and how it reports what the core returns.
func prepareSession(req sessionRequest, deps sessionDeps) (sessionPlan, error) {
	var plan sessionPlan
	warn := func(format string, args ...any) {
		plan.warnings = append(plan.warnings, fmt.Sprintf("vc: warning: "+format, args...))
	}

	token, _ := deps.loadToken()
	if strings.TrimSpace(token) == "" {
		return plan, sessionAccessError{errNotLoggedIn}
	}
	cfg := deps.resolveConfig()
	// Admission is always live: cached identity and wallet are only display
	// hints, never permission to start a paid session. The question is who the
	// token belongs to and whether they are let in, so it goes to the
	// access-check host, not to the service serving sign-in.
	me, reached, err := deps.authGate(token, cfg.AccessCheckHost, &http.Client{Timeout: authProbeTimeout})
	if err != nil {
		return plan, sessionAccessError{err}
	}
	// The wallet never refuses the session: what it has to say travels to Pi,
	// which shows it once the session is up (see walletLaunchNotice). A refused
	// desktop-session would read as "Chat stopped… check your network" instead
	// of the relay's own 402.
	notice := ""
	if reached {
		notice = launchNotice(me, deps.now())
	}

	// Resolve launch artifacts after live admission but before preparing
	// anything for the child.
	rt, err := req.resolveRuntime()
	if err != nil {
		return plan, err
	}

	extPath, err := deps.reconcilePi()
	switch {
	case err != nil && req.requireManaged:
		return plan, fmt.Errorf("managed Pi transport unavailable: %w", err)
	case err != nil:
		warn("managed Pi provider was not reconciled: %v", err)
	case extPath == "" && req.requireManaged:
		return plan, fmt.Errorf("managed Pi transport is disabled")
	}
	if extPath == "" {
		// The CLI keeps a way in when the managed transport is off or broken:
		// the embedded extension.
		if extPath, err = deps.writeEmbeddedPi(); err != nil {
			return plan, fmt.Errorf("cannot write Pi relay extension: %w", err)
		}
	}
	searchState, searchErr := deps.reconcileSearch(true)
	if searchErr != nil {
		if req.requireManaged {
			return plan, fmt.Errorf("managed Pi web search unavailable: %w", searchErr)
		}
		warn("managed Pi web search was not reconciled: %v", searchErr)
	}
	if req.compactUI && deps.reconcileUI != nil {
		uiPath, uiErr := deps.reconcileUI()
		if uiErr != nil {
			warn("managed Pi compact UI was not reconciled: %v", uiErr)
		} else if uiPath != "" && deps.seedUIDefaults != nil {
			if settingsErr := deps.seedUIDefaults(); settingsErr != nil {
				warn("Pi compact UI defaults were not seeded: %v", settingsErr)
			}
		}
	}
	// A seeded default is a convenience, never a precondition: an unreadable or
	// hand-broken settings.json must still let Pi start. It sits behind the
	// access check on purpose: a refused token must not leave a mark in anyone's
	// Pi settings.
	if err := deps.seedDefaultModel(); err != nil {
		warn("Pi default model was not seeded: %v", err)
	}

	parent := os.Environ()
	env := buildPiSpawnEnv(provider.Provider{Kind: provider.Relay}, parent)
	// A bundled runtime gets the PATH composed around its private Node; a
	// runtime without one keeps its inherited PATH exactly.
	if rt.privateNode != "" {
		env = withBuiltPiPath(env, parent, rt.privateNode)
	}
	for _, kv := range req.env {
		env = setEnv(env, kv[0], kv[1])
	}
	env = withLaunchNotice(env, notice)

	args := buildPiArgs(req.piArgs, extPath)
	if rt.entry != "" {
		args = append([]string{rt.entry}, args...)
	}
	plan.path, plan.args, plan.env = rt.path, args, env
	// Last, so nothing after it can fail the launch and orphan it: the install
	// starts now and Pi does not wait for it. npm took over a minute on Windows
	// and Pi without web search is still Pi.
	if searchErr == nil && searchState == managedWebSearchPending && deps.installSearch != nil {
		plan.webSearch = startManagedWebSearchInstall(deps.installSearch, deps.registerSearch)
	}
	return plan, nil
}

// sessionRequest is what a surface asks of the core.
type sessionRequest struct {
	piArgs []string
	// resolveRuntime finds the Node/Pi to start. It runs after admission, so
	// a refused token never triggers an install.
	resolveRuntime func() (sessionRuntime, error)
	// requireManaged refuses the session when the managed transport or web
	// search cannot be reconciled. The desktop has no fallback; the CLI warns
	// and falls back to the embedded transport.
	requireManaged bool
	// compactUI reconciles the managed compact UI and seeds its defaults.
	compactUI bool
	// env is set on the child after the core's own variables.
	env [][2]string
}

// sessionRuntime is the process the session starts: path, then entry (when
// path is a Node that runs Pi's module) and Pi's own arguments.
type sessionRuntime struct {
	path, entry string
	// privateNode is set when the runtime bundles its own Node, whose PATH the
	// child then gets.
	privateNode string
}

// sessionPlan is the prepared launch. Warnings are what the core wants the
// user to see but was not willing to fail over; the surface says them on its
// own stream. They are kept even when preparation fails.
type sessionPlan struct {
	path     string
	args     []string
	env      []string
	warnings []string
	// webSearch is the web-search install running alongside Pi, or nil. The
	// surface calls finishWebSearch once Pi has exited.
	webSearch *backgroundWebSearchInstall
}

// finishWebSearch waits for the session's web-search install after Pi has
// exited (bounded by managedWebSearchInstallGrace) and says on stderr — now
// that Pi no longer owns it — if it failed.
func finishWebSearch(install *backgroundWebSearchInstall, stderr io.Writer) {
	err := install.finish(managedWebSearchInstallGrace)
	if err == nil {
		return
	}
	var leftover *webSearchStageLeftover
	if !errors.As(err, &leftover) || leftover.install != nil {
		fmt.Fprintf(stderr, "vc: warning: managed Pi web search was not installed: %v\n", err)
	}
	if leftover != nil {
		fmt.Fprintf(stderr, "vc: warning: managed Pi web search %s\n", leftover.leftover())
	}
}

type sessionDeps struct {
	loadToken       func() (string, error)
	resolveConfig   func() config.Config
	authGate        func(string, string, *http.Client) (auth.MeResult, bool, error)
	reconcilePi     func() (string, error)
	writeEmbeddedPi func() (string, error)
	reconcileSearch func(bool) (managedWebSearchState, error)
	// installSearch runs when reconcileSearch reports managedWebSearchPending,
	// alongside Pi; registerSearch runs after Pi exits if it succeeded.
	installSearch    func(context.Context) error
	registerSearch   func() error
	reconcileUI      func() (string, error)
	seedUIDefaults   func() error
	seedDefaultModel func() error
	now              func() time.Time
}

func defaultSessionDeps() sessionDeps {
	return sessionDeps{
		loadToken:        func() (string, error) { token, _, err := auth.Load(); return token, err },
		resolveConfig:    config.OSResolve,
		authGate:         authGate,
		reconcilePi:      reconcileManagedPiExtension,
		writeEmbeddedPi:  ensurePiVoidCodexExtension,
		reconcileSearch:  prepareSessionWebSearch,
		installSearch:    installManagedWebSearch,
		registerSearch:   registerManagedWebSearch,
		reconcileUI:      reconcileManagedPiUIExtension,
		seedUIDefaults:   ensurePiDesktopUIDefaults,
		seedDefaultModel: ensurePiDefaultModel,
		now:              time.Now,
	}
}

// errNotLoggedIn is what both surfaces say when there is no token to check.
var errNotLoggedIn = errors.New("Not logged in. Run `vc login` to authenticate (email, pairing code, or --code <ACCESS-CODE>).")

// sessionAccessError marks a refusal at the access check, as opposed to a
// launch that failed after admission. Surfaces report the two differently.
// The cause stays reachable, so errors.Is(err, auth.ErrAccessNotGranted) holds.
type sessionAccessError struct{ err error }

func (e sessionAccessError) Error() string { return e.err.Error() }
func (e sessionAccessError) Unwrap() error { return e.err }

// setEnv sets key in env, dropping any earlier entry whose name matches without
// regard to case (Windows reads Path and PATH as one variable).
func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, key) {
			out = append(out, entry)
		}
	}
	return append(out, key+"="+value)
}
