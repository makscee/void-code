package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// vcBinaryName is the file name to build the subprocess-under-test into.
//
// The ".exe" is not cosmetic on Windows, it is the whole test. `go build -o
// <name>` writes exactly <name>: cmd/go appends cfg.ExeSuffix only when -o is
// absent or names a directory (cmd/go/internal/work/build.go, runBuild). The
// resulting extension-less file is a valid PE, but exec.Command can never start
// it: for a path containing ':' or a separator, os/exec on Windows resolves the
// name through findExecutable (lp_windows.go), which for a name without an
// extension tries only <name>+PATHEXT entries — <name> itself is never tried —
// and returns ErrNotExist. CombinedOutput then reports an error with empty
// output and no elapsed time, which is exactly what the windows-latest run
// recorded: `vc output ""` and `vc elapsed: 0s`. The process never ran, so the
// assertion about its message was the only one that could fail.
func vcBinaryName() string {
	if runtime.GOOS == "windows" {
		return "vc.exe"
	}
	return "vc"
}

// childEnv builds the environment for the vc subprocess.
//
// The three VC-relevant entries are the whole point of the test and are set on
// every platform. Everything else is Windows-only because a Windows process
// cannot be handed a three-entry environment and be expected to behave:
//
//   - USERPROFILE, not HOME, is what os.UserHomeDir reads on Windows, and it is
//     what internal/config.CacheDir builds ~/.void-code from. Without it the
//     child would either fall back to the real user profile (reading the
//     developer's actual token) or fail home resolution outright — neither is
//     the "no usable token" condition this test means to create.
//   - SystemRoot, PATH, PATHEXT, TEMP and TMP are how Windows locates system
//     DLLs, helper executables and scratch space; a process started without
//     them fails for reasons unrelated to auth.
//
// Names absent from the parent environment are not forwarded as empty entries.
func childEnv(homeDir, authHost string) []string {
	env := []string{
		"HOME=" + homeDir,
		"VC_AUTH_HOST=" + authHost,
		// Prevent any auto-update network calls
		"VC_DISABLE_UPDATE_CHECK=1",
	}
	if runtime.GOOS != "windows" {
		return env
	}
	env = append(env, "USERPROFILE="+homeDir)
	for _, name := range []string{"SystemRoot", "PATH", "PATHEXT", "TEMP", "TMP"} {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// TestIsStdinTTY_NotATTY verifies isStdinTTY returns false when stdin is a regular file.
func TestIsStdinTTY_NotATTY(t *testing.T) {
	// Open /dev/null as a non-TTY fd for testing.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening /dev/null: %v", err)
	}
	defer f.Close()

	got := isFdTTY(int(f.Fd()))
	if got {
		t.Error("isFdTTY(/dev/null) = true, want false")
	}
}

// TestIsStdinTTY_PipeNotTTY verifies isFdTTY returns false for a pipe fd.
func TestIsStdinTTY_PipeNotTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	got := isFdTTY(int(r.Fd()))
	if got {
		t.Error("isFdTTY(pipe) = true, want false")
	}
}

// TestNonTTY_ExpiredToken_FailsFast verifies that vc exits non-zero within a
// short deadline when the token is invalid and stdin is not a TTY.
//
// This is a subprocess integration test: it builds the vc binary, plants a
// fake token, runs the binary with stdin=/dev/null, and asserts it exits in <5 s
// with a non-zero code and an "auth" hint on stderr.
//
// The test uses a local httptest auth server that returns 401 for any request so
// we don't depend on a live relay or network.
func TestNonTTY_ExpiredToken_FailsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	// Spin up a local auth server that mimics the auth host returning 401
	// (expired/unknown token) for /v1/vc/me.
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()

	// Build vc binary into a temp dir.
	tmpDir := t.TempDir()
	vcBin := filepath.Join(tmpDir, vcBinaryName())
	buildCmd := exec.Command("go", "build",
		"-ldflags", "-X github.com/makscee/void-code/internal/version.Version=test",
		"-o", vcBin, ".")
	buildCmd.Dir = "." // cmd/vc package
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed:\n%s\n%v", out, err)
	}

	// Plant a fake token so token-missing fast-path is not triggered;
	// the 401 from the auth server will trigger the expired-token path.
	//
	// NOTE (measured, not fixed here): this plants nothing vc reads. The token
	// vc loads is <home>/.void-code/token (internal/auth/tokenstore.go
	// tokenDir+tokenPath), not <home>/void-code-home/token, so on every
	// platform the run below exercises the token-MISSING branch of the gate,
	// not the expired one — the observed stderr is "session token missing or
	// expired", and the 401 stub is never called. Left as is: pointing the file
	// at the real path would flip the gate to logged-in and spawn Pi, which is
	// a different test. Fixing that belongs with whoever owns this assertion.
	vcHome := filepath.Join(tmpDir, "void-code-home")
	if err := os.MkdirAll(vcHome, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tokenPath := filepath.Join(vcHome, "token")
	if err := os.WriteFile(tokenPath, []byte("fake-expired-token"), 0600); err != nil {
		t.Fatalf("writing token: %v", err)
	}

	// Run vc -- -p "say OK" with:
	//   stdin  = /dev/null  (NUL on Windows — non-TTY either way)
	//   home   = tmpDir     (HOME on POSIX, USERPROFILE on Windows — see childEnv)
	//   VC_AUTH_HOST = authSrv.URL (points at the 401-returning stub)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening /dev/null: %v", err)
	}
	defer devNull.Close()

	cmd := exec.Command(vcBin, "--", "-p", "say OK")
	cmd.Stdin = devNull
	cmd.Env = childEnv(tmpDir, authSrv.URL)

	start := time.Now()
	out, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	t.Logf("vc output:\n%s", out)
	t.Logf("vc elapsed: %v", elapsed)

	// Must not take longer than 5 seconds (fast-fail, not login hang).
	if elapsed > 5*time.Second {
		t.Errorf("vc took %v to exit — expected fast-fail in < 5s; likely hanging in login flow", elapsed)
	}

	// Must exit non-zero.
	if err == nil {
		t.Error("vc exited 0 with expired token, want non-zero")
	}

	// Stderr must mention auth failure.
	outStr := string(out)
	hasAuthHint := contains(outStr, "auth") || contains(outStr, "login") || contains(outStr, "token") || contains(outStr, "expired")
	if !hasAuthHint {
		t.Errorf("vc output %q does not mention auth failure or login hint", outStr)
	}
}

// contains is a case-insensitive substring check.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub ||
		len(sub) == 0 ||
		indexFold(s, sub) >= 0)
}

func indexFold(s, sub string) int {
	sLow := toLower(s)
	subLow := toLower(sub)
	for i := 0; i <= len(sLow)-len(subLow); i++ {
		if sLow[i:i+len(subLow)] == subLow {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
