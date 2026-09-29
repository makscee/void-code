package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/runtimechoice"
)

// Step 2 (docs/superpowers/specs/2026-09-29-vc-runtime-switch-design.md):
// `vc` supervises the runtime. A child asks for the other runtime by writing
// the runtime name to $VC_RUNTIME_SWITCH_FILE; vc waits for Pi to close itself,
// stops Codex by cancelling the context it was spawned with (Codex does not
// restore the terminal and never exits on its own), restores the terminal after
// every child, and starts the requested runtime without a new admission.
//
// Seams driven here:
//   spawnHarness              — one call per child; ctx cancellation = "stop it"
//   runtimeSwitchPollInterval — how often the request file is checked
//   captureTerminal           — called before each child, returns its restore
//   terminalResetSequence     — what vc prints after a child to undo its modes

const switchEnv = "VC_RUNTIME_SWITCH_FILE"

// fakeExitError is what a child's non-zero exit looks like to runSpawn: the
// same ExitCode() shape handleExecuteError turns into vc's own exit status.
type fakeExitError struct{ code int }

func (e fakeExitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e fakeExitError) ExitCode() int { return e.code }

type childRun struct {
	kind          string // "pi" or "codex", decided by the executable vc chose
	env           []string
	switchFile    string
	dirExisted    bool
	fileExisted   bool
	ctxDoneAtRun  bool
	cancelled     bool
	cancelledSeen time.Duration
}

// childScript is what one fake child does; it may write a request and block.
type childScript func(ctx context.Context, run *childRun) error

type switchLaunch struct {
	*piLaunch
	fakeCodex string
	userPath  string

	mu        sync.Mutex
	runs      []*childRun
	events    []string
	meHits    int
	provHits  int
	scripts   []childScript
	extraCall bool
	// grantsByHit, when set, is what /v1/vc/providers answers on its n-th call
	// (the last entry repeats); unset means codexGrants every time.
	grantsByHit [][]map[string]string
}

func (l *switchLaunch) event(e string) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *switchLaunch) snapshot() ([]*childRun, []string, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*childRun(nil), l.runs...), append([]string(nil), l.events...), l.meHits, l.provHits
}

// prepareSwitchLaunch builds a home from which both Pi and Codex can start:
// the bundled Node and Pi module (as the Pi launch tests plant them), a fake
// installed Codex, an admission server whose /v1/vc/me answers meJSON and whose
// /v1/vc/providers lists a ChatGPT grant, no terminal, no custom relay CA.
func prepareSwitchLaunch(t *testing.T, meJSON string, scripts ...childScript) *switchLaunch {
	t.Helper()
	l := &switchLaunch{piLaunch: preparePiRuntimeLaunch(t), scripts: scripts}
	t.Setenv("VC_RELAY_CA", "")
	_ = os.Unsetenv("VC_RELAY_CA")
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "0")
	t.Setenv(launchNoticeEnv, staleLaunchNotice)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/vc/me":
			l.mu.Lock()
			l.meHits++
			l.mu.Unlock()
			_, _ = w.Write([]byte(meJSON))
		case "/v1/vc/providers":
			l.mu.Lock()
			hit := l.provHits
			l.provHits++
			grants := codexGrants
			if n := len(l.grantsByHit); n > 0 {
				if hit >= n {
					hit = n - 1
				}
				grants = l.grantsByHit[hit]
			}
			l.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"providers": grants})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("VC_AUTH_HOST", server.URL)
	t.Setenv("VC_ACCESS_CHECK_HOST", server.URL)

	l.userPath = filepath.Join(l.home, "user-tools", "bin")
	if err := os.MkdirAll(l.userPath, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", l.userPath)

	l.fakeCodex = filepath.Join(l.home, "fake-codex-install", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(l.fakeCodex), 0700); err != nil {
		t.Fatal(err)
	}
	writeExecutableFixture(t, l.fakeCodex, "#!/bin/sh\nexit 0\n")
	l.ensure = stubEnsureCodex(t, l.fakeCodex, nil)
	stubRuntimeMenu(t, false, "", nil)

	savedPoll, savedCapture, savedSpawn := runtimeSwitchPollInterval, captureTerminal, spawnHarness
	runtimeSwitchPollInterval = time.Millisecond
	captureTerminal = func() func() {
		l.event("capture")
		return func() { l.event("restore") }
	}
	spawnHarness = func(ctx context.Context, exe string, _ []string, env []string) error {
		run := &childRun{kind: "pi", env: append([]string(nil), env...), ctxDoneAtRun: ctx.Err() != nil}
		if exe == l.fakeCodex {
			run.kind = "codex"
		}
		if v, n := envCount(env, switchEnv); n == 1 {
			run.switchFile = v
			if info, err := os.Stat(filepath.Dir(v)); err == nil && info.IsDir() {
				run.dirExisted = true
			}
			if _, err := os.Lstat(v); err == nil {
				run.fileExisted = true
			}
		}
		l.mu.Lock()
		index := len(l.runs)
		l.runs = append(l.runs, run)
		var script childScript
		if index < len(l.scripts) {
			script = l.scripts[index]
		} else {
			l.extraCall = true
		}
		l.mu.Unlock()
		l.event("start:" + run.kind)
		defer l.event("end:" + run.kind)
		if script == nil {
			t.Errorf("unexpected child #%d (%s) — the script has %d", index+1, run.kind, len(l.scripts))
			return nil
		}
		return script(ctx, run)
	}
	t.Cleanup(func() {
		runtimeSwitchPollInterval, captureTerminal, spawnHarness = savedPoll, savedCapture, savedSpawn
	})
	return l
}

