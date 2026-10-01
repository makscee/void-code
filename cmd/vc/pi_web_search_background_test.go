package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// void-works#89: "vc opens Pi without waiting for npm to install web search".
//
// The managed web-search extension needs `npm ci` the first time (and after
// every fork version bump). On Windows that took 58–74 s before Pi appeared,
// and when it failed with ECONNRESET Pi started without web search anyway. So
// the install moves off the launch path:
//
//   - Pi is spawned without waiting for the install;
//   - the install still runs, concurrently with Pi;
//   - after Pi exits, vc waits for an install still in flight, at most
//     managedWebSearchInstallGrace, then cancels it through its context and
//     waits for it to unwind. Letting it finish is what makes the next launch
//     have web search; the bound is what keeps a hung npm from holding the
//     terminal after the user quit Pi; waiting for the unwind is what keeps a
//     goroutine from publishing into a directory nobody owns any more;
//   - an installed, current extension costs no install at all (today's path);
//   - a failed install leaves Pi alone, says so on stderr once Pi has exited
//     (never into Pi's TUI), is not retried in the same launch, and is retried
//     by the next one;
//   - the published directory is never half installed: dependencies go into a
//     sibling `.pi-web-access-stage-*` and are swapped in only when complete.
//
// Seams this file relies on, and their contract:
//
//	installManagedWebSearchDependencies func(ctx context.Context, dir string) error
//	    the npm step, fed the staging directory; ctx is cancelled when the
//	    grace after Pi's exit runs out. (Today it takes no context.)
//	managedWebSearchInstallGrace time.Duration
//	    how long vc waits after Pi exits for an install in flight before it
//	    cancels it. Default: positive and at most two minutes.
//	spawnHarness                       the CLI's Pi spawn (existing)
//	desktopSessionDeps.run             the desktop's Pi run (existing)
//
// No test here touches the network: the npm step is always the probe below.

// webSearchInstallProbe stands in for `npm ci`. It blocks until released or
// cancelled, and records what vc did with it.
type webSearchInstallProbe struct {
	mu        sync.Mutex
	calls     int
	dirs      []string
	returned  bool
	cancelled bool
	// atStart is what the published path looked like when npm started.
	atStart []webSearchPublishedState

	started chan string
	release chan struct{}
	failure error
}

type webSearchPublishedState struct {
	exists  bool
	version string
	current bool
}

func newWebSearchInstallProbe() *webSearchInstallProbe {
	return &webSearchInstallProbe{started: make(chan string, 8), release: make(chan struct{})}
}

func (p *webSearchInstallProbe) install(ctx context.Context, dir string) error {
	published := publishedWebSearchState()
	p.mu.Lock()
	p.calls++
	p.dirs = append(p.dirs, dir)
	p.atStart = append(p.atStart, published)
	p.mu.Unlock()
	p.started <- dir
	defer func() {
		p.mu.Lock()
		p.returned = true
		p.mu.Unlock()
	}()
	select {
	case <-p.release:
	case <-ctx.Done():
		p.mu.Lock()
		p.cancelled = true
		p.mu.Unlock()
		return ctx.Err()
	}
	if p.failure != nil {
		return p.failure
	}
	readability := filepath.Join(dir, "node_modules", "@mozilla", "readability")
	if err := os.MkdirAll(readability, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(readability, "package.json"), []byte(`{"name":"@mozilla/readability","version":"0.6.0"}`), 0600)
}

func (p *webSearchInstallProbe) snapshot() (calls int, returned, cancelled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.returned, p.cancelled
}

func publishedWebSearchState() webSearchPublishedState {
	path := managedWebSearchPackagePath()
	var state webSearchPublishedState
	if _, err := os.Stat(path); err == nil {
		state.exists = true
	}
	if data, err := os.ReadFile(filepath.Join(path, "package.json")); err == nil {
		var manifest struct{ Version string }
		_ = json.Unmarshal(data, &manifest)
		state.version = manifest.Version
	}
	current, _, _ := inspectManagedWebSearchPackage(path)
	state.current = current
	return state
}

// useWebSearchInstallProbe swaps the npm step for the probe. Registered before
// any launch, so its restore runs after every launch below has been waited for.
func useWebSearchInstallProbe(t *testing.T, p *webSearchInstallProbe) {
	t.Helper()
	saved := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = p.install
	t.Cleanup(func() { installManagedWebSearchDependencies = saved })
}

func setWebSearchInstallGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	saved := managedWebSearchInstallGrace
	managedWebSearchInstallGrace = grace
	t.Cleanup(func() { managedWebSearchInstallGrace = saved })
}

// webSearchCLILaunch prepares an admitted CLI launch with web search enabled
// and nothing installed, and returns the Pi agent directory.
func webSearchCLILaunch(t *testing.T) string {
	t.Helper()
	home, _ := preparePiPathLaunch(t)
	dir := filepath.Join(home, "pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "")
	return dir
}

// stderrToFile points os.Stderr at a file that can be read at any moment, so a
// test can tell what was written while Pi was running from what came after.
func stderrToFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stderr.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = saved
		_ = f.Close()
	})
	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// piChild is a Pi stand-in that stays "running" until exit is closed.
type piChild struct {
	spawned chan struct{}
	exit    chan struct{}
	once    sync.Once
	// observe runs inside the child, while Pi would be running.
	observe func()
}

func newPiChild() *piChild {
	return &piChild{spawned: make(chan struct{}), exit: make(chan struct{})}
}

func (c *piChild) run() error {
	if c.observe != nil {
		c.observe()
	}
	close(c.spawned)
	<-c.exit
	return nil
}

func (c *piChild) quit() { c.once.Do(func() { close(c.exit) }) }

func (c *piChild) useAsCLISpawn(t *testing.T) {
	t.Helper()
	saved := spawnHarness
	spawnHarness = func(context.Context, string, []string, []string) error { return c.run() }
	t.Cleanup(func() { spawnHarness = saved })
}

// launchInBackground runs launch on its own goroutine. Its cleanup releases
// everything a launch could be stuck on — today's code blocks in npm before
// Pi, so a red run must still end — and waits for the launch to return before
// the temp directories go away.
func launchInBackground(t *testing.T, launch func() error, child *piChild, probe *webSearchInstallProbe) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		done <- launch()
		close(finished)
	}()
	t.Cleanup(func() {
		select {
		case <-probe.release:
		default:
			close(probe.release)
		}
		child.quit()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("launch still running 10s after everything was released")
		}
	})
	return done
}

func await[T any](t *testing.T, ch <-chan T, within time.Duration, failure string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(within):
		t.Fatal(failure)
	}
	var zero T
	return zero
}

func assertNoStagingLeft(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(managedWebSearchPackagePath()))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pi-web-access-stage-") || strings.HasPrefix(entry.Name(), ".pi-web-access-backup-") {
			t.Errorf("leftover %s next to the managed package", entry.Name())
		}
	}
}

func webSearchRegistered(t *testing.T, agentDir string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if os.IsNotExist(err) {
		return false
	}
	// Errorf, not Fatal: this also runs inside the Pi stand-in, off the test
	// goroutine.
	if err != nil {
		t.Errorf("read settings.json: %v", err)
		return false
	}
	var settings struct{ Packages []any }
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Errorf("settings.json: %v\n%s", err, data)
		return false
	}
	for _, item := range settings.Packages {
		if item == managedWebSearchPackagePath() {
			return true
		}
	}
	return false
}

