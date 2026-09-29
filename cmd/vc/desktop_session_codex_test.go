package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// `vc desktop-session --runtime codex` (spec 2026-09-29-desktop-codex-chats-design,
// item 4): a desktop chat on Codex. The same admission the CLI does, in the
// same order — token, access check, the ChatGPT grant, and only then the
// 130–160 MB install — then Codex in the chat's terminal with the desktop's
// status channel and the hook executable in its environment.
//
// Contract pinned here:
//   - flags: --runtime pi|codex (default pi), --codex-session <uuid> (codex only);
//     --node/--pi-entry are required for pi and not for codex;
//   - for codex nothing may follow `--`;
//   - the plan runs Codex as plan.nodePath (the executable runDesktopSessionProcess
//     starts) with plan.args exactly [--no-daemon] or [--no-daemon resume <uuid>];
//   - CODEX_HOME is ~/.void-code/codex, as for the CLI, and its config.toml trusts
//     the chat's folder (the process cwd).

const (
	desktopCodexToken   = "desktop-codex-secret-token-a41f"
	desktopCodexSession = "01a0ec63-35c1-7d02-bc6a-c06464a61565" // a UUIDv7, as Codex issues them
	desktopCodexChat    = "7d9f2c1a-3b4e-4f6a-8c2d-1e0f9a8b7c6d"
)

type desktopCodexProbe struct {
	mu        sync.Mutex
	journal   []string
	grants    []map[string]string
	gateErr   error
	fakeCodex string
	plan      desktopSessionPlan
	ran       bool
	server    *httptest.Server
}

func (p *desktopCodexProbe) note(step string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.journal = append(p.journal, step)
}

func (p *desktopCodexProbe) steps() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.journal...)
}

func (p *desktopCodexProbe) called(step string) bool {
	for _, s := range p.steps() {
		if s == step {
			return true
		}
	}
	return false
}

func (p *desktopCodexProbe) index(step string) int {
	for i, s := range p.steps() {
		if s == step {
			return i
		}
	}
	return -1
}

// prepareDesktopCodex builds a signed-in sandbox: an auth host that lists
// grants, a stubbed Codex install, a chat folder as the working directory and
// the environment the desktop gives `vc desktop-session`, planted with values
// that must not reach Codex.
func prepareDesktopCodex(t *testing.T, grants []map[string]string) (*desktopCodexProbe, string, string) {
	t.Helper()
	home := withTempHome(t)
	p := &desktopCodexProbe{grants: grants}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/vc/providers" {
			http.NotFound(w, r)
			return
		}
		p.note("providers")
		if got := r.Header.Get("Authorization"); got != "Bearer "+desktopCodexToken {
			t.Errorf("providers asked with Authorization %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": p.grants})
	}))
	t.Cleanup(p.server.Close)

	p.fakeCodex = filepath.Join(home, "fake-codex-install", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(p.fakeCodex), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, p.fakeCodex, "#!/bin/sh\nexit 0\n")
	saved := ensureCodexRuntime
	ensureCodexRuntime = func(io.Writer) (string, error) {
		p.note("ensure")
		return p.fakeCodex, nil
	}
	t.Cleanup(func() { ensureCodexRuntime = saved })

	cwd := t.TempDir()
	t.Chdir(cwd)

	statusPath := filepath.Join(t.TempDir(), "channel", "status.json")
	t.Setenv("VC_DESKTOP_STATUS_PATH", statusPath)
	t.Setenv("VC_DESKTOP_CHAT_ID", desktopCodexChat)
	t.Setenv("VC_DESKTOP_STATUS_GENERATION", "4")
	// None of these may reach Codex.
	t.Setenv("VC_RUNTIME_SWITCH_FILE", "/parent/switch-request")
	t.Setenv("VC_AUTH_TOKEN", "parent-token")
	t.Setenv("VC_HOOK_EXE", "/parent/not-vc")
	t.Setenv("VC_CODEX_PROVIDER", "parent-provider")
	t.Setenv("CODEX_HOME", "/parent/codex-home")
	t.Setenv("OPENAI_API_KEY", "parent-openai-key")
	t.Setenv("VC_DESKTOP_SESSION", "0")
	return p, home, cwd
}