func requestSwitch(t *testing.T, run *childRun, body string) {
	t.Helper()
	if run.switchFile == "" {
		t.Errorf("%s child has no %s to write its request to", run.kind, switchEnv)
		return
	}
	if err := os.WriteFile(run.switchFile, []byte(body), 0600); err != nil {
		t.Errorf("write request: %v", err)
	}
}

// exitWith is a child that asks for nothing and exits with code.
func exitWith(code int) childScript {
	return func(context.Context, *childRun) error {
		if code == 0 {
			return nil
		}
		return fakeExitError{code}
	}
}

// piAsks is Pi's `/runtime <x>`: `vc runtime <x>` wrote the request, then Pi
// shuts itself down. It lingers for a while first, as a Pi finishing its turn
// would, so a supervisor that signals Pi is caught doing it.
func piAsks(t *testing.T, body string, linger time.Duration) childScript {
	return func(ctx context.Context, run *childRun) error {
		requestSwitch(t, run, body)
		deadline := time.Now().Add(linger)
		for time.Now().Before(deadline) {
			if ctx.Err() != nil {
				run.cancelled = true
				return errors.New("signal: terminated")
			}
			time.Sleep(time.Millisecond)
		}
		return nil
	}
}

// codexAsks is Codex's `!vc runtime <x>`: the request is written and Codex
// keeps running — it never exits on its own; vc has to stop it.
func codexAsks(t *testing.T, body string) childScript {
	return func(ctx context.Context, run *childRun) error {
		requestSwitch(t, run, body)
		start := time.Now()
		select {
		case <-ctx.Done():
			run.cancelled = true
			run.cancelledSeen = time.Since(start)
			return errors.New("signal: terminated")
		case <-time.After(5 * time.Second):
			t.Errorf("Codex asked for %q and was never stopped", strings.TrimSpace(body))
			return nil
		}
	}
}

// runSupervised runs `vc` and returns its error plus everything it printed.
func runSupervised(t *testing.T) (string, error) {
	t.Helper()
	stopStderr := captureProcessStderr(t)
	stdout, err := captureStdout(t, func() error { return runSpawn(nil, nil) })
	return plainText(stdout + stopStderr()), err
}

func exitCodeOf(err error) (int, bool) {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode(), true
	}
	return 0, false
}

func kinds(runs []*childRun) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.kind
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ─── the loop ───────────────────────────────────────────────────────────────

