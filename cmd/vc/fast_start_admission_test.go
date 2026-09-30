package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// Fast start, items 2 and 3 (docs/superpowers/specs/2026-09-30-vc-fast-start-design.md,
// with «Уточнение Артёма 30.09»).
//
// The saved /me — ~/.void-code/me-<first 16 hex of sha256(token)>.json, 0600,
// no token inside, written by every successful live /me — marks a token that
// was let in before. It decides the mode of the access check:
//
// Saved /me present (a token let in before) — the check runs BESIDE the start:
//   - the child is spawned while /me has not answered yet;
//   - 401, 402 or an unavailable check (503 twice, the existing retry) stops
//     the running child (its spawn context is cancelled), the terminal is
//     restored after it (CLI), and the command fails with the text authGate
//     gives today (desktop: "authentication unavailable: <that>");
//   - 200 leaves the child alone and its own result comes out;
//   - /me is asked once per vc process, however many runtimes it runs;
//   - the command does not return before the answer is in: a refusal that
//     arrives after the child already ended still fails the command.
//
// No saved /me (first launch for a token, or after a refusal) — the check runs
// BEFORE anything is prepared, exactly as today: the child is spawned only
// after /me answered, and a refusal leaves no trace — nothing new under
// ~/.void-code (no Codex config, no providers cache, no saved /me) or in Pi's
// agent directory, Codex is not installed, nothing is spawned. The desktop's
// no-trace tests are the original ones (desktop_session_*_test.go).
//
// Either mode: no token — nothing is spawned and /me is never asked. A 401 or
// 402 deletes the saved /me (the next launch waits again); an unavailable check
// keeps it (a network blip does not slow the next launch down); a 200 writes it.
//
// The launch notice: with a saved /me it comes from that file as it was before
// this launch; without one it comes from this launch's live answer (the check
// already ran first). Either way the live answer rewrites the file.

// admissionHold is how long a held /me waits for the test before answering on
// its own — so a build that still waits for /me before spawning fails the test
// with a message instead of hanging it.
const admissionHold = 2 * time.Second

// firstLaunchHold is how long /me takes on a first launch, long enough that a
// build starting the runtime beside the check is caught spawning first.
const firstLaunchHold = 300 * time.Millisecond

// pendingMe is a /v1/vc/me whose first answer is held until the test (the
// running child) opens it, or until hold passes. A 503 answers every later
// retry at once.
type pendingMe struct {
	release  chan struct{}
	once     sync.Once
	hold     time.Duration
	status   int
	body     string
	hits     atomic.Int32
	answered atomic.Bool
}

func newPendingMe(status int, body string) *pendingMe {
	return &pendingMe{release: make(chan struct{}), hold: admissionHold, status: status, body: body}
}

func (m *pendingMe) open() { m.once.Do(func() { close(m.release) }) }

func (m *pendingMe) serve(w http.ResponseWriter, _ *http.Request) {
	if m.hits.Add(1) == 1 {
		select {
		case <-m.release:
		case <-time.After(m.hold):
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if m.status != http.StatusOK {
		w.WriteHeader(m.status)
		_, _ = w.Write([]byte(`{}`))
	} else {
		_, _ = w.Write([]byte(m.body))
	}
	m.answered.Store(true)
}

// admissionOutcome is one /me answer and what the command must say about it.
type admissionOutcome struct {
	name    string
	status  int
	refused bool
	meHits  int32 // HTTP requests of the one admission (503 is retried once)
	// keepsSavedMe: whether the saved /me is still there afterwards.
	keepsSavedMe bool
	check        func(t *testing.T, err error)
}

func wantErrText(text string) func(t *testing.T, err error) {
	return func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), text) {
			t.Errorf("error = %v, want the access check's own text %q", err, text)
		}
	}
}

var admissionOutcomes = []admissionOutcome{
	{name: "401", status: http.StatusUnauthorized, refused: true, meHits: 1, keepsSavedMe: false, check: wantErrText("Session token rejected by auth server")},
	{name: "402", status: http.StatusPaymentRequired, refused: true, meHits: 1, keepsSavedMe: false, check: func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, auth.ErrAccessNotGranted) {
			t.Errorf("error = %v, want auth.ErrAccessNotGranted as authGate returns it", err)
		}
	}},
	{name: "unavailable", status: http.StatusServiceUnavailable, refused: true, meHits: 2, keepsSavedMe: true, check: wantErrText("Session verification unavailable; try again")},
	{name: "200", status: http.StatusOK, refused: false, meHits: 1, keepsSavedMe: true},
}