func (p *desktopCodexProbe) deps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken: func() (string, error) { p.note("token"); return desktopCodexToken, nil },
		resolveConfig: func() config.Config {
			p.note("config")
			return config.Config{AuthHost: p.server.URL, AccessCheckHost: p.server.URL, RelayScheme: "https", RelayHost: "relay.test:9443"}
		},
		authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			p.note("access")
			if p.gateErr != nil {
				return auth.MeResult{}, false, p.gateErr
			}
			return auth.MeResult{}, true, nil
		},
		resolveCA:   func(config.Config) (string, error) { p.note("ca"); return "/ca.pem", nil },
		reconcilePi: func() (string, error) { p.note("reconcilePi"); return "/managed/void-code.ts", nil },
		reconcileSearch: func(bool) (managedWebSearchState, error) {
			p.note("reconcileSearch")
			return managedWebSearchReady, nil
		},
		reconcileUI:    func() (string, error) { p.note("reconcileUI"); return "", nil },
		seedUIDefaults: func() error { p.note("seedUIDefaults"); return nil },
		now:            time.Now,
		run: func(_ context.Context, plan desktopSessionPlan, _ io.Reader, _, _ io.Writer) error {
			p.note("run")
			p.ran = true
			p.plan = plan
			return nil
		},
	}
}

func execDesktopSessionArgs(t *testing.T, deps desktopSessionDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newDesktopSessionCommand(deps)
	var errOut bytes.Buffer
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return errOut.String(), err
}

func selfExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}

func sameFilePath(left, right string) bool {
	if left == right {
		return true
	}
	l, lerr := filepath.EvalSymlinks(left)
	r, rerr := filepath.EvalSymlinks(right)
	return lerr == nil && rerr == nil && l == r
}

func TestDesktopSessionCodexStartsANewChat(t *testing.T) {
	p, home, cwd := prepareDesktopCodex(t, codexGrants)

	// No --node, no --pi-entry: Codex needs neither.
	warnings, err := execDesktopSessionArgs(t, p.deps(), "--runtime", "codex", "--")
	if err != nil {
		t.Fatalf("desktop-session --runtime codex: %v; stderr=%q; steps=%v", err, warnings, p.steps())
	}
	if !p.ran {
		t.Fatal("Codex was never launched")
	}
	if p.plan.nodePath != p.fakeCodex {
		t.Fatalf("plan runs %q, want the Codex ensureCodexRuntime returned (%q)", p.plan.nodePath, p.fakeCodex)
	}
	if strings.Join(p.plan.args, " ") != "--no-daemon" {
		t.Fatalf("Codex arguments = %q, want exactly [--no-daemon]", p.plan.args)
	}

	// Order as in the CLI: nobody downloads Codex before the grant is known,
	// and the grant is asked only of an admitted token.
	for _, pair := range [][2]string{{"token", "access"}, {"access", "providers"}, {"providers", "ensure"}, {"ensure", "run"}} {
		a, b := p.index(pair[0]), p.index(pair[1])
		if a < 0 || b < 0 || a > b {
			t.Fatalf("want %s before %s; steps were %v", pair[0], pair[1], p.steps())
		}
	}
	// Pi's transport, web search and UI are Pi's; a Codex chat must not fail
	// or wait on them.
	for _, step := range []string{"reconcilePi", "reconcileSearch", "reconcileUI", "seedUIDefaults"} {
		if p.called(step) {
			t.Errorf("a Codex chat ran Pi's %s; steps were %v", step, p.steps())
		}
	}

	env := p.plan.env
	codexHome, n := envCount(env, "CODEX_HOME")
	wantHome := filepath.Join(home, ".void-code", "codex")
	if n != 1 || !sameDirectory(codexHome, wantHome) {
		t.Fatalf("CODEX_HOME = %q (present %d times), want exactly one pointing at %s", codexHome, n, wantHome)
	}
	wantEnvOnce(t, env, "VC_AUTH_TOKEN", desktopCodexToken)
	wantEnvOnce(t, env, "VC_CODEX_PROVIDER", "chatgpt-new")
	wantEnvOnce(t, env, "VC_DESKTOP_SESSION", "1")
	wantEnvOnce(t, env, "VC_DESKTOP_STATUS_PATH", os.Getenv("VC_DESKTOP_STATUS_PATH"))
	wantEnvOnce(t, env, "VC_DESKTOP_CHAT_ID", desktopCodexChat)
	wantEnvOnce(t, env, "VC_DESKTOP_STATUS_GENERATION", "4")
	hookExe, n := envCount(env, "VC_HOOK_EXE")
	if n != 1 || !filepath.IsAbs(hookExe) || !sameFilePath(hookExe, selfExecutable(t)) {
		t.Errorf("VC_HOOK_EXE = %q (present %d times), want exactly one: the absolute path of this vc (%s)", hookExe, n, selfExecutable(t))
	}
	// The desktop never switches runtimes inside a chat; the variable that
	// offers the switch must not reach Codex.
	if v, n := envCount(env, "VC_RUNTIME_SWITCH_FILE"); n != 0 {
		t.Errorf("VC_RUNTIME_SWITCH_FILE=%q reached a desktop Codex", v)
	}
	for _, key := range []string{"OPENAI_API_KEY"} {
		if v, n := envCount(env, key); n != 0 {
			t.Errorf("inherited %s=%q reached Codex", key, v)
		}
	}
	joined := strings.Join(env, "\n")
	for _, planted := range []string{"parent-token", "/parent/not-vc", "parent-provider", "/parent/codex-home", "/parent/switch-request"} {
		if strings.Contains(joined, planted) {
			t.Errorf("parent value %q reached Codex", planted)
		}
	}

	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("config.toml was not written before launch: %v", err)
	}
	text := string(data)
	if strings.Contains(text, desktopCodexToken) {
		t.Fatal("config.toml carries the VC token")
	}
	if !strings.Contains(text, "codex-hook") {
		t.Errorf("config.toml has no codex-hook hooks; the chat would never leave Working/Ready unknown:\n%s", text)
	}
	if !strings.Contains(text, `"https://relay.test:9443/codex"`) {
		t.Errorf("config.toml does not point Codex at the configured relay:\n%s", text)
	}
	if !trustsFolder(text, cwd) {
		t.Errorf("config.toml does not trust the chat folder %s; Codex would ask \"Trust this folder?\" again:\n%s", cwd, text)
	}
}