func TestSupervisorWithoutARequestRunsOneChildAndReturnsItsExitCode(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), exitWith(7))
	saveRuntimeKey(t, "pi")

	_, err := runSupervised(t)
	runs, events, meHits, _ := l.snapshot()
	if len(runs) != 1 || runs[0].kind != "pi" {
		t.Fatalf("children = %v, want exactly one Pi", kinds(runs))
	}
	if code, ok := exitCodeOf(err); !ok || code != 7 {
		t.Fatalf("runSpawn returned %v, want the child's exit code 7 carried out", err)
	}
	run := runs[0]
	if run.switchFile == "" || !filepath.IsAbs(run.switchFile) {
		t.Fatalf("%s = %q in Pi's environment, want exactly one absolute path", switchEnv, run.switchFile)
	}
	if !run.dirExisted {
		t.Error("the request directory did not exist while the child ran")
	}
	if run.fileExisted {
		t.Error("a request was already waiting when the child started")
	}
	if _, err := os.Lstat(filepath.Dir(run.switchFile)); err == nil {
		t.Errorf("request directory %s outlived vc", filepath.Dir(run.switchFile))
	}
	if want := []string{"capture", "start:pi", "end:pi", "restore"}; !equalStrings(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if meHits != 1 {
		t.Errorf("admission asked %d times, want 1", meHits)
	}
}

func TestPiRequestingCodexIsFollowedByCodexInTheSameVC(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), piAsks(t, "codex\n", 0), exitWith(5))
	saveRuntimeKey(t, "pi")

	out, err := runSupervised(t)
	runs, events, meHits, provHits := l.snapshot()
	if !equalStrings(kinds(runs), []string{"pi", "codex"}) {
		t.Fatalf("children = %v, want [pi codex]", kinds(runs))
	}
	if code, ok := exitCodeOf(err); !ok || code != 5 {
		t.Fatalf("runSpawn returned %v, want the last child's exit code 5", err)
	}
	pi, codex := runs[0], runs[1]
	if pi.cancelled {
		t.Error("vc stopped Pi; Pi closes itself")
	}
	if codex.fileExisted {
		t.Error("the handled request was still in place when Codex started")
	}
	if codex.ctxDoneAtRun {
		t.Error("Codex was started with an already cancelled context")
	}
	want := []string{"capture", "start:pi", "end:pi", "restore", "capture", "start:codex", "end:codex", "restore"}
	if !equalStrings(events, want) {
		t.Errorf("events = %v, want %v — the terminal is captured before and restored after every child", events, want)
	}
	if meHits != 1 {
		t.Errorf("admission asked %d times for two runtimes, want 1", meHits)
	}
	if provHits < 1 {
		t.Error("Codex started without its ChatGPT grant being checked")
	}
	if !strings.Contains(out, "переключаюсь на Codex") {
		t.Errorf("vc did not say it is switching to Codex; printed:\n%s", out)
	}
	if l.ensure.calls != 1 {
		t.Errorf("ensureCodexRuntime called %d times, want 1", l.ensure.calls)
	}

	// Both runtimes can ask: the request file reaches Pi and Codex alike.
	for _, run := range runs {
		if _, n := envCount(run.env, switchEnv); n != 1 {
			t.Errorf("%s got %s %d times, want once", run.kind, switchEnv, n)
		}
	}
	// `!vc runtime pi` inside Codex needs `vc` on Codex's PATH, first.
	path, n := envCount(codex.env, "PATH")
	if n != 1 {
		t.Fatalf("Codex got PATH %d times", n)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	entries := strings.Split(path, string(os.PathListSeparator))
	if len(entries) == 0 || !sameDirectory(entries[0], filepath.Dir(self)) {
		t.Errorf("Codex PATH = %q, want it to start with vc's own directory %s", path, filepath.Dir(self))
	}
	foundUser := false
	for _, e := range entries[1:] {
		if e == l.userPath {
			foundUser = true
		}
	}
	if !foundUser {
		t.Errorf("Codex PATH = %q lost the person's own PATH %s", path, l.userPath)
	}
}

