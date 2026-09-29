package harness_test

import (
	"context"
	"os"
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

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child never became ready (%s missing)", path)
}

func spawnShell(t *testing.T, ctx context.Context, script string, extraEnv ...string) <-chan error {
	t.Helper()
	sh := "/bin/sh"
	if _, err := os.Stat(sh); err != nil {
		t.Skip("no /bin/sh")
	}
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
	waitForFile(t, ready)

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
	waitForFile(t, ready)

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