// TestRunSpawnStartsPiWithoutWaitingForWebSearchInstall: on a fresh machine Pi
// comes up while npm is still running, npm runs alongside Pi, and vc stays
// until it has finished so the next launch finds web search installed.
func TestRunSpawnStartsPiWithoutWaitingForWebSearchInstall(t *testing.T) {
	agentDir := webSearchCLILaunch(t)
	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	var atSpawn webSearchPublishedState
	var registeredAtSpawn bool
	var installReturnedAtSpawn bool
	child.observe = func() {
		atSpawn = publishedWebSearchState()
		registeredAtSpawn = webSearchRegistered(t, agentDir)
		_, installReturnedAtSpawn, _ = probe.snapshot()
	}
	child.useAsCLISpawn(t)
	done := launchInBackground(t, func() error { return runSpawn(nil, nil) }, child, probe)

	await(t, child.spawned, 5*time.Second, "Pi was not spawned while the web-search install was still blocked: vc waits for npm before starting Pi")
	if installReturnedAtSpawn {
		t.Fatal("the install had already returned when Pi started, though it was never released")
	}
	if atSpawn.exists {
		t.Errorf("managed web-search path existed before its install finished: %+v", atSpawn)
	}
	if registeredAtSpawn && !atSpawn.current {
		t.Error("settings.json pointed Pi at a web-search package that is not installed yet")
	}
	await(t, probe.started, 5*time.Second, "the web-search install never started while Pi was running")

	child.quit()
	select {
	case err := <-done:
		t.Fatalf("vc returned (%v) while the web-search install was still in flight; the next launch would not have it", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(probe.release)
	if err := await(t, done, 5*time.Second, "vc did not return after the web-search install finished"); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	calls, _, cancelled := probe.snapshot()
	if calls != 1 || cancelled {
		t.Fatalf("install calls=%d cancelled=%v, want one install run to completion", calls, cancelled)
	}
	if state := publishedWebSearchState(); !state.current {
		t.Fatalf("after the background install, managed web search = %+v, want installed and current", state)
	}
	assertNoStagingLeft(t)

	// The point of letting it finish: the next launch has web search, at once.
	next := newWebSearchInstallProbe()
	close(next.release)
	installManagedWebSearchDependencies = next.install
	var registeredNext bool
	spawnHarness = func(context.Context, string, []string, []string) error {
		registeredNext = webSearchRegistered(t, agentDir)
		return nil
	}
	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("second runSpawn: %v", err)
	}
	if calls, _, _ := next.snapshot(); calls != 0 {
		t.Fatalf("second launch ran the install %d times, want none: the first one finished", calls)
	}
	if !registeredNext {
		t.Fatal("second launch started Pi without the installed web-search package registered in settings.json")
	}
}

// TestRunSpawnCancelsWebSearchInstallAfterGrace: when Pi exits and npm is still
// hung, vc waits no longer than the grace, cancels npm, and returns only after
// the install has unwound — leaving neither a half-installed package nor a
// staging directory behind.
func TestRunSpawnCancelsWebSearchInstallAfterGrace(t *testing.T) {
	webSearchCLILaunch(t)
	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, 50*time.Millisecond)

	child := newPiChild()
	child.useAsCLISpawn(t)
	done := launchInBackground(t, func() error { return runSpawn(nil, nil) }, child, probe)

	await(t, child.spawned, 5*time.Second, "Pi was not spawned while the web-search install was still blocked")
	await(t, probe.started, 5*time.Second, "the web-search install never started while Pi was running")
	child.quit()

	if err := await(t, done, 5*time.Second, "vc did not return after the grace although the install never finishes: the wait is unbounded"); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	calls, returned, cancelled := probe.snapshot()
	if calls != 1 {
		t.Fatalf("install calls=%d, want 1", calls)
	}
	if !cancelled {
		t.Fatal("the install's context was never cancelled: npm keeps running after vc gave up on it")
	}
	if !returned {
		t.Fatal("vc returned before the cancelled install unwound")
	}
	if state := publishedWebSearchState(); state.exists {
		t.Fatalf("a cancelled install left the managed path behind: %+v", state)
	}
	assertNoStagingLeft(t)
}

func TestManagedWebSearchInstallGraceDefaultIsBounded(t *testing.T) {
	if managedWebSearchInstallGrace <= 0 || managedWebSearchInstallGrace > 2*time.Minute {
		t.Fatalf("managedWebSearchInstallGrace = %v, want positive and at most 2m", managedWebSearchInstallGrace)
	}
}

// TestRunSpawnSkipsInstallWhenWebSearchIsCurrent: today's fast path stays —
// nothing to install, Pi starts with the package already registered.
func TestRunSpawnSkipsInstallWhenWebSearchIsCurrent(t *testing.T) {
	agentDir := webSearchCLILaunch(t)
	setup := newWebSearchInstallProbe()
	close(setup.release)
	useWebSearchInstallProbe(t, setup)
	if state, err := reconcileManagedWebSearch(true); err != nil || state != managedWebSearchReady {
		t.Fatalf("pre-install: state=%s err=%v", state, err)
	}

	probe := newWebSearchInstallProbe()
	close(probe.release)
	installManagedWebSearchDependencies = probe.install
	var registered bool
	saved := spawnHarness
	spawnHarness = func(context.Context, string, []string, []string) error {
		registered = webSearchRegistered(t, agentDir)
		return nil
	}
	t.Cleanup(func() { spawnHarness = saved })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls, _, _ := probe.snapshot(); calls != 0 {
		t.Fatalf("install ran %d times for an installed, current extension", calls)
	}
	if !registered {
		t.Fatal("Pi started without the installed web-search package registered")
	}
}

