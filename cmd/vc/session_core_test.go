package main

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

type sessionCoreProbe struct {
	steps     []string
	piErr     error
	piPath    string
	searchErr error
	gateErr   error
}

func (p *sessionCoreProbe) deps() sessionDeps {
	step := func(name string) { p.steps = append(p.steps, name) }
	return sessionDeps{
		loadToken: func() (string, error) { step("token"); return "token", nil },
		resolveConfig: func() config.Config {
			step("config")
			return config.Config{RelayScheme: "https", RelayHost: "relay.test"}
		},
		authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			step("access")
			return auth.MeResult{}, false, p.gateErr
		},
		resolveCA:        func(config.Config) (string, error) { step("ca"); return "/ca.pem", nil },
		reconcilePi:      func() (string, error) { step("transport"); return p.piPath, p.piErr },
		writeEmbeddedPi:  func() (string, error) { step("embedded"); return "/embedded.ts", nil },
		reconcileSearch:  func(bool) (managedWebSearchState, error) { step("search"); return managedWebSearchReady, p.searchErr },
		reconcileUI:      func() (string, error) { step("ui"); return "/ui.ts", nil },
		seedUIDefaults:   func() error { step("ui-defaults"); return nil },
		seedDefaultModel: func() error { step("model"); return nil },
		now:              time.Now,
	}
}

func coreRequest(p *sessionCoreProbe, strict bool) sessionRequest {
	return sessionRequest{
		resolveRuntime: func() (sessionRuntime, error) {
			p.steps = append(p.steps, "runtime")
			return sessionRuntime{path: "/node", entry: "/pi.js"}, nil
		},
		requireManaged: strict,
		compactUI:      strict,
	}
}

// TestPrepareSessionOneOrderForBothSurfaces pins the point of the core: the CLI
// and the desktop run the same steps in the same order. The desktop only adds
// the compact UI.
func TestPrepareSessionOneOrderForBothSurfaces(t *testing.T) {
	cli := &sessionCoreProbe{piPath: "/managed.ts"}
	if _, err := prepareSession(coreRequest(cli, false), cli.deps()); err != nil {
		t.Fatal(err)
	}
	desktop := &sessionCoreProbe{piPath: "/managed.ts"}
	if _, err := prepareSession(coreRequest(desktop, true), desktop.deps()); err != nil {
		t.Fatal(err)
	}
	want := []string{"token", "config", "access", "runtime", "transport", "search", "model", "ca"}
	if !reflect.DeepEqual(cli.steps, want) {
		t.Fatalf("CLI steps = %v, want %v", cli.steps, want)
	}
	wantDesktop := []string{"token", "config", "access", "runtime", "transport", "search", "ui", "ui-defaults", "model", "ca"}
	if !reflect.DeepEqual(desktop.steps, wantDesktop) {
		t.Fatalf("desktop steps = %v, want %v", desktop.steps, wantDesktop)
	}
}

// TestPrepareSessionRefusalStopsBeforeRuntime: a refused token neither installs
// a runtime nor touches Pi's files, and the refusal is marked as one.
func TestPrepareSessionRefusalStopsBeforeRuntime(t *testing.T) {
	p := &sessionCoreProbe{gateErr: auth.ErrAccessNotGranted}
	_, err := prepareSession(coreRequest(p, false), p.deps())
	var access sessionAccessError
	if !errors.As(err, &access) || !errors.Is(err, auth.ErrAccessNotGranted) {
		t.Fatalf("err = %v, want an access refusal wrapping ErrAccessNotGranted", err)
	}
	if want := []string{"token", "config", "access"}; !reflect.DeepEqual(p.steps, want) {
		t.Fatalf("steps = %v, want %v", p.steps, want)
	}
}

// TestPrepareSessionManagedFailures: the CLI warns and falls back to the
// embedded transport; the desktop refuses.
func TestPrepareSessionManagedFailures(t *testing.T) {
	cli := &sessionCoreProbe{piErr: errors.New("broken"), searchErr: errors.New("offline")}
	plan, err := prepareSession(coreRequest(cli, false), cli.deps())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.args, " "); got != "/pi.js -e /embedded.ts" {
		t.Fatalf("CLI args = %q, want the embedded transport", got)
	}
	if len(plan.warnings) != 2 {
		t.Fatalf("CLI warnings = %q, want the transport and search warnings", plan.warnings)
	}

	desktop := &sessionCoreProbe{piErr: errors.New("broken")}
	if _, err := prepareSession(coreRequest(desktop, true), desktop.deps()); err == nil || !strings.Contains(err.Error(), "managed Pi transport unavailable") {
		t.Fatalf("desktop err = %v, want the transport refusal", err)
	}
	desktop = &sessionCoreProbe{}
	if _, err := prepareSession(coreRequest(desktop, true), desktop.deps()); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("desktop err = %v, want the disabled-transport refusal", err)
	}
}
