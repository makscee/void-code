package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

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
	vcBin := filepath.Join(tmpDir, "vc")
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
	vcHome := filepath.Join(tmpDir, "void-code-home")
	if err := os.MkdirAll(vcHome, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	tokenPath := filepath.Join(vcHome, "token")
	if err := os.WriteFile(tokenPath, []byte("fake-expired-token"), 0600); err != nil {
		t.Fatalf("writing token: %v", err)
	}

	// Run vc -- -p "say OK" with:
	//   stdin  = /dev/null  (non-TTY)
	//   HOME   = tmpDir     (so ~/.void-code resolves to tmpDir/.void-code)
	//   VC_AUTH_HOST = authSrv.URL (points at the 401-returning stub)
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("opening /dev/null: %v", err)
	}
	defer devNull.Close()

	cmd := exec.Command(vcBin, "--", "-p", "say OK")
	cmd.Stdin = devNull
	cmd.Env = []string{
		"HOME=" + tmpDir,
		"VC_AUTH_HOST=" + authSrv.URL,
		// Prevent any auto-update network calls
		"VC_DISABLE_UPDATE_CHECK=1",
	}

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
