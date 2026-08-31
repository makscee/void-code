package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// The desktop app never goes through runSpawn: it launches Pi with
// `vc desktop-session --node ... --pi-entry ...`. Whatever runSpawn seeds into
// Pi's settings is therefore invisible to anyone who only ever opens the app,
// and the app is how most people arrive. These tests fix the same seed on the
// desktop path, with the same safety the terminal path has: never over a choice
// the user already made, never fatal when it fails, and never before the access
// check has said this token is let in.
//
// Everything runs through the cobra command rather than prepareDesktopSession
// directly, so the tests state what a session does and stay silent on how the
// warning writer is plumbed into it.

// desktopSeedProbe stands in for every seam desktop-session reaches through, so
// a run touches no network, no real home and no Pi process. It also records what
// each seam saw, which is how the seed is placed relative to the gate and to the
// launch without reading line numbers.
type desktopSeedProbe struct {
	journal      []string
	ran          bool
	settingsPath string

	// Snapshot of Pi's settings taken from inside the launch seam: the seed has
	// to be done by the time Pi starts, or the session Pi opens is on the wrong
	// model and the file only becomes right for the next launch.
	settingsAtLaunch   []byte
	settingsAtLaunchOK bool

	loadTokenErr error
	authGateErr  error
}

func (p *desktopSeedProbe) note(name string) { p.journal = append(p.journal, name) }

func (p *desktopSeedProbe) deps() desktopSessionDeps {
	return desktopSessionDeps{
		loadToken: func() (string, error) {
			p.note("loadToken")
			if p.loadTokenErr != nil {
				return "", p.loadTokenErr
			}
			return "token", nil
		},
		resolveConfig: func() config.Config {
			p.note("resolveConfig")
			return config.Config{AuthHost: "http://auth.invalid", AccessCheckHost: "http://check.invalid", RelayScheme: "https", RelayHost: "relay.invalid"}
		},
		authGate: func(string, string, *http.Client) (auth.MeResult, bool, error) {
			p.note("authGate")
			if p.authGateErr != nil {
				return auth.MeResult{}, false, p.authGateErr
			}
			return auth.MeResult{}, true, nil
		},
		resolveCA:   func(config.Config) (string, error) { p.note("resolveCA"); return "/ca.pem", nil },
		reconcilePi: func() (string, error) { p.note("reconcilePi"); return "/managed.ts", nil },
		reconcileSearch: func(bool) (managedWebSearchState, error) {
			p.note("reconcileSearch")
			return managedWebSearchReady, nil
		},
		now: time.Now,
		run: func(context.Context, desktopSessionPlan, io.Reader, io.Writer, io.Writer) error {
			p.note("run")
			p.ran = true
			data, err := os.ReadFile(p.settingsPath)
			p.settingsAtLaunch, p.settingsAtLaunchOK = data, err == nil
			return nil
		},
	}
}

func (p *desktopSeedProbe) called(name string) bool {
	for _, entry := range p.journal {
		if entry == name {
			return true
		}
	}
	return false
}

// execDesktopSession runs the command the desktop app runs and returns whatever
// it wrote to its own error stream. The command's stream is the one asserted on:
// a warning printed to os.Stderr instead reaches a process the app does not read.
func execDesktopSession(t *testing.T, probe *desktopSeedProbe) (string, error) {
	t.Helper()
	node, pi := desktopFiles(t)
	cmd := newDesktopSessionCommand(probe.deps())
	var errOut bytes.Buffer
	cmd.SetIn(bytes.NewReader(nil))
	cmd.SetOut(io.Discard)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--node", node, "--pi-entry", pi})
	// Executed first and read second on purpose: a `return errOut.String(),
	// cmd.Execute()` evaluates the buffer before the command ever runs, and
	// every assertion about the warning then reads an empty string.
	err := cmd.Execute()
	return errOut.String(), err
}

// captureProcessStderr redirects os.Stderr for the length of one test. The
// returned function restores it and yields everything written meanwhile.
func captureProcessStderr(t *testing.T) func() string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = writer
	collected := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		collected <- buf.String()
	}()
	var (
		once sync.Once
		out  string
	)
	stop := func() string {
		once.Do(func() {
			os.Stderr = saved
			_ = writer.Close()
			out = <-collected
			_ = reader.Close()
		})
		return out
	}
	t.Cleanup(func() { stop() })
	return stop
}

func piSettingsExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
	return false
}

// Contract 1: the desktop path seeds the same pair the terminal path seeds, and
// it is already on disk when Pi is launched.
func TestDesktopSessionSeedsPiDefaultModelBeforeLaunchingPi(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := filepath.Join(dir, "settings.json")
	probe := &desktopSeedProbe{settingsPath: path}

	warnings, err := execDesktopSession(t, probe)
	if err != nil {
		t.Fatalf("desktop-session returned %v; warnings=%q", err, warnings)
	}
	if !probe.ran {
		t.Fatal("desktop-session never launched Pi")
	}

	if !piSettingsExists(t, path) {
		t.Fatalf("desktop-session left Pi's settings unseeded at %s — a user who only opens the app never gets the default model", path)
	}
	assertDefaultsWritten(t, readPiSettings(t, path))

	if !probe.settingsAtLaunchOK {
		t.Fatal("Pi was launched before its settings were seeded: the file did not exist yet at launch")
	}
	if !bytes.Contains(probe.settingsAtLaunch, []byte(wantPiDefaultModel)) {
		t.Fatalf("Pi was launched before the seed landed; settings at launch were:\n%s", probe.settingsAtLaunch)
	}
}

