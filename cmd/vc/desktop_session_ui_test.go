package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

type desktopUIProbe struct {
	journal       []string
	uiPath        string
	uiErr         error
	uiSettingsErr error
}

func (p *desktopUIProbe) note(step string) { p.journal = append(p.journal, step) }

func (p *desktopUIProbe) deps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken: func() (string, error) { p.note("token"); return "token", nil },
		resolveConfig: func() config.Config {
			p.note("config")
			return config.Config{AccessCheckHost: "https://access.invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
		},
		authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			p.note("access")
			return auth.MeResult{}, true, nil
		},
		reconcilePi: func() (string, error) { p.note("transport"); return "/managed/void-code.ts", nil },
		reconcileSearch: func(bool) (managedWebSearchState, error) {
			p.note("search")
			return managedWebSearchReady, nil
		},
		reconcileUI: func() (string, error) {
			p.note("ui")
			return p.uiPath, p.uiErr
		},
		seedUIDefaults: func() error {
			p.note("ui-settings")
			return p.uiSettingsErr
		},
		resolveCA: func(config.Config) (string, error) { p.note("ca"); return "/ca.pem", nil },
	}
}

func envValues(env []string, key string) []string {
	var values []string
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, key) {
			values = append(values, value)
		}
	}
	return values
}

// The production dependency graph, not only test probes, must install and seed compact UI for a fresh desktop user.
func TestDefaultDesktopSessionInstallsCompactUIForFreshUser(t *testing.T) {
	dir := piSettingsSandbox(t)
	t.Setenv("VC_PI_COMPACT_UI", "")
	node, piEntry := desktopFiles(t)
	deps := defaultDesktopSessionDeps()
	deps.loadToken = func() (string, error) { return "token", nil }
	deps.resolveConfig = func() config.Config {
		return config.Config{AccessCheckHost: "https://access.invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
	}
	deps.authGate = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		return auth.MeResult{}, true, nil
	}
	deps.reconcilePi = func() (string, error) { return "/managed/void-code.ts", nil }
	deps.reconcileSearch = func(bool) (managedWebSearchState, error) { return managedWebSearchReady, nil }
	deps.resolveCA = func(config.Config) (string, error) { return "/ca.pem", nil }

	plan, err := prepareDesktopSession(node, piEntry, nil, deps)
	if err != nil {
		t.Fatal(err)
	}

	uiPath := filepath.Join(dir, "extensions", "void-code-ui.ts")
	if data, err := os.ReadFile(uiPath); err != nil {
		t.Fatalf("desktop did not install managed compact UI: %v", err)
	} else if string(data) != piVoidCodeUIExtensionSource {
		t.Fatal("desktop installed compact UI source different from the embedded product source")
	}
	settings := readPiSettings(t, filepath.Join(dir, "settings.json"))
	if settings["tuiMode"] != "fullscreen" || settings["hideThinkingBlock"] != false {
		t.Fatalf("desktop UI defaults = %#v", settings)
	}
	if got := envValues(plan.env, "VC_DESKTOP_SESSION"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("desktop marker = %#v, want [1]", got)
	}
}

// Desktop context is minted by vc after sanitisation; a parent process cannot turn compact UI on for terminal Pi.
func TestDesktopSessionMintsOneTrustedCompactUIMarker(t *testing.T) {
	piSettingsSandbox(t)
	t.Setenv("VC_DESKTOP_SESSION", "attacker-value")
	node, piEntry := desktopFiles(t)
	probe := &desktopUIProbe{uiPath: "/managed/void-code-ui.ts"}

	plan, err := prepareDesktopSession(node, piEntry, nil, probe.deps())
	if err != nil {
		t.Fatal(err)
	}

	if got := envValues(plan.env, "VC_DESKTOP_SESSION"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("VC_DESKTOP_SESSION = %#v, want one vc-minted value [1]", got)
	}
	plain := buildPiSpawnEnv(providerRelay(), []string{"VC_DESKTOP_SESSION=1"}, "https", "relay.invalid", "token", "/ca.pem")
	if got := envValues(plain, "VC_DESKTOP_SESSION"); len(got) != 0 {
		t.Fatalf("terminal Pi inherited desktop-only marker: %#v", got)
	}
	if strings.Join(probe.journal, ",") != "token,config,access,transport,search,ui,ui-settings,ca" {
		t.Fatalf("desktop UI prepared outside the admitted pre-launch phase: %v", probe.journal)
	}
}