// trustsFolder finds a [projects."<dir>"] table naming cwd (as written or
// resolved) with trust_level = "trusted".
func trustsFolder(text, cwd string) bool {
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "[projects.") || !strings.HasSuffix(line, "]") {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(line, "[projects."), "]")
		var dir string
		if err := json.Unmarshal([]byte(key), &dir); err != nil {
			dir = strings.Trim(key, `'`)
		}
		if !filepath.IsAbs(dir) || !sameDirectory(dir, cwd) {
			continue
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			if strings.HasPrefix(next, "[") {
				break
			}
			if k, v, ok := strings.Cut(next, "="); ok && strings.TrimSpace(k) == "trust_level" && strings.TrimSpace(v) == `"trusted"` {
				return true
			}
		}
	}
	return false
}

func TestDesktopSessionCodexResumesTheSavedConversation(t *testing.T) {
	p, _, _ := prepareDesktopCodex(t, codexGrants)
	if _, err := execDesktopSessionArgs(t, p.deps(), "--runtime", "codex", "--codex-session", desktopCodexSession, "--"); err != nil {
		t.Fatalf("desktop-session --runtime codex --codex-session: %v; steps=%v", err, p.steps())
	}
	if !p.ran {
		t.Fatal("Codex was never launched")
	}
	if got, want := strings.Join(p.plan.args, " "), "--no-daemon resume "+desktopCodexSession; got != want {
		t.Fatalf("Codex arguments = %q, want [%s]", p.plan.args, want)
	}
}

func TestDesktopSessionCodexWithoutAChatGPTGrantDoesNotInstall(t *testing.T) {
	p, _, _ := prepareDesktopCodex(t, []map[string]string{{"id": "deepseek-granted", "name": "DeepSeek", "type": "deepseek"}})
	if _, err := execDesktopSessionArgs(t, p.deps(), "--runtime", "codex", "--"); err == nil {
		t.Fatal("desktop-session --runtime codex succeeded without a ChatGPT grant")
	}
	if !p.called("providers") {
		t.Errorf("the grant was decided without asking auth; steps=%v", p.steps())
	}
	if p.called("ensure") {
		t.Fatalf("Codex was installed for a person with no ChatGPT grant; steps=%v", p.steps())
	}
	if p.ran {
		t.Fatal("Codex was launched without a grant")
	}
}

