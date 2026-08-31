package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/config"
)

// The contract this file pins:
//
//	func updatePiSettings(mutate func(map[string]any) bool) error
//
// One owner of ~/.pi/agent/settings.json. It takes a cross-process lock, reads
// the file (numbers as their on-disk digits), hands the map to mutate, and
// writes atomically only if mutate returned true. Both existing writers go
// through it, so a guarantee one of them keeps is a guarantee the file has.
//
// Everything here runs in real processes. Goroutines would prove nothing: a
// lock between goroutines is a mutex, and the interleaving that loses a key is
// between two `vc` processes — the desktop keeps two live runtimes for one
// owner (desktop/src/main/session-manager.ts:48 warns the user about exactly
// that), and nothing in vc coordinates them today.

const (
	// Role the re-executed test binary plays. Empty means "not a helper".
	ownerHelperRoleEnv = "VC_PI_SETTINGS_OWNER_ROLE"
	// Path the helper creates once it is INSIDE the mutator, i.e. after the
	// read and before the write. That is the only instant from which the
	// parent can start a second writer and know the window is open.
	ownerHelperEnteredEnv = "VC_PI_SETTINGS_OWNER_ENTERED"
	// Path whose appearance lets a holding helper leave the mutator.
	ownerHelperReleaseEnv = "VC_PI_SETTINGS_OWNER_RELEASE"
	// How long the helper stays inside the mutator, milliseconds.
	ownerHelperHoldEnv = "VC_PI_SETTINGS_OWNER_HOLD_MS"
	// The parent's sandbox home, handed over explicitly. HOME cannot simply be
	// inherited: the child is this same test binary, so it runs TestMain, and
	// TestMain points HOME at a fresh sandbox of its own
	// (home_isolation_test.go). Two children would then take two different
	// locks under two different homes and never contend — the witness would go
	// green while proving nothing. The lock's move into ~/.void-code is what
	// made this load-bearing; while it lived beside settings.json it rode on
	// PI_CODING_AGENT_DIR, which TestMain does not touch.
	ownerHelperHomeEnv = "VC_PI_SETTINGS_OWNER_HOME"

	// The window the losing interleave needs. It also sets a floor under the
	// lock's wait budget: a writer that gives up in less than this would fail
	// TestPiSettingsHasOneOwnerAcrossProcesses/one_slow_writer_and_one_fast_one
	// with a lock-timeout error rather than a lost key. Criterion 5 puts the
	// ceiling on the same budget — it must be reachable well inside a test —
	// so anything from a couple of seconds to half a minute satisfies both.
	ownerHelperHoldMS = 600

	// Bound on every wait in this file. Nothing here should approach it; it
	// exists so a deadlock fails with a message instead of a test timeout.
	ownerHelperPatience = 30 * time.Second
)

type ownerHelper struct {
	cmd *exec.Cmd
	out *bytes.Buffer
}

// startOwnerHelper re-executes this test binary as a second writer of the same
// settings.json. The child inherits PI_CODING_AGENT_DIR, HOME and USERPROFILE
// from the parent's sandbox, so it writes into the temp dir and never into a
// real home.
func startOwnerHelper(t *testing.T, role string, extraEnv ...string) *ownerHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPiSettingsOwnerHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), ownerHelperRoleEnv+"="+role, ownerHelperHomeEnv+"="+os.Getenv("HOME"))
	cmd.Env = append(cmd.Env, extraEnv...)
	out := &bytes.Buffer{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s helper: %v", role, err)
	}
	return &ownerHelper{cmd: cmd, out: out}
}

func (h *ownerHelper) wait(t *testing.T, role string) {
	t.Helper()
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("%s helper failed: %v\n%s", role, err, h.out)
	}
}

func waitForOwnerFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(ownerHelperPatience)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("helper never reached the marker %s within %s", path, ownerHelperPatience)
}

func touchOwnerFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
}