// Presentation is optional: an ownership conflict must warn, not cost the user an authenticated chat.
func TestDesktopSessionSurvivesManagedUIInstallFailure(t *testing.T) {
	piSettingsSandbox(t)
	node, piEntry := desktopFiles(t)
	probe := &desktopUIProbe{uiErr: errors.New("file is not owned by void-code")}

	plan, err := prepareDesktopSession(node, piEntry, nil, probe.deps())
	if err != nil {
		t.Fatalf("optional compact UI blocked desktop session: %v", err)
	}

	joined := strings.Join(plan.warnings, "\n")
	if !strings.Contains(joined, "compact UI") || !strings.Contains(joined, "not owned") {
		t.Fatalf("install failure warning = %q", joined)
	}
	if strings.Contains(strings.Join(probe.journal, ","), "ui-settings") {
		t.Fatalf("UI settings were seeded although the extension was unavailable: %v", probe.journal)
	}
	if got := envValues(plan.env, "VC_DESKTOP_SESSION"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("desktop marker missing after fail-open: %#v", got)
	}
}

// An unreadable settings.json degrades only defaults; the installed compact UI and session still launch.
func TestDesktopSessionSurvivesCompactUISettingsFailure(t *testing.T) {
	piSettingsSandbox(t)
	node, piEntry := desktopFiles(t)
	probe := &desktopUIProbe{uiPath: "/managed/void-code-ui.ts", uiSettingsErr: errors.New("parse Pi settings")}

	plan, err := prepareDesktopSession(node, piEntry, nil, probe.deps())
	if err != nil {
		t.Fatalf("optional UI defaults blocked desktop session: %v", err)
	}

	joined := strings.Join(plan.warnings, "\n")
	if !strings.Contains(joined, "compact UI defaults") || !strings.Contains(joined, "parse Pi settings") {
		t.Fatalf("settings failure warning = %q", joined)
	}
}

// Opt-out returns no managed UI path and must not leave unrelated fullscreen/reasoning defaults behind.
func TestDesktopSessionOptOutSkipsCompactUISettings(t *testing.T) {
	piSettingsSandbox(t)
	node, piEntry := desktopFiles(t)
	probe := &desktopUIProbe{uiPath: ""}

	plan, err := prepareDesktopSession(node, piEntry, nil, probe.deps())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(strings.Join(probe.journal, ","), "ui-settings") {
		t.Fatalf("opted-out UI still changed Pi settings: %v", probe.journal)
	}
	if joined := strings.Join(plan.warnings, "\n"); strings.Contains(joined, "compact UI") {
		t.Fatalf("clean opt-out emitted a warning: %q", joined)
	}
}

// No extension or UI settings are written before the live access gate accepts the token.
func TestDesktopSessionDoesNotPrepareCompactUIBeforeAccess(t *testing.T) {
	piSettingsSandbox(t)
	node, piEntry := desktopFiles(t)
	probe := &desktopUIProbe{uiPath: "/managed/void-code-ui.ts"}
	deps := probe.deps()
	deps.authGate = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		probe.note("access")
		return auth.MeResult{}, false, errors.New("access denied")
	}

	if _, err := prepareDesktopSession(node, piEntry, nil, deps); err == nil {
		t.Fatal("refused token prepared a desktop session")
	}
	for _, forbidden := range []string{"transport", "search", "ui", "ui-settings", "ca"} {
		if strings.Contains(strings.Join(probe.journal, ","), forbidden) {
			t.Fatalf("%s ran before access was granted: %v", forbidden, probe.journal)
		}
	}
}