// Codex dies at once on SIGTERM and leaves the terminal raw; it never exits on
// its own after `!vc runtime pi`. vc stops it and restores the terminal itself.
func TestCodexRequestingPiIsStoppedByVCAndPiStarts(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), codexAsks(t, "pi\n"), exitWith(0))
	saveRuntimeKey(t, "codex")

	out, err := runSupervised(t)
	runs, events, _, _ := l.snapshot()
	if !equalStrings(kinds(runs), []string{"codex", "pi"}) {
		t.Fatalf("children = %v, want [codex pi]", kinds(runs))
	}
	if !runs[0].cancelled {
		t.Fatal("Codex asked to switch and vc did not stop it")
	}
	if runs[1].ctxDoneAtRun {
		t.Error("Pi was started with an already cancelled context")
	}
	if err != nil {
		t.Fatalf("runSpawn = %v after Pi exited 0", err)
	}
	want := []string{"capture", "start:codex", "end:codex", "restore", "capture", "start:pi", "end:pi", "restore"}
	if !equalStrings(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if !strings.Contains(out, "переключаюсь на Pi") {
		t.Errorf("vc did not say it is switching to Pi; printed:\n%s", out)
	}
}

// Pi closes itself through ctx.shutdown() after `/runtime`, finishing its turn
// and restoring the terminal; vc must not signal it while it does.
func TestPiRequestingCodexIsNotStoppedByVC(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), piAsks(t, "codex\n", 100*time.Millisecond), exitWith(0))
	saveRuntimeKey(t, "pi")

	if _, err := runSupervised(t); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	runs, _, _, _ := l.snapshot()
	if len(runs) == 0 {
		t.Fatal("nothing was started")
	}
	if runs[0].cancelled {
		t.Fatal("vc stopped Pi after its request; Pi closes itself")
	}
	if !equalStrings(kinds(runs), []string{"pi", "codex"}) {
		t.Fatalf("children = %v, want [pi codex] once Pi closed itself", kinds(runs))
	}
}

func TestRequestForTheCurrentRuntimeOrGarbageDoesNotSwitch(t *testing.T) {
	for _, tc := range []struct {
		current, body string
	}{
		{"pi", "pi\n"},
		{"pi", "claude\n"},
		{"pi", "garbage"},
		{"pi", ""},
		{"codex", "codex\n"},
		{"codex", "foo\n"},
	} {
		t.Run(tc.current+" asked "+fmt.Sprintf("%q", tc.body), func(t *testing.T) {
			// The child lingers for many poll intervals so a wrongful stop shows,
			// then exits 3 on its own.
			child := func(ctx context.Context, run *childRun) error {
				if err := piAsks(t, tc.body, 60*time.Millisecond)(ctx, run); err != nil {
					return err
				}
				return fakeExitError{3}
			}
			l := prepareSwitchLaunch(t, meBody(""), child)
			saveRuntimeKey(t, tc.current)

			_, err := runSupervised(t)
			runs, _, _, _ := l.snapshot()
			if len(runs) != 1 {
				t.Fatalf("children = %v, want one %s and no switch", kinds(runs), tc.current)
			}
			if runs[0].kind != tc.current {
				t.Fatalf("child = %s, want %s", runs[0].kind, tc.current)
			}
			if runs[0].cancelled {
				t.Fatalf("vc stopped %s over a request that is not a switch", tc.current)
			}
			if code, ok := exitCodeOf(err); !ok || code != 3 {
				t.Fatalf("runSpawn = %v, want the child's exit code 3", err)
			}
		})
	}
}

