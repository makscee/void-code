package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Second round on void-works#89, after the panel.
//
// The cancellation tests in pi_web_search_background_test.go cancel an
// in-process fake, which honours its context the moment it is asked. The real
// npm is a process tree: npm, its node, whatever it spawned. Killing npm does
// not kill a grandchild that inherited npm's output pipe, and Wait then blocks
// on that pipe until WaitDelay. These tests run the production installer
// against a fake `npm` on PATH that behaves exactly that badly.
//
// Seams this file adds to the contract:
//
//	npmManagedWebSearchInstall func(ctx context.Context, dir string) error
//	    the production `npm ci` installer, compiled in EVERY build (no build
//	    tag). The untagged build wires installManagedWebSearchDependencies to
//	    it; the vctestfixture build keeps its fixture copier as the default.
//	    Either way this test can force the real installer, so CI (which runs
//	    `go test -tags vctestfixture`) runs it too. npm is resolved from PATH.
//	managedWebSearchNpmWaitDelay time.Duration
//	    the WaitDelay given to npm's exec.Cmd: how long vc waits for a killed
//	    npm's children to let go of its output. Production keeps 5 s.
//	staleWebSearchStagingAge time.Duration
//	    the sweep before an install removes .pi-web-access-stage-* and
//	    .pi-web-access-backup-* siblings only when their mtime is older than
//	    this. Default 30 min: no real install takes that long, so the live stage
//	    of a second vc launched at the same moment is never touched.
//
// Unix only: the fake npm is a shell script. Windows resolves npm.cmd through
// cmd.exe, and a faithful fake there is a separate piece of work.

const fakeNpmScript = `#!/bin/sh
PATH=/usr/bin:/bin
printf '%s\n' "$*" > "$FAKE_NPM_MARKERS/args"
pwd > "$FAKE_NPM_MARKERS/cwd"
case "$FAKE_NPM_MODE" in
ok)
	mkdir -p node_modules/@mozilla/readability
	printf '{"name":"@mozilla/readability","version":"0.6.0"}' > node_modules/@mozilla/readability/package.json
	exit 0
	;;
hang)
	: > npm-was-here
	# A grandchild that outlives npm: it keeps a file open inside the stage and
	# inherits npm's stdout, the pipe CombinedOutput waits on.
	sleep 60 3> "$PWD/held-by-grandchild" &
	echo $! > "$FAKE_NPM_MARKERS/grandchild.pid"
	: > "$FAKE_NPM_MARKERS/started"
	exec sleep 60
	;;
esac
echo "fake npm: unknown FAKE_NPM_MODE=$FAKE_NPM_MODE" >&2
exit 2
`

// useFakeNpm puts the fake npm first on PATH, forces the production installer,
// and returns the directory where the fake leaves its markers.
func useFakeNpm(t *testing.T, waitDelay time.Duration) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake npm is a POSIX shell script")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(fakeNpmScript), 0700); err != nil {
		t.Fatal(err)
	}
	markers := t.TempDir()
	t.Setenv("FAKE_NPM_MARKERS", markers)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	savedInstall := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = npmManagedWebSearchInstall
	savedDelay := managedWebSearchNpmWaitDelay
	managedWebSearchNpmWaitDelay = waitDelay
	t.Cleanup(func() {
		installManagedWebSearchDependencies = savedInstall
		managedWebSearchNpmWaitDelay = savedDelay
	})
	// The grandchild is orphaned on purpose; do not leave it for 60 s.
	t.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(markers, "grandchild.pid"))
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if process, err := os.FindProcess(pid); err == nil {
				_ = process.Kill()
			}
		}
	})
	return markers
}