// TestRunSpawnWebSearchInstallFailureLeavesPiAlone: npm fails while Pi runs.
// Pi is not disturbed — not even by the warning, which waits until Pi has
// exited — the failure is not retried in this launch, and the next launch
// tries again.
func TestRunSpawnWebSearchInstallFailureLeavesPiAlone(t *testing.T) {
	webSearchCLILaunch(t)
	stderr := stderrToFile(t)
	probe := newWebSearchInstallProbe()
	probe.failure = errors.New("npm ERR! code ECONNRESET")
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	child.useAsCLISpawn(t)
	done := launchInBackground(t, func() error { return runSpawn(nil, nil) }, child, probe)

	await(t, child.spawned, 5*time.Second, "Pi was not spawned while the web-search install was still blocked")
	await(t, probe.started, 5*time.Second, "the web-search install never started while Pi was running")
	beforeFailure := readText(t, stderr)
	close(probe.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, returned, _ := probe.snapshot(); returned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the failing install never returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Give a warning written straight from the install goroutine the chance to
	// land. This can only miss a violation, never invent one.
	time.Sleep(100 * time.Millisecond)
	if during := readText(t, stderr); during != beforeFailure {
		t.Fatalf("vc wrote into Pi's terminal while Pi was running:\n%s", strings.TrimPrefix(during, beforeFailure))
	}

	child.quit()
	if err := await(t, done, 5*time.Second, "vc did not return after a failed install and Pi's exit"); err != nil {
		t.Fatalf("a web-search install failure must not fail the launch: %v", err)
	}
	out := readText(t, stderr)
	after := strings.TrimPrefix(out, beforeFailure)
	if !strings.Contains(after, "vc: warning:") || !strings.Contains(after, "web search") || !strings.Contains(after, "ECONNRESET") {
		t.Fatalf("stderr after Pi exited = %q, want a vc warning naming web search and the npm error", after)
	}
	if calls, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("install calls in one launch = %d, want 1 (no retry inside a launch)", calls)
	}
	if state := publishedWebSearchState(); state.exists {
		t.Fatalf("a failed install left the managed path behind: %+v", state)
	}
	assertNoStagingLeft(t)

	retry := newWebSearchInstallProbe()
	close(retry.release)
	installManagedWebSearchDependencies = retry.install
	spawnHarness = func(context.Context, string, []string, []string) error { return nil }
	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("second runSpawn: %v", err)
	}
	if calls, _, _ := retry.snapshot(); calls != 1 {
		t.Fatalf("next launch ran the install %d times, want 1: a failure is retried by the next launch", calls)
	}
}

// TestRunSpawnWebSearchUpgradeNeverPublishesHalfInstalled: a stale copy is
// replaced only by a complete one. While npm runs it works in a sibling staging
// directory and the old copy stays whole; if the install is cancelled the old
// copy is still there, whole, and no staging is left.
func TestRunSpawnWebSearchUpgradeNeverPublishesHalfInstalled(t *testing.T) {
	webSearchCLILaunch(t)
	path := managedWebSearchPackagePath()
	readability := filepath.Join(path, "node_modules", "@mozilla", "readability")
	if err := os.MkdirAll(readability, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readability, "package.json"), []byte(`{"name":"@mozilla/readability"}`), 0600); err != nil {
		t.Fatal(err)
	}
	const oldVersion = "0.13.0-void.1"
	oldManifest := `{"name":"@void-code/pi-web-access","version":"` + oldVersion + `","voidCodeFork":{"patch":"VC-10 managed void-codex seam v1"}}`
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(oldManifest), 0600); err != nil {
		t.Fatal(err)
	}

	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, 50*time.Millisecond)
	child := newPiChild()
	child.useAsCLISpawn(t)
	done := launchInBackground(t, func() error { return runSpawn(nil, nil) }, child, probe)

	await(t, child.spawned, 5*time.Second, "Pi was not spawned while the web-search upgrade was still blocked")
	stage := await(t, probe.started, 5*time.Second, "the web-search upgrade never started while Pi was running")
	if filepath.Dir(stage) != filepath.Dir(path) || !strings.HasPrefix(filepath.Base(stage), ".pi-web-access-stage-") {
		t.Errorf("npm ran in %s, want a .pi-web-access-stage-* sibling of %s", stage, path)
	}
	probe.mu.Lock()
	whileInstalling := probe.atStart[0]
	probe.mu.Unlock()
	if whileInstalling.version != oldVersion || !fileExists(filepath.Join(readability, "package.json")) {
		t.Errorf("while npm ran, the published copy was %+v, want the old version intact", whileInstalling)
	}

	child.quit()
	if err := await(t, done, 5*time.Second, "vc did not return after the grace"); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	if state := publishedWebSearchState(); state.version != oldVersion || !fileExists(filepath.Join(readability, "package.json")) {
		t.Fatalf("after a cancelled upgrade the published copy is %+v, want the old version intact", state)
	}
	assertNoStagingLeft(t)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// webSearchDesktopDeps is the production desktop graph with only the parts
