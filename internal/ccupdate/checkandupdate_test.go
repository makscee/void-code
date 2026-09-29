package ccupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// CheckAndUpdate runs npm on the user's machine and may npm-install a new
// claude-code over the one they have, so every branch of it is exercised here
// against a stand-in npm: this test binary, re-entered as TestFakeNPMProcess
// through a small npm (npm.cmd on Windows) wrapper first on PATH. What the fake
// prints and how it exits comes from FAKE_NPM_* variables; each call's
// arguments are appended to a log so a test can say which npm commands ran.

const (
	envFakeBin         = "FAKE_NPM_TESTBIN"
	envFakeLog         = "FAKE_NPM_LOG"
	envFakeList        = "FAKE_NPM_LIST"         // stdout of `npm list -g`
	envFakeView        = "FAKE_NPM_VIEW"         // stdout of `npm view`
	envFakeViewErr     = "FAKE_NPM_VIEW_ERR"     // non-empty: `npm view` prints it to stderr, exits 1
	envFakeViewSleep   = "FAKE_NPM_VIEW_SLEEP"   // non-empty: `npm view` outlives npmCallTimeout
	envFakeInstallFail = "FAKE_NPM_INSTALL_FAIL" // non-empty: `npm install` exits 1
)

// TestFakeNPMProcess is the stand-in npm. In a normal test run it does nothing.
func TestFakeNPMProcess(t *testing.T) {
	if os.Getenv(envFakeBin) == "" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if f, err := os.OpenFile(os.Getenv(envFakeLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		fmt.Print(os.Getenv(envFakeList))
		// npm list exits non-zero on problems in the tree; vc must not care.
		os.Exit(1)
	case "view":
		if os.Getenv(envFakeViewSleep) != "" {
			time.Sleep(3 * npmCallTimeout)
		}
		if msg := os.Getenv(envFakeViewErr); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(1)
		}
		fmt.Println(os.Getenv(envFakeView))
		os.Exit(0)
	case "install":
		if os.Getenv(envFakeInstallFail) != "" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(2)
}

// fakeNPM installs the stand-in npm for one test and returns a function that
// reads back the npm commands run so far.
func fakeNPM(t *testing.T) func() []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		script := "@\"%" + envFakeBin + "%\" -test.run=TestFakeNPMProcess -- %*\r\n"
		if err := os.WriteFile(filepath.Join(dir, "npm.cmd"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		script := "#!/bin/sh\nexec \"$" + envFakeBin + "\" -test.run='^TestFakeNPMProcess$' -- \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, "npm"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(envFakeBin, exe)
	t.Setenv(envFakeLog, logPath)
	for _, k := range []string{envFakeList, envFakeView, envFakeViewErr, envFakeViewSleep, envFakeInstallFail} {
		t.Setenv(k, "")
	}
	t.Setenv(envTTL, "")

	old := CachePath
	CachePath = filepath.Join(dir, "last-cc-update-check")
	t.Cleanup(func() { CachePath = old })

	return func() []string {
		b, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func npmList(version string) string {
	return fmt.Sprintf(`{"dependencies":{"npm":{"version":"10.9.0"},%q:{"version":%q}}}`, pkg, version)
}

var (
	callList    = "list -g --depth=0 --json"
	callView    = "view " + pkg + " version"
	callInstall = "install -g " + pkg + "@latest"
)

func assertCalls(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("npm calls = %q, want %q", got, want)
	}
}

func TestCheckAndUpdateInstallsNewerVersion(t *testing.T) {
	calls := fakeNPM(t)
	t.Setenv(envFakeList, npmList("1.0.0"))
	t.Setenv(envFakeView, "1.2.3")

	if got, want := CheckAndUpdate(), "claude-code: v1.0.0 → v1.2.3"; got != want {
		t.Fatalf("CheckAndUpdate() = %q, want %q", got, want)
	}
	assertCalls(t, calls(), callList, callView, callInstall)
}

func TestCheckAndUpdateSkipsWhenCurrentOrNewer(t *testing.T) {
	for _, tc := range []struct{ installed, latest string }{
		{"1.2.3", "1.2.3"},
		{"1.3.0", "1.2.3"}, // registry lags a local install: never downgrade
	} {
		t.Run(tc.installed+" vs "+tc.latest, func(t *testing.T) {
			calls := fakeNPM(t)
			t.Setenv(envFakeList, npmList(tc.installed))
			t.Setenv(envFakeView, tc.latest)

			if got := CheckAndUpdate(); got != "" {
				t.Fatalf("CheckAndUpdate() = %q, want no output", got)
			}
			assertCalls(t, calls(), callList, callView)
		})
	}
}

// claude-code that npm does not manage (standalone installer, another prefix)
// is left alone: vc must not install a second copy beside it.
func TestCheckAndUpdateLeavesUnmanagedInstallAlone(t *testing.T) {
	for name, list := range map[string]string{
		"not in npm global": `{"dependencies":{"npm":{"version":"10.9.0"}}}`,
		"empty list":        `{}`,
		"malformed output":  `npm ERR! something went wrong`,
		"no output":         ``,
	} {
		t.Run(name, func(t *testing.T) {
			calls := fakeNPM(t)
			t.Setenv(envFakeList, list)
			t.Setenv(envFakeView, "9.9.9")

			if got := CheckAndUpdate(); got != "" {
				t.Fatalf("CheckAndUpdate() = %q, want no output", got)
			}
			assertCalls(t, calls(), callList)
		})
	}
}

func TestCheckAndUpdateWithoutNPMIsSilent(t *testing.T) {
	fakeNPM(t)
	empty := t.TempDir()
	t.Setenv("PATH", empty)

	if got := CheckAndUpdate(); got != "" {
		t.Fatalf("CheckAndUpdate() without npm = %q, want no output", got)
	}
}

func TestCheckAndUpdateRegistryErrors(t *testing.T) {
	for _, tc := range []struct {
		name, stderr, want string
	}{
		{"package gone", "npm ERR! code E404\nnpm ERR! 404 Not Found", "auto-update failed: npm view: npm ERR! code E404\nnpm ERR! 404 Not Found"},
		{"forbidden", "npm ERR! 403 Forbidden", "auto-update failed: npm view: npm ERR! 403 Forbidden"},
		{"server error", "npm ERR! 500 Internal Server Error", "auto-update failed: npm view: npm ERR! 500 Internal Server Error"},
		// Offline is the common case on a laptop: vc says nothing and starts.
		{"offline", "npm ERR! code ENOTFOUND\nnpm ERR! network request failed", ""},
		{"connection reset", "npm ERR! code ECONNRESET", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := fakeNPM(t)
			t.Setenv(envFakeList, npmList("1.0.0"))
			t.Setenv(envFakeViewErr, tc.stderr)

			if got := CheckAndUpdate(); got != tc.want {
				t.Fatalf("CheckAndUpdate() = %q, want %q", got, tc.want)
			}
			assertCalls(t, calls(), callList, callView)
		})
	}
}

func TestCheckAndUpdateEmptyRegistryAnswerDoesNotInstall(t *testing.T) {
	calls := fakeNPM(t)
	t.Setenv(envFakeList, npmList("1.0.0"))
	t.Setenv(envFakeView, "")

	if got := CheckAndUpdate(); got != "" {
		t.Fatalf("CheckAndUpdate() = %q, want no output", got)
	}
	assertCalls(t, calls(), callList, callView)
}

func TestCheckAndUpdateRegistryTimeoutIsSilent(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Killing npm.cmd's cmd.exe leaves the re-entered test binary holding
		// the pipe, so the wait outlasts the timeout; the Unix run covers it.
		t.Skip("fake npm cannot be killed through cmd.exe")
	}
	calls := fakeNPM(t)
	t.Setenv(envFakeList, npmList("1.0.0"))
	t.Setenv(envFakeViewSleep, "1")

	start := time.Now()
	if got := CheckAndUpdate(); got != "" {
		t.Fatalf("CheckAndUpdate() on a hung registry = %q, want no output", got)
	}
	if took := time.Since(start); took > 2*npmCallTimeout {
		t.Fatalf("CheckAndUpdate() took %v on a hung registry, want about %v", took, npmCallTimeout)
	}
	assertCalls(t, calls(), callList, callView)
}

func TestCheckAndUpdateReportsFailedInstall(t *testing.T) {
	calls := fakeNPM(t)
	t.Setenv(envFakeList, npmList("1.0.0"))
	t.Setenv(envFakeView, "1.2.3")
	t.Setenv(envFakeInstallFail, "1")

	got := CheckAndUpdate()
	if !strings.HasPrefix(got, "auto-update failed: ") {
		t.Fatalf("CheckAndUpdate() = %q, want an auto-update failure", got)
	}
	assertCalls(t, calls(), callList, callView, callInstall)
}

// The TTL sentinel is written before npm runs, so a check that fails (offline,
// npm missing) is not retried on every launch within the hour either.
func TestCheckAndUpdateRunsOncePerTTL(t *testing.T) {
	calls := fakeNPM(t)
	t.Setenv(envFakeList, npmList("1.0.0"))
	t.Setenv(envFakeViewErr, "npm ERR! code ENOTFOUND")

	CheckAndUpdate()
	if _, err := os.Stat(CachePath); err != nil {
		t.Fatalf("sentinel not written: %v", err)
	}
	if got := CheckAndUpdate(); got != "" {
		t.Fatalf("second CheckAndUpdate() = %q, want no output", got)
	}
	assertCalls(t, calls(), callList, callView)

	// Once the sentinel is older than the TTL, the check runs again.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(CachePath, past, past); err != nil {
		t.Fatal(err)
	}
	CheckAndUpdate()
	assertCalls(t, calls(), callList, callView, callList, callView)
}

func TestCheckAndUpdateWithoutCachePathAlwaysChecks(t *testing.T) {
	calls := fakeNPM(t)
	CachePath = ""
	t.Setenv(envFakeList, npmList("1.2.3"))
	t.Setenv(envFakeView, "1.2.3")

	CheckAndUpdate()
	CheckAndUpdate()
	assertCalls(t, calls(), callList, callView, callList, callView)
}