// assertNpmGrandchildDead fails when the process the fake npm left in the
// background is still alive shortly after vc returned.
func assertNpmGrandchildDead(t *testing.T, markers string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(markers, "grandchild.pid"))
	if err != nil {
		t.Fatalf("fake npm recorded no grandchild: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		t.Fatalf("grandchild pid %q: %v", data, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("npm's grandchild (pid %d) still runs after vc cancelled the install and returned: only npm was killed, not its process group", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForFile(path string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if fileExists(path) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// plantStaleSiblings leaves what a crashed or killed older run leaves: a stage
// and a backup, both last touched two hours ago — well past
// staleWebSearchStagingAge.
func plantStaleSiblings(t *testing.T) []string {
	t.Helper()
	var planted []string
	for _, name := range []string{".pi-web-access-stage-1234567", ".pi-web-access-backup-7654321"} {
		planted = append(planted, plantStagingSibling(t, name, time.Now().Add(-2*time.Hour)))
	}
	return planted
}

// plantLiveStage leaves what a second vc, installing at this very moment,
// has next to the package: a stage touched just now. The sweep must not take
// it from under that launch.
func plantLiveStage(t *testing.T) string {
	t.Helper()
	return plantStagingSibling(t, ".pi-web-access-stage-live9999", time.Now())
}

func plantStagingSibling(t *testing.T, name string, modified time.Time) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(managedWebSearchPackagePath()), name)
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "left"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"@void-code/pi-web-access"}`), 0600); err != nil {
		t.Fatal(err)
	}
	// Last, so the writes above do not move the directory's mtime again.
	for _, path := range []string{filepath.Join(dir, "node_modules", "left"), filepath.Join(dir, "node_modules"), filepath.Join(dir, "package.json"), dir} {
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStaleWebSearchStagingAgeDefault(t *testing.T) {
	if staleWebSearchStagingAge != 30*time.Minute {
		t.Fatalf("staleWebSearchStagingAge = %v, want 30m: no real install takes that long, so a concurrent launch's live stage is never swept", staleWebSearchStagingAge)
	}
}

func stagingSiblings(t *testing.T, except ...string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(managedWebSearchPackagePath()))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		skip := false
		for _, path := range except {
			skip = skip || name == filepath.Base(path)
		}
		if skip {
			continue
		}
		if strings.HasPrefix(name, ".pi-web-access-stage-") || strings.HasPrefix(name, ".pi-web-access-backup-") {
			found = append(found, name)
		}
	}
	return found
}

// TestRunSpawnCancelsRealNpmWebSearchInstall: the production installer, a npm
// that hangs and leaves a grandchild holding its pipe and a file in the stage.
// After Pi exits vc gives up within grace + WaitDelay (plus slack), publishes
// nothing, and the next launch — with a npm that works — installs, and sweeps
// every staging and backup directory left behind.
func TestRunSpawnCancelsRealNpmWebSearchInstall(t *testing.T) {
	agentDir := webSearchCLILaunch(t)
	markers := useFakeNpm(t, 300*time.Millisecond)
	t.Setenv("FAKE_NPM_MODE", "hang")
	setWebSearchInstallGrace(t, 100*time.Millisecond)

	saved := spawnHarness
	t.Cleanup(func() { spawnHarness = saved })
	npmStarted := false
	spawnHarness = func(context.Context, string, []string, []string) error {
		// Pi stays up until npm is really running, so the cancel hits a live
		// process tree rather than one that was never started.
		npmStarted = waitForFile(filepath.Join(markers, "started"), 10*time.Second)
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- runSpawn(nil, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runSpawn: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("vc did not return 30s into a hung npm: cancellation does not reach the real installer")
	}
	if !npmStarted {
		t.Fatal("the production installer never started the fake npm on PATH")
	}
	// Before useFakeNpm's cleanup kill, which is only a safety net: the
	// grandchild must already be dead because vc killed npm's whole group, not
	// just npm — WaitDelay alone lets vc return and leaves it running.
	assertNpmGrandchildDead(t, markers)
	args, err := os.ReadFile(filepath.Join(markers, "args"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(args)); len(got) == 0 || got[0] != "ci" || !strings.Contains(string(args), "--ignore-scripts") {
		t.Errorf("npm args = %q, want `ci ... --ignore-scripts ...`", strings.TrimSpace(string(args)))
	}
	cwd, err := os.ReadFile(filepath.Join(markers, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	if stage := strings.TrimSpace(string(cwd)); !strings.HasPrefix(filepath.Base(stage), ".pi-web-access-stage-") {
		t.Errorf("npm ran in %s, want a .pi-web-access-stage-* directory", stage)
	}
	if state := publishedWebSearchState(); state.exists {
		t.Fatalf("a cancelled npm left the managed path published: %+v", state)
	}
	if webSearchRegistered(t, agentDir) {
		t.Fatal("a cancelled npm left the missing package registered in settings.json")
	}

	// Next launch: npm works now, and everything left in the parent — by this
	// cancelled run or by a crashed older one — is swept.
	stale := plantStaleSiblings(t)
	live := plantLiveStage(t)
	t.Setenv("FAKE_NPM_MODE", "ok")
	setWebSearchInstallGrace(t, 30*time.Second)
	spawnHarness = func(context.Context, string, []string, []string) error { return nil }
	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("second runSpawn: %v", err)
	}
	if state := publishedWebSearchState(); !state.current {
		t.Fatalf("after a working npm, managed web search = %+v, want installed and current", state)
	}
	if left := stagingSiblings(t, live); len(left) != 0 {
		t.Fatalf("after the next successful install these remain next to the package: %v (planted stale %v)", left, stale)
	}
	if !fileExists(filepath.Join(live, "package.json")) {
		t.Fatal("the sweep removed a stage touched just now: a concurrent launch's live install")
	}
}

// TestRunSpawnRealNpmWebSearchCancelIsBounded: the bound itself, measured from Pi's exit.
// The grandchild keeps npm's stdout open, so without WaitDelay Wait would sit
// for the grandchild's full 60 s.
func TestRunSpawnRealNpmWebSearchCancelIsBounded(t *testing.T) {
	webSearchCLILaunch(t)
	const (
		grace     = 100 * time.Millisecond
		waitDelay = 300 * time.Millisecond
		slack     = 3 * time.Second
	)
	markers := useFakeNpm(t, waitDelay)
	t.Setenv("FAKE_NPM_MODE", "hang")
	setWebSearchInstallGrace(t, grace)

	saved := spawnHarness
	t.Cleanup(func() { spawnHarness = saved })
	var piExited time.Time
	spawnHarness = func(context.Context, string, []string, []string) error {
		if !waitForFile(filepath.Join(markers, "started"), 10*time.Second) {
			return errors.New("fake npm never started")
		}
		piExited = time.Now()
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- runSpawn(nil, nil) }()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("vc did not return 30s into a hung npm")
	}
	returned := time.Now()
	if err != nil {
		t.Fatalf("runSpawn: %v", err)
	}
	if piExited.IsZero() {
		t.Fatal("the production installer never started the fake npm on PATH")
	}
	if took, bound := returned.Sub(piExited), grace+waitDelay+slack; took > bound {
		t.Fatalf("vc returned %v after Pi exited, want within grace+WaitDelay+slack = %v", took, bound)
	}
}

func TestManagedWebSearchNpmWaitDelayDefault(t *testing.T) {
	if managedWebSearchNpmWaitDelay != 5*time.Second {
		t.Fatalf("managedWebSearchNpmWaitDelay = %v, want 5s", managedWebSearchNpmWaitDelay)
	}
}

// TestRunSpawnWebSearchSweepsStaleStagingBeforeInstall: staging and backup
// directories older than staleWebSearchStagingAge are gone before npm starts,
// the fresh install lands, and nothing else in the parent is touched — not a
// neighbour that is no stage at all, and not a stage modified just now, which
// belongs to a second vc installing concurrently.
func TestRunSpawnWebSearchSweepsStaleStagingBeforeInstall(t *testing.T) {
	webSearchCLILaunch(t)
	stale := plantStaleSiblings(t)
	live := plantLiveStage(t)
	parent := filepath.Dir(managedWebSearchPackagePath())
	bystander := filepath.Join(parent, "pi-web-access-user-copy")
	if err := os.MkdirAll(bystander, 0700); err != nil {
		t.Fatal(err)
	}

	probe := newWebSearchInstallProbe()
	close(probe.release)
	var leftAtStart []string
	saved := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = func(ctx context.Context, dir string) error {
		leftAtStart = stagingSiblings(t, dir, live)
		return probe.install(ctx, dir)
	}
	t.Cleanup(func() { installManagedWebSearchDependencies = saved })
	setWebSearchInstallGrace(t, 30*time.Second)
	savedSpawn := spawnHarness
	spawnHarness = func(context.Context, string, []string, []string) error { return nil }
	t.Cleanup(func() { spawnHarness = savedSpawn })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatal(err)
	}
	if calls, _, _ := probe.snapshot(); calls != 1 {
		t.Fatalf("install calls = %d, want 1", calls)
	}
	if len(leftAtStart) != 0 {
		t.Errorf("npm started with stale staging still next to the package: %v (planted %v)", leftAtStart, stale)
	}
	if left := stagingSiblings(t, live); len(left) != 0 {
		t.Errorf("after the install these remain: %v", left)
	}
	if !fileExists(filepath.Join(live, "package.json")) {
		t.Error("the sweep removed a stage touched just now: a concurrent launch's live install")
	}
	if !fileExists(bystander) {
		t.Error("the sweep removed a directory that is neither a stage nor a backup")
	}
	if state := publishedWebSearchState(); !state.current {
		t.Fatalf("managed web search = %+v, want installed and current", state)
	}
}

// TestRunSpawnWebSearchReportsStageCleanupFailure: when the install fails and
// its own stage cannot be removed, vc says so (after Pi exits) instead of
// leaving an unexplained directory behind.
func TestRunSpawnWebSearchReportsStageCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block removal the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root removes read-only directories regardless")
	}
	webSearchCLILaunch(t)
	stderr := stderrToFile(t)

	var stage, locked string
	saved := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = func(_ context.Context, dir string) error {
		stage = dir
		locked = filepath.Join(dir, "node_modules", "locked")
		if err := os.MkdirAll(locked, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(locked, "pinned"), nil, 0600); err != nil {
			return err
		}
		if err := os.Chmod(locked, 0500); err != nil {
			return err
		}
		return errors.New("npm ERR! code ECONNRESET")
	}
	t.Cleanup(func() {
		installManagedWebSearchDependencies = saved
		if locked != "" {
			_ = os.Chmod(locked, 0700)
		}
	})
	setWebSearchInstallGrace(t, 30*time.Second)
	savedSpawn := spawnHarness
	spawnHarness = func(context.Context, string, []string, []string) error { return nil }
	t.Cleanup(func() { spawnHarness = savedSpawn })

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("a web-search failure must not fail the launch: %v", err)
	}
	if stage == "" {
		t.Fatal("the install never ran")
	}
	if !fileExists(stage) {
		t.Fatal("fixture: the stage was removed despite the read-only subdirectory")
	}
	out := readText(t, stderr)
	if !strings.Contains(out, "ECONNRESET") {
		t.Errorf("stderr = %q, want the npm failure reported", out)
	}
	var cleanupLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "vc: warning:") && strings.Contains(line, filepath.Base(stage)) {
			cleanupLine = line
		}
	}
	if cleanupLine == "" {
		t.Fatalf("stderr = %q, want a vc warning naming the stage %s that could not be removed", out, filepath.Base(stage))
	}
}