// that would leave the machine replaced. Web search stays the real one.
func webSearchDesktopDeps(child *piChild) desktopSessionDeps {
	deps := defaultDesktopSessionDeps()
	deps.loadToken = func() (string, error) { return "token", nil }
	deps.resolveConfig = func() config.Config {
		return config.Config{AccessCheckHost: "https://access.invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
	}
	deps.authGate = func(string, string, *http.Client) (auth.MeResult, bool, error) {
		return auth.MeResult{}, true, nil
	}
	deps.reconcilePi = func() (string, error) { return "/managed/void-code.ts", nil }
	deps.run = func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error { return child.run() }
	return deps
}

func desktopWebSearchCommand(t *testing.T, child *piChild) (func() error, *lockedBuffer) {
	t.Helper()
	node, pi := desktopFiles(t)
	cmd := newDesktopSessionCommand(webSearchDesktopDeps(child))
	errOut := &lockedBuffer{}
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--node", node, "--pi-entry", pi})
	return cmd.Execute, errOut
}

// TestDesktopSessionStartsPiWithoutWaitingForWebSearchInstall: the desktop's
// Pi path behaves like the CLI's — Pi first, npm alongside, vc stays until it
// has finished.
func TestDesktopSessionStartsPiWithoutWaitingForWebSearchInstall(t *testing.T) {
	piSettingsSandbox(t)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "")
	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	var installReturnedAtSpawn bool
	child.observe = func() { _, installReturnedAtSpawn, _ = probe.snapshot() }
	execute, _ := desktopWebSearchCommand(t, child)
	done := launchInBackground(t, execute, child, probe)

	await(t, child.spawned, 5*time.Second, "desktop Pi was not started while the web-search install was still blocked: vc waits for npm before starting Pi")
	if installReturnedAtSpawn {
		t.Fatal("the install had already returned when desktop Pi started, though it was never released")
	}
	await(t, probe.started, 5*time.Second, "the web-search install never started while desktop Pi was running")

	child.quit()
	select {
	case err := <-done:
		t.Fatalf("desktop-session returned (%v) while the web-search install was still in flight", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(probe.release)
	if err := await(t, done, 5*time.Second, "desktop-session did not return after the web-search install finished"); err != nil {
		t.Fatalf("desktop-session: %v", err)
	}
	if calls, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("install calls=%d, want 1", calls)
	}
	if state := publishedWebSearchState(); !state.current {
		t.Fatalf("after the background install, managed web search = %+v, want installed and current", state)
	}
	assertNoStagingLeft(t)
}

// TestDesktopSessionWebSearchInstallFailureLeavesPiAlone: today the desktop
// refuses the whole session when npm fails. With the install in the
// background, a failure is a warning: Pi has already started and keeps going.
func TestDesktopSessionWebSearchInstallFailureLeavesPiAlone(t *testing.T) {
	piSettingsSandbox(t)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "")
	probe := newWebSearchInstallProbe()
	probe.failure = errors.New("npm ERR! code ECONNRESET")
	close(probe.release)
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	execute, errOut := desktopWebSearchCommand(t, child)
	done := launchInBackground(t, execute, child, probe)

	select {
	case <-child.spawned:
	case err := <-done:
		t.Fatalf("desktop-session refused Pi because web search failed to install: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("desktop Pi was not started")
	}
	child.quit()
	if err := await(t, done, 5*time.Second, "desktop-session did not return after Pi exited"); err != nil {
		t.Fatalf("a web-search install failure must not fail the desktop session: %v", err)
	}
	if out := errOut.String(); !strings.Contains(out, "vc: warning:") || !strings.Contains(out, "web search") || !strings.Contains(out, "ECONNRESET") {
		t.Fatalf("desktop stderr = %q, want a vc warning naming web search and the npm error", out)
	}
	if calls, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("install calls in one launch = %d, want 1", calls)
	}
}