// Contract 1, the other half of "as safely as the terminal path": a model the
// user picked is a choice, not a gap to fill. The file must come out of a
// session byte-identical.
//
// The mode is checked as "whatever it was, unchanged" rather than against 0600,
// because Go on Windows has no POSIX permissions to report: os.Stat gives every
// ordinary file 0666, os.Chmod moves only the read-only flag, and the real
// access lives in ACLs that os.FileMode cannot express. A literal 0600 there
// demands something the platform does not have and reddens correct code.
//
// Plainly, so a green Windows run is not read as two platforms agreeing: on
// Windows the mode half of this subtest is nearly empty — a preserved file and
// a re-created one both report 0666, so it cannot tell them apart. It still
// catches a session that left the user's settings read-only. The mode claim is
// verified on POSIX. The byte-identity claim above it — the reason this subtest
// exists — is fully verified on both.
func TestDesktopSessionKeepsAnExistingDefaultModel(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = "{\n  \"defaultModel\": \"user-pick\",\n  \"theme\": \"nord\"\n}\n"
	path := writePiSettings(t, dir, body, 0600)
	before := statPerm(t, path)
	// On POSIX the fixture has to really carry 0600, or the comparison below
	// holds the write against a mode nobody asked for.
	if runtime.GOOS != "windows" && before != 0600 {
		t.Fatalf("fixture mode = %04o, want 0600 — the test cannot say anything about a mode it failed to set", before)
	}
	probe := &desktopSeedProbe{settingsPath: path}

	if warnings, err := execDesktopSession(t, probe); err != nil {
		t.Fatalf("desktop-session returned %v; warnings=%q", err, warnings)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("desktop-session rewrote settings the user owns.\nbefore:\n%s\nafter:\n%s", body, after)
	}
	if got := statPerm(t, path); got != before {
		t.Errorf("settings.json mode = %04o, want %04o unchanged — a session updates the user's file, it does not re-create it", got, before)
	}
}

// Contract 2: the seed is a convenience, never a precondition. A settings file
// vc cannot parse must not cost the user their session — and the warning has to
// reach the stream the command was given, because that is the one the desktop
// app reads. Every other failure in this preparation is fatal; this one is not.
func TestDesktopSessionSurvivesASeedFailureAndWarnsThroughTheCommand(t *testing.T) {
	dir := piSettingsSandbox(t)
	const broken = "{ this is not json"
	path := writePiSettings(t, dir, broken, 0600)
	probe := &desktopSeedProbe{settingsPath: path}

	processStderr := captureProcessStderr(t)
	warnings, err := execDesktopSession(t, probe)
	leaked := processStderr()

	if err != nil {
		t.Fatalf("an unseedable settings.json cost the user the session: %v", err)
	}
	if !probe.ran {
		t.Fatal("Pi was never launched after the seed failed; the seed is a convenience, not a precondition")
	}

	if strings.TrimSpace(warnings) == "" {
		t.Fatal("the failed seed was not reported on the command's error stream")
	}
	if !strings.Contains(strings.ToLower(warnings), "model") {
		t.Fatalf("warning does not say what failed: %q", warnings)
	}
	if !strings.Contains(warnings, "parse Pi settings") {
		t.Fatalf("warning drops the cause reported by the seed: %q", warnings)
	}
	if strings.TrimSpace(leaked) != "" {
		t.Fatalf("warning went to os.Stderr instead of the command's error stream: %q", leaked)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Fatalf("a settings file vc could not parse was rewritten anyway:\n%s", after)
	}
}

// Contract 3: nobody's Pi settings are edited on the strength of a token that
// was never accepted. Asserted by what is on disk after a refused preparation,
// not by where the call sits in the function.
func TestDesktopSessionDoesNotTouchPiSettingsWithoutAccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*desktopSeedProbe)
		gate  string
	}{
		{"no token", func(p *desktopSeedProbe) { p.loadTokenErr = errors.New("no token stored") }, "authGate"},
		{"access check refuses", func(p *desktopSeedProbe) { p.authGateErr = errors.New("access check unreachable") }, "reconcilePi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := filepath.Join(dir, "settings.json")
			probe := &desktopSeedProbe{settingsPath: path}
			tc.setup(probe)

			if _, err := execDesktopSession(t, probe); err == nil {
				t.Fatal("desktop-session prepared a session for a token that was not accepted")
			}
			if probe.ran {
				t.Fatal("Pi was launched anyway")
			}
			if probe.called(tc.gate) {
				t.Fatalf("preparation walked past the access check: %v", probe.journal)
			}
			if piSettingsExists(t, path) {
				t.Fatalf("a refused session still wrote to %s — settings are edited before the access check", path)
			}
		})
	}
}
