package harness_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/harness"
)

// Runtime switch, step 2: vc stops a Codex child by cancelling the context it
// was spawned with. Codex 0.158 dies at once on SIGTERM (checked in a pty on
// 29.09), so a cancel sends SIGTERM first and kills only when the child is
// still there after TerminateGrace (default 5 s). On Windows it is Kill.

func TestTerminateGraceDefaultsToFiveSeconds(t *testing.T) {
	if harness.TerminateGrace != 5*time.Second {
		t.Fatalf("TerminateGrace = %v, want 5s", harness.TerminateGrace)
	}
}

// waitForFile waits for the child's ready marker. If Spawn returns first, the
// child was refused or died before it got that far; that is reported with
// Spawn's own error, never as "not ready".
func waitForFile(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("Spawn returned before the child became ready: %v", err)
		case <-deadline:
			t.Fatalf("child never became ready (%s missing)", path)
		case <-tick.C:
		}
	}
}

// shellPath is a POSIX shell Spawn accepts: harness.Spawn refuses symlinks, and
// on Ubuntu /bin/sh is one (to dash), so the real file is passed.
func shellPath(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		sh = "/bin/sh"
	}
	resolved, err := filepath.EvalSymlinks(sh)
	if err != nil {
		t.Skipf("no usable sh: %v", err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func spawnShell(t *testing.T, ctx context.Context, script string, extraEnv ...string) <-chan error {
	t.Helper()
	return spawnShellWith(t, ctx, shellPath(t), script, extraEnv...)
}

func spawnShellWith(t *testing.T, ctx context.Context, sh, script string, extraEnv ...string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	env := append(os.Environ(), extraEnv...)
	go func() { done <- harness.Spawn(ctx, sh, []string{"-c", script}, env) }()
	return done
}

func TestCancelSendsSIGTERMFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows stops the child with Kill")
	}
	dir := t.TempDir()
	ready, mark := filepath.Join(dir, "ready"), filepath.Join(dir, "term")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := spawnShell(t, ctx,
		`trap 'echo term > "$MARK"; exit 0' TERM; : > "$READY"; while :; do sleep 0.02; done`,
		"READY="+ready, "MARK="+mark)
	waitForFile(t, ready, done)

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Spawn did not return after its context was cancelled")
	}
	data, err := os.ReadFile(mark)
	if err != nil {
		t.Fatal("the child never saw SIGTERM: a cancel killed it outright")
	}
	if strings.TrimSpace(string(data)) != "term" {
		t.Fatalf("marker = %q", data)
	}
}

func TestCancelKillsAChildThatIgnoresSIGTERMAfterTheGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows stops the child with Kill")
	}
	saved := harness.TerminateGrace
	harness.TerminateGrace = 100 * time.Millisecond
	t.Cleanup(func() { harness.TerminateGrace = saved })

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := spawnShell(t, ctx,
		`trap '' TERM; : > "$READY"; while :; do sleep 0.02; done`,
		"READY="+ready)
	waitForFile(t, ready, done)

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a child ignoring SIGTERM was never killed")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("Spawn returned after %v: the child was killed before the grace ran out", elapsed)
	}
}