// childUnderAdmission is what a runtime child sees while /me is pending.
type childUnderAdmission struct {
	started         atomic.Bool
	answeredAtStart atomic.Bool
	stopped         atomic.Bool
	neverStopped    atomic.Bool
}

// run plays the runtime: it notes whether /me had answered when it was
// started, lets /me answer, and then either waits to be stopped (a refusal) or
// keeps running a little past the answer and exits (admitted).
func (c *childUnderAdmission) run(ctx context.Context, m *pendingMe, refused bool, stoppedErr error, exit error) error {
	c.started.Store(true)
	c.answeredAtStart.Store(m.answered.Load())
	m.open()
	if refused {
		select {
		case <-ctx.Done():
			c.stopped.Store(true)
			return stoppedErr
		case <-time.After(5 * time.Second):
			c.neverStopped.Store(true)
			return exit
		}
	}
	waitFor(admissionHold+time.Second, m.answered.Load)
	select {
	case <-ctx.Done():
		c.stopped.Store(true)
		return stoppedErr
	case <-time.After(150 * time.Millisecond):
		return exit
	}
}

func (c *childUnderAdmission) assertStartedBeforeAnswer(t *testing.T) {
	t.Helper()
	if !c.started.Load() {
		t.Fatal("the runtime was never started (a token with a saved /me must start beside the access check)")
	}
	if c.answeredAtStart.Load() {
		t.Error("the runtime was started only after /v1/vc/me answered; with a saved /me it must start while the access check is still pending")
	}
}

func (c *childUnderAdmission) assertOutcome(t *testing.T, tc admissionOutcome) {
	t.Helper()
	if tc.refused {
		if c.neverStopped.Load() || !c.stopped.Load() {
			t.Errorf("/me answered %s and the running runtime was not stopped", tc.name)
		}
		return
	}
	if c.stopped.Load() {
		t.Error("/me admitted the token and the runtime was stopped anyway")
	}
}

// ─── the saved /me ──────────────────────────────────────────────────────────

func meSnapshotFile(t *testing.T, token string) string {
	t.Helper()
	return filepath.Join(vcCacheDir(t), "me-"+tokenHash16(token)+".json")
}

func savedMeExists(t *testing.T, token string) bool {
	t.Helper()
	_, err := os.Stat(meSnapshotFile(t, token))
	return err == nil
}

func assertMeSnapshotFile(t *testing.T, token string) {
	t.Helper()
	path := meSnapshotFile(t, token)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the live /me of a successful launch was not saved to %s: %v", path, err)
	}
	if strings.Contains(string(data), token) {
		t.Errorf("%s carries the token: %s", path, data)
	}
	if info, err := os.Stat(path); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("%s mode = %04o, want 0600", path, info.Mode().Perm())
	}
}

func assertSavedMeAfter(t *testing.T, token string, tc admissionOutcome) {
	t.Helper()
	switch exists := savedMeExists(t, token); {
	case tc.keepsSavedMe && !exists:
		t.Errorf("after /me answered %s the saved /me is gone; want it kept", tc.name)
	case !tc.keepsSavedMe && exists:
		t.Errorf("after /me answered %s the saved /me is still there; a refusal must delete it so the next launch waits for the check", tc.name)
	}
}

// ─── CLI: runSpawn ──────────────────────────────────────────────────────────

// setMe swaps what /v1/vc/me answers between launches of one switchLaunch.
func (l *switchLaunch) setMe(handler http.HandlerFunc) {
	l.mu.Lock()
	l.meHandler = handler
	l.mu.Unlock()
}

// cliLaunchWithSavedMe prepares a switchLaunch whose first child belongs to a
// warm-up vc: /me admits it at once, which saves the /me. The children of the
// launch under test follow it. Returns the launch and how many events the
// warm-up left.
func cliLaunchWithSavedMe(t *testing.T, rt string, scripts ...childScript) *switchLaunch {
	t.Helper()
	l := prepareSwitchLaunch(t, meBody(""), append([]childScript{exitWith(0)}, scripts...)...)
	saveRuntimeKey(t, rt)
	if _, err := runSupervisedBounded(t); err != nil {
		t.Fatalf("warm-up launch: %v", err)
	}
	assertMeSnapshotFile(t, switchLaunchToken)
	return l
}