// seedStaleWebSearchRegistration writes settings.json that already registers
// the managed web-search path while the directory itself is absent — deleted
// by the user, by a cleanup, or left so by an older vc.
func seedStaleWebSearchRegistration(t *testing.T, agentDir string) {
	t.Helper()
	path := managedWebSearchPackagePath()
	if fileExists(path) {
		t.Fatalf("fixture: %s must not exist", path)
	}
	body, err := json.Marshal(map[string]any{"packages": []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	writePiSettings(t, agentDir, string(body), 0600)
	if !webSearchRegistered(t, agentDir) {
		t.Fatal("fixture: settings.json does not register the managed path")
	}
}

// TestRunSpawnWebSearchUnregistersMissingPackageWhileInstalling: settings.json
// still names the managed package but its directory is gone. Pi must not start
// pointed at a package that is not there while npm runs in the background; once
// the install has completed and Pi has exited, the package is registered again.
func TestRunSpawnWebSearchUnregistersMissingPackageWhileInstalling(t *testing.T) {
	agentDir := webSearchCLILaunch(t)
	seedStaleWebSearchRegistration(t, agentDir)
	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	var registeredAtSpawn bool
	var atSpawn webSearchPublishedState
	child.observe = func() {
		atSpawn = publishedWebSearchState()
		registeredAtSpawn = webSearchRegistered(t, agentDir)
	}
	child.useAsCLISpawn(t)
	done := launchInBackground(t, func() error { return runSpawn(nil, nil) }, child, probe)

	await(t, child.spawned, 5*time.Second, "Pi was not spawned while the web-search install was still blocked")
	if atSpawn.exists {
		t.Fatalf("fixture: managed path existed at spawn although the install was blocked: %+v", atSpawn)
	}
	if registeredAtSpawn {
		t.Fatal("Pi started with settings.json pointing at a web-search package directory that does not exist")
	}
	await(t, probe.started, 5*time.Second, "the web-search install never started while Pi was running")

	close(probe.release)
	child.quit()
	if err := await(t, done, 5*time.Second, "vc did not return after the install finished and Pi exited"); err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	if state := publishedWebSearchState(); !state.current {
		t.Fatalf("after the background install, managed web search = %+v, want installed and current", state)
	}
	if !webSearchRegistered(t, agentDir) {
		t.Fatal("after the install completed and Pi exited, the managed package is not registered in settings.json")
	}
}

// TestDesktopSessionWebSearchUnregistersMissingPackageWhileInstalling: the same
// on the desktop's Pi path.
func TestDesktopSessionWebSearchUnregistersMissingPackageWhileInstalling(t *testing.T) {
	agentDir := piSettingsSandbox(t)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "")
	seedStaleWebSearchRegistration(t, agentDir)
	probe := newWebSearchInstallProbe()
	useWebSearchInstallProbe(t, probe)
	setWebSearchInstallGrace(t, time.Minute)

	child := newPiChild()
	var registeredAtSpawn bool
	child.observe = func() { registeredAtSpawn = webSearchRegistered(t, agentDir) }
	execute, _ := desktopWebSearchCommand(t, child)
	done := launchInBackground(t, execute, child, probe)

	await(t, child.spawned, 5*time.Second, "desktop Pi was not started while the web-search install was still blocked")
	if registeredAtSpawn {
		t.Fatal("desktop Pi started with settings.json pointing at a web-search package directory that does not exist")
	}
	await(t, probe.started, 5*time.Second, "the web-search install never started while desktop Pi was running")

	close(probe.release)
	child.quit()
	if err := await(t, done, 5*time.Second, "desktop-session did not return after the install finished and Pi exited"); err != nil {
		t.Fatalf("desktop-session: %v", err)
	}
	if !webSearchRegistered(t, agentDir) {
		t.Fatal("after the install completed and desktop Pi exited, the managed package is not registered in settings.json")
	}
}