// TestPiSettingsOwnerHelperProcess is the child half of every multi-process
// test here. Under `go test` with no role set it is an ordinary skip; the
// parent re-executes the binary with a role and it becomes a second writer.
func TestPiSettingsOwnerHelperProcess(t *testing.T) {
	role := os.Getenv(ownerHelperRoleEnv)
	if role == "" {
		t.Skip("child process entry point; runs only when " + ownerHelperRoleEnv + " is set")
	}
	// Undo TestMain's redirect and join the parent's sandbox, so both processes
	// resolve the same ~/.void-code and therefore the same lock. Restored when
	// this test ends, which leaves TestMain's own leak check looking at a home
	// nothing wrote to.
	if home := os.Getenv(ownerHelperHomeEnv); home != "" {
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
	}
	entered := os.Getenv(ownerHelperEnteredEnv)
	markEntered := func() {
		if entered == "" {
			return
		}
		if err := os.WriteFile(entered, []byte("in"), 0600); err != nil {
			t.Fatalf("mark entered: %v", err)
		}
	}

	switch role {
	// Writes "packages" but stays inside the mutator first, holding whatever
	// the owner holds. A second writer that starts during the hold and lands
	// before it is a writer that read a file this one is about to overwrite.
	case "hold-then-write":
		holdMS, err := strconv.Atoi(os.Getenv(ownerHelperHoldEnv))
		if err != nil {
			t.Fatalf("bad %s: %v", ownerHelperHoldEnv, err)
		}
		err = updatePiSettings(func(settings map[string]any) bool {
			markEntered()
			time.Sleep(time.Duration(holdMS) * time.Millisecond)
			settings["packages"] = []any{contractPackagePath}
			return true
		})
		if err != nil {
			t.Fatalf("updatePiSettings() error = %v", err)
		}

	// Enters the mutator and does not leave until the parent says so, without
	// ever writing. Used to make the lock unavailable for as long as a test
	// needs it.
	case "hold-until-released":
		release := os.Getenv(ownerHelperReleaseEnv)
		err := updatePiSettings(func(map[string]any) bool {
			markEntered()
			deadline := time.Now().Add(ownerHelperPatience)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(release); err == nil {
					return false
				}
				time.Sleep(2 * time.Millisecond)
			}
			t.Errorf("never released within %s", ownerHelperPatience)
			return false
		})
		if err != nil {
			t.Fatalf("updatePiSettings() error = %v", err)
		}

	// The two real writers, unchanged, as production calls them.
	case "seed":
		if err := ensurePiDefaultModel(); err != nil {
			t.Fatalf("ensurePiDefaultModel() error = %v", err)
		}
	case "packages":
		if err := reconcileManagedPackageSetting(contractPackagePath, true); err != nil {
			t.Fatalf("reconcileManagedPackageSetting() error = %v", err)
		}

	default:
		t.Fatalf("unknown helper role %q", role)
	}
}

// Acceptance criterion 1: two processes writing different keys at the same time
// both arrive.
func TestPiSettingsHasOneOwnerAcrossProcesses(t *testing.T) {
	// The deterministic witness. The slow writer has already read the file
	// when the fast one starts, so without a lock the slow rename lands last
	// and carries a map that predates the fast writer's key. With a lock the
	// fast writer waits, re-reads, and both keys are on disk. This subtest is
	// the one that must go red on an implementation without a lock — if it
	// passes there, nothing in this file is testing anything.
	t.Run("one slow writer and one fast one", func(t *testing.T) {
		dir := piSettingsSandbox(t)
		path := filepath.Join(dir, "settings.json")
		entered := filepath.Join(t.TempDir(), "entered")

		slow := startOwnerHelper(t, "hold-then-write",
			ownerHelperEnteredEnv+"="+entered,
			ownerHelperHoldEnv+"="+strconv.Itoa(ownerHelperHoldMS),
		)
		waitForOwnerFile(t, entered)
		fast := startOwnerHelper(t, "seed")
		fast.wait(t, "seed")
		slow.wait(t, "hold-then-write")

		got := readPiSettings(t, path)
		if _, ok := got["packages"]; !ok {
			t.Errorf("the slow writer's key is gone: %#v", got)
		}
		if got["defaultModel"] != wantPiDefaultModel {
			t.Errorf("defaultModel = %#v, want %q — the fast writer's key was overwritten by a process that read the file before it", got["defaultModel"], wantPiDefaultModel)
		}
		// The children got HOME from this test's sandbox. If the lock turned up
		// under the developer's real ~/.void-code instead, this directory is
		// empty and the two processes were serialising against a file no test
		// owns — which would also make them contend with a live vc session.
		if got := lsNames(t, vcCacheDir(t)); len(got) == 0 {
			t.Errorf("no lock under the sandbox %s — a child process resolved a home this test does not own", vcCacheDir(t))
		}
	})

	// The same claim about the two writers production actually runs, with no
	// widened window. Their critical sections are microseconds long, so a lock
	// makes this pass every time while its absence only sometimes loses a key —
	// which is why the subtest above exists. Repeats give it a chance to speak.
	t.Run("the two real writers, repeatedly", func(t *testing.T) {
		if testing.Short() {
			t.Skip("spawns 2 processes per round")
		}
		for round := 0; round < 8; round++ {
			t.Run("round "+strconv.Itoa(round), func(t *testing.T) {
				dir := piSettingsSandbox(t)
				path := filepath.Join(dir, "settings.json")

				seed := startOwnerHelper(t, "seed")
				packages := startOwnerHelper(t, "packages")
				seed.wait(t, "seed")
				packages.wait(t, "packages")

				got := readPiSettings(t, path)
				if _, ok := got["packages"]; !ok {
					t.Errorf("packages lost: %#v", got)
				}
				if got["defaultModel"] != wantPiDefaultModel {
					t.Errorf("defaultModel = %#v, want %q (lost): %#v", got["defaultModel"], wantPiDefaultModel, got)
				}
			})
		}
	})
}