// afterWarmUp drops the warm-up's child and its four terminal events.
func afterWarmUp(t *testing.T, runs []*childRun, events []string) ([]*childRun, []string) {
	t.Helper()
	if len(runs) < 1 || len(events) < 4 {
		t.Fatalf("the warm-up vc did not run one child: children %v, events %v", kinds(runs), events)
	}
	return runs[1:], events[4:]
}

func TestCLIWithSavedMeStartsBeforeAdmissionAnswersAndIsStoppedOnRefusal(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		for _, tc := range admissionOutcomes {
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				m := newPendingMe(tc.status, meBody(""))
				child := &childUnderAdmission{}
				l := cliLaunchWithSavedMe(t, rt, func(ctx context.Context, _ *childRun) error {
					return child.run(ctx, m, tc.refused, errors.New("signal: terminated"), fakeExitError{7})
				})
				l.setMe(m.serve)
				exitCodes := recordExitProcess(t)

				out, err := runSupervisedBounded(t)
				all, allEvents, _, _ := l.snapshot()
				runs, events := afterWarmUp(t, all, allEvents)
				if len(runs) != 1 || runs[0].kind != rt {
					t.Fatalf("children = %v, want one %s (err=%v)\n%s", kinds(runs), rt, err, out)
				}
				child.assertStartedBeforeAnswer(t)
				child.assertOutcome(t, tc)
				if got := m.hits.Load(); got != tc.meHits {
					t.Errorf("/v1/vc/me asked %d times, want %d (one admission per vc)", got, tc.meHits)
				}
				assertTerminalRestoredAfterChild(t, events, rt)
				assertSavedMeAfter(t, switchLaunchToken, tc)

				if !tc.refused {
					if code, ok := exitCodeOf(err); !ok || code != 7 {
						t.Errorf("runSpawn = %v, want the runtime's own exit code 7", err)
					}
					if codes := exitCodes(); len(codes) != 0 {
						t.Errorf("exitProcess(%v) on an admitted launch", codes)
					}
					return
				}
				tc.check(t, err)
				if code, ok := exitCodeOf(err); ok {
					t.Errorf("runSpawn returned the stopped runtime's exit code %d, want the access check's error", code)
				}
				if err != nil && !strings.Contains(out, strings.SplitN(err.Error(), "\n", 2)[0]) {
					t.Errorf("the refusal was not printed as today; printed:\n%s", out)
				}
				for _, code := range exitCodes() {
					if code != 1 {
						t.Errorf("exitProcess(%d) on a refusal, want 1 as today", code)
					}
				}
			})
		}
	}
}

// The admission belongs to the vc process: a switch while /me is pending does
// not ask again, and a refusal that arrives during the second runtime stops
// that one.
func TestCLIWithSavedMeAdmissionSpansARuntimeSwitch(t *testing.T) {
	m := newPendingMe(http.StatusUnauthorized, "")
	second := &childUnderAdmission{}
	var firstSawAnswer atomic.Bool
	l := cliLaunchWithSavedMe(t, "pi",
		func(ctx context.Context, run *childRun) error {
			firstSawAnswer.Store(m.answered.Load())
			return piAsks(t, "codex\n", 0)(ctx, run)
		},
		func(ctx context.Context, _ *childRun) error {
			return second.run(ctx, m, true, errors.New("signal: terminated"), fakeExitError{9})
		},
	)
	l.setMe(m.serve)
	recordExitProcess(t)

	_, err := runSupervisedBounded(t)
	all, allEvents, _, _ := l.snapshot()
	runs, events := afterWarmUp(t, all, allEvents)
	if !equalStrings(kinds(runs), []string{"pi", "codex"}) {
		t.Fatalf("children = %v, want [pi codex] (err=%v)", kinds(runs), err)
	}
	if firstSawAnswer.Load() {
		t.Error("Pi was started only after /v1/vc/me answered")
	}
	if !second.stopped.Load() {
		t.Error("/me refused while Codex ran and Codex was not stopped")
	}
	if got := m.hits.Load(); got != 1 {
		t.Errorf("/v1/vc/me asked %d times over two runtimes, want 1", got)
	}
	wantErrText("Session token rejected by auth server")(t, err)
	var captures, restores int
	for _, e := range events {
		switch e {
		case "capture":
			captures++
		case "restore":
			restores++
		}
	}
	if captures != 2 || restores != 2 {
		t.Errorf("terminal captured %d and restored %d times over two children, want 2 and 2: %v", captures, restores, events)
	}
}