// Admission and the wallet notice belong to the vc process, not to each
// runtime; the ChatGPT grant is a live question for every Codex start.
func TestAdmissionOncePerVCAndGrantPerCodexStart(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(wallet("2", tariffT1, "true", "1")),
		piAsks(t, "codex\n", 0),
		codexAsks(t, "pi\n"),
		piAsks(t, "codex\n", 0),
		exitWith(0),
	)
	saveRuntimeKey(t, "pi")

	if _, err := runSupervised(t); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	runs, events, meHits, provHits := l.snapshot()
	if !equalStrings(kinds(runs), []string{"pi", "codex", "pi", "codex"}) {
		t.Fatalf("children = %v, want [pi codex pi codex]", kinds(runs))
	}
	if meHits != 1 {
		t.Errorf("admission (/v1/vc/me) asked %d times over four runtimes, want 1", meHits)
	}
	if provHits != 2 {
		t.Errorf("ChatGPT grant asked %d times over two Codex starts, want 2", provHits)
	}
	if l.ensure.calls != 2 {
		t.Errorf("ensureCodexRuntime called %d times over two Codex starts, want 2", l.ensure.calls)
	}
	assertLaunchNoticeEnv(t, runs[0].env, walletLowNotice(1))
	assertLaunchNoticeEnv(t, runs[2].env, "")
	var captures, restores int
	for _, e := range events {
		switch e {
		case "capture":
			captures++
		case "restore":
			restores++
		}
	}
	if captures != 4 || restores != 4 {
		t.Errorf("terminal captured %d and restored %d times over four children, want 4 and 4: %v", captures, restores, events)
	}
}

// After Codex is killed the terminal is left in its alternate screen, raw, with
// the cursor hidden and every input mode it enabled; vc undoes each of them.
func TestTerminalResetSequenceUndoesEveryModeARuntimeLeaves(t *testing.T) {
	for what, seq := range map[string]string{
		"leave the alternate screen": "\x1b[?1049l",
		"show the cursor":            "\x1b[?25h",
		"bracketed paste off":        "\x1b[?2004l",
		"mouse clicks off":           "\x1b[?1000l",
		"mouse drag off":             "\x1b[?1002l",
		"mouse motion off":           "\x1b[?1003l",
		"SGR mouse off":              "\x1b[?1006l",
	} {
		if !strings.Contains(terminalResetSequence, seq) {
			t.Errorf("terminalResetSequence does not %s (%q)", what, seq)
		}
	}
	if !regexp.MustCompile(`\x1b\[<\d*u`).MatchString(terminalResetSequence) {
		t.Error("terminalResetSequence does not pop the kitty keyboard mode (CSI < u)")
	}
}

// ─── `vc runtime` inside a session ──────────────────────────────────────────

type sessionCase struct {
	dir, file string
}