// Acceptance criterion 4: a mutator that returns false leaves the file exactly
// as it was — same bytes, same mtime, and no staging file left beside it. The
// mutator here does modify the map, so returning false has to be what stops the
// write rather than the map happening to be unchanged.
func TestUpdatePiSettingsWritesNothingWhenMutatorDeclines(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = `{"theme":"nord","lastSessionSeq":9007199254740993}`
	path := writePiSettings(t, dir, body, 0600)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := updatePiSettings(func(settings map[string]any) bool {
		settings["scratch"] = "written by a mutator that then declined"
		delete(settings, "theme")
		return false
	}); err != nil {
		t.Fatalf("updatePiSettings() error = %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("file rewritten although the mutator declined\n got: %s\nwant: %s", after, body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime moved from %s to %s — something wrote and re-wrote the same bytes", before.ModTime(), info.ModTime())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".settings.json.tmp-") {
			t.Errorf("staging file left behind: %s", e.Name())
		}
	}
}

// Acceptance criterion 5: when the lock cannot be taken in the time allowed,
// the writer reports an error and touches nothing. Both callers downgrade that
// error to a warning, so Pi still starts — what must not happen is a write
// without the lock, or a wait that never ends.
func TestPiSettingsWritersFailSafeWhenTheLockIsHeld(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = `{"theme":"nord"}`
	path := writePiSettings(t, dir, body, 0600)
	sync := t.TempDir()
	entered := filepath.Join(sync, "entered")
	release := filepath.Join(sync, "release")

	holder := startOwnerHelper(t, "hold-until-released",
		ownerHelperEnteredEnv+"="+entered,
		ownerHelperReleaseEnv+"="+release,
	)
	defer func() {
		touchOwnerFile(t, release)
		holder.wait(t, "hold-until-released")
	}()
	waitForOwnerFile(t, entered)

	for _, writer := range []struct {
		name string
		call func() error
	}{
		{name: "default-model seed", call: ensurePiDefaultModel},
		{name: "web-search package reconciler", call: func() error {
			return reconcileManagedPackageSetting(contractPackagePath, true)
		}},
	} {
		t.Run(writer.name, func(t *testing.T) {
			start := time.Now()
			err := writer.call()
			elapsed := time.Since(start)

			if err == nil {
				t.Fatalf("error = nil after %s, want a refusal: the lock was held by another process the whole time", elapsed)
			}
			if elapsed > ownerHelperPatience {
				t.Errorf("waited %s for the lock — the wait budget has to be reachable inside a test", elapsed)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != body {
				t.Errorf("file written without the lock\n got: %s\nwant: %s", after, body)
			}
		})
	}
}

// Acceptance criterion 6: the lock comes off however the call ends. Each case
// leaves the lock behind if the release is not on a defer, and the proof is the
// next call in the SAME process getting through — a lock a process failed to
// release is a lock it will also fail to re-take.
func TestUpdatePiSettingsReleasesTheLockOnEveryOutcome(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first func(t *testing.T)
	}{
		{name: "after a write", first: func(t *testing.T) {
			if err := updatePiSettings(func(settings map[string]any) bool {
				settings["first"] = "wrote"
				return true
			}); err != nil {
				t.Fatalf("first updatePiSettings() error = %v", err)
			}
		}},
		{name: "after a declined write", first: func(t *testing.T) {
			if err := updatePiSettings(func(map[string]any) bool { return false }); err != nil {
				t.Fatalf("first updatePiSettings() error = %v", err)
			}
		}},
		// A mutator is caller-supplied code; one of the two callers could
		// panic on a value it did not expect. Whether updatePiSettings lets
		// the panic through or converts it is its business — the lock coming
		// off is not.
		{name: "after a panicking mutator", first: func(t *testing.T) {
			defer func() { _ = recover() }()
			_ = updatePiSettings(func(map[string]any) bool {
				panic("mutator blew up")
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, `{"theme":"nord"}`, 0600)

			tc.first(t)

			done := make(chan error, 1)
			go func() {
				done <- updatePiSettings(func(settings map[string]any) bool {
					settings["second"] = "got through"
					return true
				})
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("second updatePiSettings() error = %v — the lock was not released", err)
				}
			case <-time.After(ownerHelperPatience):
				t.Fatalf("second updatePiSettings() never returned — the lock is still held by this process")
			}

			got := readPiSettings(t, path)
			if got["second"] != "got through" {
				t.Errorf("second call did not write: %#v", got)
			}
		})
	}
}