// A runtime that ended before /me answered does not end vc's question: the
// refusal still decides how vc exits.
func TestCLIWithSavedMeRefusalAfterTheRuntimeEndedStillFailsVC(t *testing.T) {
	m := newPendingMe(http.StatusUnauthorized, "")
	l := cliLaunchWithSavedMe(t, "pi", func(context.Context, *childRun) error {
		go func() {
			time.Sleep(30 * time.Millisecond)
			m.open()
		}()
		return nil
	})
	l.setMe(m.serve)
	recordExitProcess(t)

	_, err := runSupervisedBounded(t)
	all, allEvents, _, _ := l.snapshot()
	if runs, _ := afterWarmUp(t, all, allEvents); len(runs) != 1 {
		t.Fatalf("children = %v, want one Pi", kinds(runs))
	}
	wantErrText("Session token rejected by auth server")(t, err)
	if savedMeExists(t, switchLaunchToken) {
		t.Error("a 401 left the saved /me in place")
	}
}

// First launch for a token: nothing is started before /me answers.
func TestCLIWithoutSavedMeWaitsForAdmissionBeforeTheRuntime(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		t.Run(rt, func(t *testing.T) {
			m := newPendingMe(http.StatusOK, meBody(""))
			m.hold = firstLaunchHold
			var answeredAtStart atomic.Bool
			l := prepareSwitchLaunch(t, meBody(""), func(context.Context, *childRun) error {
				answeredAtStart.Store(m.answered.Load())
				return fakeExitError{7}
			})
			l.setMe(m.serve)
			saveRuntimeKey(t, rt)

			_, err := runSupervisedBounded(t)
			runs, _, _, _ := l.snapshot()
			if len(runs) != 1 || runs[0].kind != rt {
				t.Fatalf("children = %v, want one %s (err=%v)", kinds(runs), rt, err)
			}
			if !answeredAtStart.Load() {
				t.Error("with no saved /me the runtime was started before /v1/vc/me answered; a first launch must wait for the check")
			}
			if code, ok := exitCodeOf(err); !ok || code != 7 {
				t.Errorf("runSpawn = %v, want the runtime's exit code 7", err)
			}
			assertMeSnapshotFile(t, switchLaunchToken)
		})
	}
}

// First launch for a token, refused: exactly today's behaviour — nothing is
// prepared, nothing is written, nothing is started.
func TestCLIWithoutSavedMeRefusalLeavesNoTrace(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		for _, tc := range admissionOutcomes {
			if !tc.refused {
				continue
			}
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				m := newPendingMe(tc.status, "")
				m.hold = 0
				l := prepareSwitchLaunch(t, meBody(""))
				l.setMe(m.serve)
				saveRuntimeKey(t, rt)
				recordExitProcess(t)
				piAgentDir := os.Getenv("PI_CODING_AGENT_DIR")
				beforeCache, beforePi := treeFiles(t, vcCacheDir(t)), treeFiles(t, piAgentDir)

				_, err := runSupervisedBounded(t)
				runs, _, _, provHits := l.snapshot()
				if len(runs) != 0 {
					t.Fatalf("children = %v started for a refused token", kinds(runs))
				}
				tc.check(t, err)
				if l.ensure.calls != 0 {
					t.Errorf("Codex was installed (%d calls) for a refused token", l.ensure.calls)
				}
				if provHits != 0 {
					t.Errorf("providers were asked %d times for a refused token", provHits)
				}
				if after := treeFiles(t, vcCacheDir(t)); !equalStrings(after, beforeCache) {
					t.Errorf("a refused token left a trace in ~/.void-code:\nbefore %v\nafter  %v", beforeCache, after)
				}
				if after := treeFiles(t, piAgentDir); !equalStrings(after, beforePi) {
					t.Errorf("a refused token left a trace in Pi's agent directory:\nbefore %v\nafter  %v", beforePi, after)
				}
			})
		}
	}
}