// inSession is the environment `vc runtime` sees when typed inside a runtime vc
// launched: the request path, and VC_HARNESS naming the running runtime.
func inSession(t *testing.T, current string) sessionCase {
	t.Helper()
	withTempHome(t)
	dir := t.TempDir()
	s := sessionCase{dir: dir, file: filepath.Join(dir, "switch")}
	t.Setenv(switchEnv, s.file)
	t.Setenv("VC_HARNESS", current)
	saveRuntimeKey(t, current)
	return s
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func execVCCapturingAll(t *testing.T, args ...string) (string, error) {
	t.Helper()
	stopStderr := captureProcessStderr(t)
	var cobraOut string
	stdout, err := captureStdout(t, func() error {
		out, err := execVC(t, args...)
		cobraOut = out
		return err
	})
	return plainText(cobraOut + stdout + stopStderr()), err
}

func TestRuntimeCommandInsideASessionWritesTheRequestAndSaves(t *testing.T) {
	s := inSession(t, "pi")
	menu := stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

	out, err := execVCCapturingAll(t, "runtime", "codex")
	if err != nil {
		t.Fatalf("vc runtime codex inside Pi: %v", err)
	}
	data, readErr := os.ReadFile(s.file)
	if readErr != nil {
		t.Fatalf("no request was written to %s: %v", s.file, readErr)
	}
	if string(data) != "codex\n" {
		t.Fatalf("request = %q, want %q", data, "codex\n")
	}
	// Written atomically: the finished request and nothing half-written beside it.
	if names := dirNames(t, s.dir); !equalStrings(names, []string{"switch"}) {
		t.Errorf("request directory holds %v, want only [switch]", names)
	}
	if got, _ := configKey(t, "runtime"); got != "codex" {
		t.Errorf("runtime = %q after `vc runtime codex` inside a session, want codex saved", got)
	}
	if menu.calls != 0 {
		t.Errorf("menu opened %d times", menu.calls)
	}
	if !strings.Contains(out, "переключаюсь на Codex") {
		t.Errorf("vc runtime codex did not say it is switching; printed:\n%s", out)
	}
}

// Pi's `/runtime` closes Pi when `vc runtime` succeeds. Asking for the runtime
// already running must therefore fail: a success would close Pi with no
// request behind it, and vc would exit instead of switching.
func TestRuntimeCommandInsideASessionRefusesTheRuntimeAlreadyRunning(t *testing.T) {
	for _, current := range []string{"pi", "codex"} {
		t.Run(current, func(t *testing.T) {
			s := inSession(t, current)
			before := configBytes(t)
			stubRuntimeMenu(t, true, runtimechoice.Pi, nil)

			out, err := execVCCapturingAll(t, "runtime", current)
			if err == nil {
				t.Fatalf("vc runtime %s inside %s returned nil", current, current)
			}
			if !strings.Contains(strings.ToLower(out+err.Error()), "уже") {
				t.Errorf("vc runtime %s inside %s did not say it is already running: %v\n%s", current, current, err, out)
			}
			if _, statErr := os.Lstat(s.file); statErr == nil {
				t.Fatal("a request for the running runtime was written")
			}
			if after := configBytes(t); !bytes.Equal(before, after) {
				t.Errorf("config changed:\nbefore: %q\nafter:  %q", before, after)
			}
		})
	}
}

// Inside a session the runtime owns the terminal: no menu, a hint instead.
func TestRuntimeCommandWithoutAValueInsideASessionAsksForOne(t *testing.T) {
	s := inSession(t, "pi")
	before := configBytes(t)
	menu := stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

	out, err := execVCCapturingAll(t, "runtime")
	if err == nil {
		t.Fatal("vc runtime with no value inside a session returned nil")
	}
	text := out + err.Error()
	for _, hint := range []string{"vc runtime pi", "vc runtime codex"} {
		if !strings.Contains(text, hint) {
			t.Errorf("error does not suggest %q: %v\n%s", hint, err, out)
		}
	}
	if menu.calls != 0 {
		t.Fatalf("menu opened %d times inside a session", menu.calls)
	}
	if _, statErr := os.Lstat(s.file); statErr == nil {
		t.Fatal("a request was written without a value")
	}
	if after := configBytes(t); !bytes.Equal(before, after) {
		t.Errorf("config changed:\nbefore: %q\nafter:  %q", before, after)
	}
}

func TestRuntimeCommandInsideASessionRefusesAnUnknownValue(t *testing.T) {
	s := inSession(t, "pi")
	stubRuntimeMenu(t, true, runtimechoice.Codex, nil)

	if _, err := execVCCapturingAll(t, "runtime", "claude"); err == nil {
		t.Fatal("vc runtime claude inside a session returned nil")
	}
	if _, statErr := os.Lstat(s.file); statErr == nil {
		t.Fatal("a request was written for an unknown runtime")
	}
}

func TestRuntimeCommandOutsideASessionWritesNoRequest(t *testing.T) {
	withTempHome(t)
	dir := t.TempDir()
	t.Setenv(switchEnv, "")
	_ = os.Unsetenv(switchEnv)
	t.Setenv("VC_HARNESS", "")
	_ = os.Unsetenv("VC_HARNESS")
	stubRuntimeMenu(t, true, runtimechoice.Pi, nil)
	t.Chdir(dir) // a relative request path would land here

	if _, err := execVCCapturingAll(t, "runtime", "codex"); err != nil {
		t.Fatalf("vc runtime codex outside a session: %v", err)
	}
	if got, _ := configKey(t, "runtime"); got != "codex" {
		t.Fatalf("runtime = %q, want codex saved", got)
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Fatalf("outside a session vc still wrote %v", names)
	}
}

// ─── Pi's `/runtime` command ────────────────────────────────────────────────

// The managed extension registers `/runtime` only when vc supervises the
// session (VC_RUNTIME_SWITCH_FILE set; the desktop never sets it), runs
// `$VC_BOOTSTRAP_EXECUTABLE runtime <x>`, shuts Pi down on success and
// notifies on failure.
func TestPiExtensionRegistersRuntimeCommandOnlyUnderTheSwitchFile(t *testing.T) {
	src := piVoidCodexExtensionSource
	guard := strings.Index(src, "process.env.VC_RUNTIME_SWITCH_FILE")
	register := strings.Index(src, `registerCommand("runtime"`)
	if guard < 0 {
		t.Fatal("the managed Pi extension never reads process.env.VC_RUNTIME_SWITCH_FILE")
	}
	if register < 0 {
		t.Fatal(`the managed Pi extension does not registerCommand("runtime", …)`)
	}
	if strings.Count(src, `registerCommand("runtime"`) != 1 {
		t.Error(`registerCommand("runtime" appears more than once`)
	}
	if guard > register {
		t.Error("`/runtime` is registered before the VC_RUNTIME_SWITCH_FILE check, so the desktop would get it too")
	}
	body := src[register:]
	for _, want := range []string{
		"VC_BOOTSTRAP_EXECUTABLE",
		`"runtime"`,
		`"pi"`,
		`"codex"`,
		"ctx.shutdown()",
		"ctx.ui.notify(",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the /runtime command does not contain %q", want)
		}
	}
	if run, shutdown := strings.Index(body, "VC_BOOTSTRAP_EXECUTABLE"), strings.Index(body, "ctx.shutdown()"); run >= 0 && shutdown >= 0 && shutdown < run {
		t.Error("ctx.shutdown() comes before `vc runtime` is run; Pi must close only after it succeeded")
	}
}