func TestDesktopSessionCodexRefusedAccessAsksNothingFurther(t *testing.T) {
	p, _, _ := prepareDesktopCodex(t, codexGrants)
	p.gateErr = errors.New("access check refused")
	if _, err := execDesktopSessionArgs(t, p.deps(), "--runtime", "codex", "--"); err == nil {
		t.Fatal("desktop-session --runtime codex succeeded although the access check failed")
	}
	for _, step := range []string{"providers", "ensure", "run"} {
		if p.called(step) {
			t.Errorf("a refused token still reached %s; steps=%v", step, p.steps())
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".void-code", "codex", "config.toml")); err == nil {
		t.Error("a refused token still got a Codex config written")
	}
}

// Only the two lifecycle shapes the desktop needs. Anything else is someone
// else's authority over Codex (a model, a sandbox mode, a profile) and is
// refused before any download.
func TestDesktopSessionCodexRejectsForeignArguments(t *testing.T) {
	for name, args := range map[string][]string{
		"arguments after --":               {"--runtime", "codex", "--", "--model", "gpt-5"},
		"a bare resume after --":           {"--runtime", "codex", "--", "resume", desktopCodexSession},
		"a Pi lifecycle flag":              {"--runtime", "codex", "--", "--session", desktopCodexSession},
		"a codex session that is no id":    {"--runtime", "codex", "--codex-session", "--dangerously-bypass-approvals-and-sandbox", "--"},
		"a codex session path":             {"--runtime", "codex", "--codex-session", "../../etc/passwd", "--"},
		"an unknown runtime":               {"--runtime", "claude", "--"},
		"a codex session on a Pi chat":     {"--runtime", "pi", "--codex-session", desktopCodexSession, "--"},
		"a codex session, default runtime": {"--codex-session", desktopCodexSession, "--"},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, _ := prepareDesktopCodex(t, codexGrants)
			if _, err := execDesktopSessionArgs(t, p.deps(), args...); err == nil {
				t.Fatalf("desktop-session %q was accepted", args)
			}
			if p.called("ensure") || p.ran {
				t.Fatalf("desktop-session %q got as far as %v", args, p.steps())
			}
		})
	}
}

// The default stays Pi, and Pi still needs its runtime paths.
func TestDesktopSessionRuntimeFlagDefaultsToPi(t *testing.T) {
	cmd := newDesktopSessionCommand(defaultDesktopSessionDeps())
	flag := cmd.Flags().Lookup("runtime")
	if flag == nil {
		t.Fatal("desktop-session has no --runtime flag")
	}
	if flag.DefValue != "pi" {
		t.Fatalf("--runtime defaults to %q, want pi", flag.DefValue)
	}
	if cmd.Flags().Lookup("codex-session") == nil {
		t.Fatal("desktop-session has no --codex-session flag")
	}
}

func TestDesktopSessionPiStillRequiresItsRuntime(t *testing.T) {
	node, pi := desktopFiles(t)
	for name, args := range map[string][]string{
		"no --node, default runtime":     {"--pi-entry", pi, "--"},
		"no --pi-entry, default runtime": {"--node", node, "--"},
		"no paths, explicit pi":          {"--runtime", "pi", "--"},
	} {
		t.Run(name, func(t *testing.T) {
			p, _, _ := prepareDesktopCodex(t, codexGrants)
			if _, err := execDesktopSessionArgs(t, p.deps(), args...); err == nil {
				t.Fatalf("desktop-session %q started Pi without its runtime paths", args)
			}
			if p.ran {
				t.Fatal("something was launched")
			}
		})
	}
}

func TestDesktopSessionExplicitPiRunsPi(t *testing.T) {
	dir := piSettingsSandbox(t)
	probe := &desktopSeedProbe{settingsPath: filepath.Join(dir, "settings.json")}
	node, pi := desktopFiles(t)
	var plan desktopSessionPlan
	deps := probe.deps()
	deps.run = func(_ context.Context, got desktopSessionPlan, _ io.Reader, _, _ io.Writer) error {
		plan = got
		probe.ran = true
		return nil
	}
	if _, err := execDesktopSessionArgs(t, deps, "--runtime", "pi", "--node", node, "--pi-entry", pi, "--"); err != nil {
		t.Fatalf("desktop-session --runtime pi: %v", err)
	}
	if !probe.ran || plan.nodePath != node || len(plan.args) == 0 || plan.args[0] != pi {
		t.Fatalf("--runtime pi did not launch Pi through node: nodePath=%q args=%q", plan.nodePath, plan.args)
	}
	if v, n := envCount(plan.env, "CODEX_HOME"); n != 0 {
		t.Errorf("a Pi chat got CODEX_HOME=%q", v)
	}
}