func TestCLIWithoutATokenStartsNothing(t *testing.T) {
	l := prepareSwitchLaunch(t, meBody(""))
	saveRuntimeKey(t, "pi")
	if err := auth.Wipe(); err != nil {
		t.Fatal(err)
	}
	recordExitProcess(t)

	_, err := runSupervisedBounded(t)
	runs, _, meHits, _ := l.snapshot()
	if len(runs) != 0 {
		t.Fatalf("children = %v started without a token", kinds(runs))
	}
	if meHits != 0 {
		t.Errorf("/v1/vc/me asked %d times without a token", meHits)
	}
	wantErrText("Not logged in")(t, err)
}

// treeFiles lists every file under dir, relative and sorted; a missing dir is
// an empty list.
func treeFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	if dir == "" {
		return files
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// recordExitProcess replaces exitProcess with a recorder; the returned func
// yields the codes it was called with.
func recordExitProcess(t *testing.T) func() []int {
	t.Helper()
	var mu sync.Mutex
	var codes []int
	saved := exitProcess
	exitProcess = func(code int) {
		mu.Lock()
		codes = append(codes, code)
		mu.Unlock()
	}
	t.Cleanup(func() { exitProcess = saved })
	return func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), codes...)
	}
}

func assertTerminalRestoredAfterChild(t *testing.T, events []string, kind string) {
	t.Helper()
	want := []string{"capture", "start:" + kind, "end:" + kind, "restore"}
	if !equalStrings(events, want) {
		t.Errorf("events = %v, want %v — the terminal is restored after the child, stopped or not", events, want)
	}
}

// ─── desktop-session ────────────────────────────────────────────────────────

const desktopPiAdmissionToken = "desktop-pi-admission-token"

// meOnlyServer serves /v1/vc/me through handler and nothing else.
func meOnlyServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/me" {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// phasedMe answers the warm-up launch at once with 200 and then hands /me to
// the handler under test.
type phasedMe struct {
	mu   sync.Mutex
	next http.HandlerFunc
}

func (p *phasedMe) set(next http.HandlerFunc) { p.mu.Lock(); p.next = next; p.mu.Unlock() }
func (p *phasedMe) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	next := p.next
	p.mu.Unlock()
	if next != nil {
		next(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(meBody("")))
}

// desktopPiAdmissionDeps is a Pi desktop session with the REAL authGate
// against accessHost.
func desktopPiAdmissionDeps(accessHost string, run func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error) desktopSessionDeps {
	return desktopSessionDeps{
		loadToken: func() (string, error) { return desktopPiAdmissionToken, nil },
		resolveConfig: func() config.Config {
			return config.Config{AuthHost: "http://auth.invalid", AccessCheckHost: accessHost, RelayScheme: "https", RelayHost: "relay.invalid"}
		},
		authGate:        authGate,
		resolveCA:       func(config.Config) (string, error) { return "/ca.pem", nil },
		reconcilePi:     func() (string, error) { return "/managed.ts", nil },
		reconcileSearch: func(bool) (managedWebSearchState, error) { return managedWebSearchReady, nil },
		now:             time.Now,
		run:             run,
	}
}

// desktopLaunch is one runtime's desktop-session wiring with a swappable run.
type desktopLaunch struct {
	deps  desktopSessionDeps
	args  []string
	token string
	mu    sync.Mutex
	run   func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error
}

func (d *desktopLaunch) setRun(run func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error) {
	d.mu.Lock()
	d.run = run
	d.mu.Unlock()
}