// ─── a switch that cannot be prepared ───────────────────────────────────────

var noChatGPTGrant = []map[string]string{{"id": "deepseek-granted", "name": "DeepSeek", "type": "deepseek"}}

// piSwitchesTo is what `/runtime <x>` does in Pi: `vc runtime <x>` saves the
// choice and writes the request, then Pi closes itself.
func piSwitchesTo(t *testing.T, target string) childScript {
	return func(ctx context.Context, run *childRun) error {
		if err := config.WriteConfigFile(map[string]string{"runtime": target}); err != nil {
			t.Errorf("save runtime: %v", err)
		}
		requestSwitch(t, run, target+"\n")
		return nil
	}
}

// runSupervisedBounded is runSupervised with a deadline, so a supervisor that
// loops between two failing runtimes fails the test instead of hanging it.
func runSupervisedBounded(t *testing.T) (string, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runSupervised(t)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(20 * time.Second):
		t.Fatal("runSpawn did not return: the supervisor loops between runtimes that cannot start")
		return "", nil
	}
}

// Switching to Codex without a ChatGPT grant must not throw the person out of
// vc: they are told why, get their Pi back in the same vc, and the saved choice
// goes back to Pi so the next `vc` does not fail the same way.
func TestFailedSwitchToCodexWithoutAGrantFallsBackToPi(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), piSwitchesTo(t, "codex"), exitWith(4))
	l.grantsByHit = [][]map[string]string{noChatGPTGrant}
	saveRuntimeKey(t, "pi")

	out, err := runSupervisedBounded(t)
	runs, events, meHits, provHits := l.snapshot()
	if !equalStrings(kinds(runs), []string{"pi", "pi"}) {
		t.Fatalf("children = %v, want [pi pi]: the failed switch relaunches Pi", kinds(runs))
	}
	if code, ok := exitCodeOf(err); !ok || code != 4 {
		t.Fatalf("runSpawn = %v, want the relaunched Pi's exit code 4", err)
	}
	if provHits < 1 {
		t.Error("the Codex grant was never asked")
	}
	if !strings.Contains(out, "vc runtime pi") {
		t.Errorf("vc did not say why Codex could not start (the no-grant reason); printed:\n%s", out)
	}
	if got, _ := configKey(t, "runtime"); got != "pi" {
		t.Errorf("saved runtime = %q after the switch to Codex failed, want pi restored", got)
	}
	if meHits != 1 {
		t.Errorf("admission asked %d times, want 1", meHits)
	}
	want := []string{"capture", "start:pi", "end:pi", "restore", "capture", "start:pi", "end:pi", "restore"}
	if !equalStrings(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if runs[1].switchFile == "" {
		t.Error("the fallback Pi has no request file: it could never switch again")
	}
}