// vcCacheDir is where vc keeps its own state. Asked of the application rather
// than spelled out, so these tests follow if ~/.void-code ever moves — the same
// reasoning the package's HOME guard uses (home_isolation_test.go).
func vcCacheDir(t *testing.T) string {
	t.Helper()
	dir, err := config.CacheDir()
	if err != nil {
		t.Fatalf("config.CacheDir(): %v", err)
	}
	return dir
}

func lsNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// ~/.pi/agent/ belongs to Pi. vc reads and writes one file in there and has no
// business leaving anything else — a lock, a staging file, a stray marker. Its
// own state goes in its own directory.
func TestUpdatePiSettingsKeepsItsLockOutOfPisDirectory(t *testing.T) {
	t.Run("Pi's directory holds settings.json and nothing else", func(t *testing.T) {
		dir := piSettingsSandbox(t)

		if err := updatePiSettings(func(settings map[string]any) bool {
			settings["defaultModel"] = wantPiDefaultModel
			return true
		}); err != nil {
			t.Fatalf("updatePiSettings() error = %v", err)
		}

		if got := lsNames(t, dir); len(got) != 1 || got[0] != "settings.json" {
			t.Errorf("%s = %v, want exactly [settings.json] — the lock and the staging file belong elsewhere", dir, got)
		}
	})

	// And the other half of the same claim: the lock is somewhere, and that
	// somewhere is ours.
	//
	// It must still be there when the call returns. Unlinking a lock file after
	// releasing it is the standard way to reintroduce the race it exists to
	// prevent: A releases and unlinks while B is waiting on that inode, C
	// creates a fresh file and takes it, and now B and C both believe they hold
	// the lock. A file left in ~/.void-code is the correct outcome, not litter
	// to clean up.
	t.Run("the lock lives in the vc cache directory and outlives the call", func(t *testing.T) {
		piSettingsSandbox(t)
		cache := vcCacheDir(t)

		if err := updatePiSettings(func(settings map[string]any) bool {
			settings["defaultModel"] = wantPiDefaultModel
			return true
		}); err != nil {
			t.Fatalf("updatePiSettings() error = %v", err)
		}

		if got := lsNames(t, cache); len(got) == 0 {
			t.Errorf("%s is empty after a write — the lock is either somewhere else or was unlinked on release", cache)
		}
	})

	t.Run("repeated calls do not accumulate locks", func(t *testing.T) {
		piSettingsSandbox(t)
		cache := vcCacheDir(t)

		var first []string
		for i := 0; i < 3; i++ {
			if err := updatePiSettings(func(settings map[string]any) bool {
				settings["round"] = strconv.Itoa(i)
				return true
			}); err != nil {
				t.Fatalf("updatePiSettings() error = %v", err)
			}
			names := lsNames(t, cache)
			if i == 0 {
				first = names
				continue
			}
			if !reflect.DeepEqual(names, first) {
				t.Fatalf("round %d left %v in %s, round 0 left %v", i, names, cache, first)
			}
		}
	})

	// The lock's name is derived from the settings path, so two installs
	// pointed at different PI_CODING_AGENT_DIRs serialise against their own
	// file and not against each other. One shared lock would be correct but
	// needlessly contended; worse, a name derived from nothing would let a
	// second Pi directory be edited under a lock the first one holds.
	t.Run("two Pi directories do not share one lock", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		roots := t.TempDir()

		for _, name := range []string{"agent-a", "agent-b"} {
			t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(roots, name))
			if err := updatePiSettings(func(settings map[string]any) bool {
				settings["defaultModel"] = wantPiDefaultModel
				return true
			}); err != nil {
				t.Fatalf("updatePiSettings() for %s error = %v", name, err)
			}
		}

		if got := lsNames(t, filepath.Join(home, ".void-code")); len(got) < 2 {
			t.Errorf("~/.void-code = %v, want one lock per Pi directory — the name has to derive from the settings path", got)
		}
	})
}