// prepareDesktopLaunch wires desktop-session for rt with the real authGate
// against accessHost.
func prepareDesktopLaunch(t *testing.T, rt, accessHost string) *desktopLaunch {
	t.Helper()
	d := &desktopLaunch{}
	dispatch := func(ctx context.Context, plan desktopSessionPlan, in io.Reader, out, errOut io.Writer) error {
		d.mu.Lock()
		run := d.run
		d.mu.Unlock()
		if run == nil {
			return nil
		}
		return run(ctx, plan, in, out, errOut)
	}
	if rt == "pi" {
		piSettingsSandbox(t)
		node, pi := desktopFiles(t)
		d.deps = desktopPiAdmissionDeps(accessHost, dispatch)
		d.args = []string{"--node", node, "--pi-entry", pi, "--"}
		d.token = desktopPiAdmissionToken
		return d
	}
	p, _, _ := prepareDesktopCodex(t, codexGrants)
	d.deps = p.deps()
	d.deps.authGate = authGate
	d.deps.resolveConfig = func() config.Config {
		return config.Config{AuthHost: p.server.URL, AccessCheckHost: accessHost, RelayScheme: "https", RelayHost: "relay.test:9443"}
	}
	d.deps.run = dispatch
	d.args = []string{"--runtime", "codex", "--"}
	d.token = desktopCodexToken
	return d
}

// desktopAdmissionRun is desktop-session's run seam playing the runtime; a
// stop returns what runDesktopSessionProcess returns on a cancelled context.
func desktopAdmissionRun(child *childUnderAdmission, m *pendingMe, refused bool) func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
	return func(ctx context.Context, _ desktopSessionPlan, _ io.Reader, _, _ io.Writer) error {
		return child.run(ctx, m, refused, fmt.Errorf("desktop-session canceled: %w", context.Canceled), desktopProcessExitError{7})
	}
}

func TestDesktopWithSavedMeStartsBeforeAdmissionAnswersAndIsStoppedOnRefusal(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		for _, tc := range admissionOutcomes {
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				phases := &phasedMe{}
				d := prepareDesktopLaunch(t, rt, meOnlyServer(t, phases.serve))
				// Warm-up: a launch /me admits at once saves the /me.
				if _, err := execDesktopSessionArgs(t, d.deps, d.args...); err != nil {
					t.Fatalf("warm-up desktop session: %v", err)
				}
				assertMeSnapshotFile(t, d.token)

				m := newPendingMe(tc.status, meBody(""))
				child := &childUnderAdmission{}
				phases.set(m.serve)
				d.setRun(desktopAdmissionRun(child, m, tc.refused))

				_, err := execDesktopSessionArgs(t, d.deps, d.args...)
				child.assertStartedBeforeAnswer(t)
				child.assertOutcome(t, tc)
				if got := m.hits.Load(); got != tc.meHits {
					t.Errorf("/v1/vc/me asked %d times, want %d", got, tc.meHits)
				}
				assertSavedMeAfter(t, d.token, tc)
				if !tc.refused {
					if code, ok := exitCodeOf(err); !ok || code != 7 {
						t.Errorf("desktop-session = %v, want the runtime's own exit status 7", err)
					}
					return
				}
				tc.check(t, err)
				wantErrText("authentication unavailable")(t, err)
				if err != nil && strings.Contains(err.Error(), "canceled") {
					t.Errorf("desktop-session = %v, want the access check's error, not the stopped runtime's", err)
				}
				if code, ok := exitCodeOf(err); ok {
					t.Errorf("desktop-session returned the stopped runtime's exit status %d, want the access check's error", code)
				}
			})
		}
	}
}

// First desktop chat for a token: the check runs before the runtime, and a
// refusal leaves no saved /me behind. (That nothing else is prepared for a
// refused token is pinned by the original desktop_session_*_test.go tests.)
func TestDesktopWithoutSavedMeWaitsForAdmission(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		for _, tc := range admissionOutcomes {
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				m := newPendingMe(tc.status, meBody(""))
				m.hold = firstLaunchHold
				d := prepareDesktopLaunch(t, rt, meOnlyServer(t, m.serve))
				ran := false
				var answeredAtStart bool
				d.setRun(func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
					ran = true
					answeredAtStart = m.answered.Load()
					return desktopProcessExitError{7}
				})

				_, err := execDesktopSessionArgs(t, d.deps, d.args...)
				if tc.refused {
					if ran {
						t.Fatal("with no saved /me a refused token got its runtime started")
					}
					tc.check(t, err)
					if savedMeExists(t, d.token) {
						t.Error("a refused first launch left a saved /me")
					}
					return
				}
				if !ran {
					t.Fatalf("the runtime was never started: %v", err)
				}
				if !answeredAtStart {
					t.Error("with no saved /me the runtime was started before /v1/vc/me answered")
				}
				if code, ok := exitCodeOf(err); !ok || code != 7 {
					t.Errorf("desktop-session = %v, want exit status 7", err)
				}
				assertMeSnapshotFile(t, d.token)
			})
		}
	}
}