func TestFailedCodexInstallOnSwitchFallsBackToPi(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""), piSwitchesTo(t, "codex"), exitWith(0))
	l.ensure.path, l.ensure.err = "", errors.New("sha256 mismatch for codex-package")
	saveRuntimeKey(t, "pi")

	out, err := runSupervisedBounded(t)
	runs, _, _, _ := l.snapshot()
	if !equalStrings(kinds(runs), []string{"pi", "pi"}) {
		t.Fatalf("children = %v, want [pi pi]", kinds(runs))
	}
	if err != nil {
		t.Fatalf("runSpawn = %v after the fallback Pi exited 0", err)
	}
	if !strings.Contains(out, "sha256 mismatch for codex-package") {
		t.Errorf("vc did not print the install error; printed:\n%s", out)
	}
	if got, _ := configKey(t, "runtime"); got != "pi" {
		t.Errorf("saved runtime = %q after the Codex install failed, want pi restored", got)
	}
}

// The fallback is an ordinary supervised child: once the grant exists, the
// same vc can switch again.
func TestFallbackChildCanSwitchAgain(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""),
		piSwitchesTo(t, "codex"),
		piSwitchesTo(t, "codex"),
		exitWith(0),
	)
	l.grantsByHit = [][]map[string]string{noChatGPTGrant, codexGrants}
	saveRuntimeKey(t, "pi")

	if _, err := runSupervisedBounded(t); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	runs, _, _, provHits := l.snapshot()
	if !equalStrings(kinds(runs), []string{"pi", "pi", "codex"}) {
		t.Fatalf("children = %v, want [pi pi codex]", kinds(runs))
	}
	if provHits != 2 {
		t.Errorf("grant asked %d times, want 2 (once per Codex start)", provHits)
	}
	if got, _ := configKey(t, "runtime"); got != "codex" {
		t.Errorf("saved runtime = %q after the second switch succeeded, want codex", got)
	}
}

// When the runtime to fall back to cannot start either, vc gives up with that
// error instead of bouncing between two runtimes that both fail.
func TestFallbackThatAlsoFailsEndsVCWithAnError(t *testing.T) {
	var l *switchLaunch
	breakPiAndSwitch := func(ctx context.Context, run *childRun) error {
		// Pi's runtime disappears while it runs (no source can put it back in
		// tests), so falling back to Pi cannot be prepared either.
		if err := os.RemoveAll(filepath.Join(l.home, ".void-code", "runtime")); err != nil {
			t.Errorf("remove Pi runtime: %v", err)
		}
		return piSwitchesTo(t, "codex")(ctx, run)
	}
	l = prepareSwitchLaunch(t, meBody(""), breakPiAndSwitch)
	l.grantsByHit = [][]map[string]string{noChatGPTGrant}
	saveRuntimeKey(t, "pi")

	_, err := runSupervisedBounded(t)
	runs, _, _, provHits := l.snapshot()
	if err == nil {
		t.Fatal("runSpawn returned nil although neither Codex nor the fallback Pi could start")
	}
	if len(runs) != 1 {
		t.Fatalf("children = %v, want only the first Pi", kinds(runs))
	}
	if provHits > 1 {
		t.Errorf("Codex was prepared %d times; after the fallback failed vc must stop, not retry", provHits)
	}
	if l.ensure.calls > 1 {
		t.Errorf("ensureCodexRuntime called %d times; at most one attempt", l.ensure.calls)
	}
}