func TestDesktopWithoutATokenStartsNothing(t *testing.T) {
	for _, rt := range []string{"pi", "codex"} {
		t.Run(rt, func(t *testing.T) {
			d := prepareDesktopLaunch(t, rt, "http://check.invalid")
			var gateCalls atomic.Int32
			ran := false
			d.setRun(func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
				ran = true
				return nil
			})
			d.deps.loadToken = func() (string, error) { return "", nil }
			d.deps.authGate = func(string, string, *http.Client) (auth.MeResult, bool, error) {
				gateCalls.Add(1)
				return auth.MeResult{}, false, errors.New("gate asked without a token")
			}

			if _, err := execDesktopSessionArgs(t, d.deps, d.args...); err == nil {
				t.Fatal("desktop-session without a token succeeded")
			}
			if ran {
				t.Fatal("a runtime was started without a token")
			}
			if n := gateCalls.Load(); n != 0 {
				t.Errorf("the access check was asked %d times without a token", n)
			}
		})
	}
}

// ─── the launch notice ──────────────────────────────────────────────────────

// switchableMe answers /me with whatever body is current.
type switchableMe struct {
	mu   sync.Mutex
	body string
}

func (s *switchableMe) set(body string) { s.mu.Lock(); s.body = body; s.mu.Unlock() }
func (s *switchableMe) serve(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	body := s.body
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

var lowWalletMe = meBody(wallet("2", tariffT1, "true", "1"))

// Four terminal launches of Pi:
//  1. no saved /me — the check runs first, the notice is its live answer's;
//  2. saved /me with a low wallet, live /me without — the saved one speaks;
//  3. the second launch's /me replaced the file — no notice;
//  4. saved /me without a wallet, live /me with a low one — still none: the
//     live answer only prepares the next launch.
func TestCLILaunchNoticeComesFromTheSavedMeOrTheFirstLiveAnswer(t *testing.T) {
	me := &switchableMe{}
	l := prepareSwitchLaunch(t, meBody(""), exitWith(0), exitWith(0), exitWith(0), exitWith(0))
	l.setMe(me.serve)
	saveRuntimeKey(t, "pi")

	for i, body := range []string{lowWalletMe, meBody(""), meBody(""), lowWalletMe} {
		me.set(body)
		if _, err := runSupervisedBounded(t); err != nil {
			t.Fatalf("launch %d: %v", i+1, err)
		}
		assertMeSnapshotFile(t, switchLaunchToken)
	}
	runs, _, _, _ := l.snapshot()
	if len(runs) != 4 {
		t.Fatalf("children = %v, want four Pi launches", kinds(runs))
	}
	assertLaunchNoticeEnv(t, runs[0].env, walletLowNotice(1))
	assertLaunchNoticeEnv(t, runs[1].env, walletLowNotice(1))
	assertLaunchNoticeEnv(t, runs[2].env, "")
	assertLaunchNoticeEnv(t, runs[3].env, "")
}

// The desktop's Codex chat says the notice on the command's error stream
// before Codex takes the terminal; same sources, same four launches.
func TestDesktopCodexLaunchNoticeComesFromTheSavedMeOrTheFirstLiveAnswer(t *testing.T) {
	me := &switchableMe{}
	d := prepareDesktopLaunch(t, "codex", meOnlyServer(t, me.serve))
	notice := "vc: " + walletLowNotice(1)

	var streams []string
	for i, body := range []string{lowWalletMe, meBody(""), meBody(""), lowWalletMe} {
		me.set(body)
		stderr, err := execDesktopSessionArgs(t, d.deps, d.args...)
		if err != nil {
			t.Fatalf("launch %d: %v", i+1, err)
		}
		streams = append(streams, stderr)
		assertMeSnapshotFile(t, d.token)
	}
	for i, want := range []bool{true, true, false, false} {
		if got := strings.Contains(streams[i], notice); got != want {
			t.Errorf("launch %d printed the notice = %v, want %v; stderr:\n%s", i+1, got, want, streams[i])
		}
	}
}
