package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Third round on void-works#89: npm's process tree on Windows.
//
// On Unix a cancelled npm dies with its whole process group (pinned by
// TestRunSpawnCancelsRealNpmWebSearchInstall). On Windows npm is npm.cmd under
// cmd.exe, then node; killing only the top process leaves node running in the
// stage. The Windows cancel kills the tree with taskkill instead.
//
// These tests pin the BRANCH LOGIC of that cancel, and they run on every OS so
// a darwin or linux CI catches a regression in it. They do not prove taskkill
// itself kills a tree — that is verified live on WIN11-VCLAB, not here.
//
// Contract (all compiled on every OS):
//
//	npmTreeCancel(goos string, cmd *exec.Cmd, killTree func(pid int) error) func() error
//	    the exec.Cmd.Cancel for npm on goos. For "windows": call killTree with
//	    npm's pid; if killTree returns an error, fall back to cmd.Process.Kill
//	    and return its result. The Windows configureNpmProcessTree sets
//	    cmd.Cancel = npmTreeCancel(runtime.GOOS, cmd, killNpmTree).
//	killNpmTree func(pid int) error
//	    the production tree kill: runs windowsNpmTreeKillArgs(pid).
//	windowsNpmTreeKillArgs(pid int) []string
//	    the taskkill command line: taskkill /T /F /PID <pid>.

// startSleeper starts a process that would outlive the test by a minute. The
// caller's cancel is what should end it; the cleanup is only a safety net.
func startSleeper(t *testing.T, ctx context.Context) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ping", "-n", "60", "127.0.0.1")
	} else {
		cmd = exec.CommandContext(ctx, "sleep", "60")
	}
	return cmd
}

func waitExited(t *testing.T, cmd *exec.Cmd, within time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

type treeKillProbe struct {
	mu   sync.Mutex
	pids []int
	err  error
	// kill makes the probe behave like a working taskkill.
	kill bool
}

func (p *treeKillProbe) killTree(pid int) error {
	p.mu.Lock()
	p.pids = append(p.pids, pid)
	p.mu.Unlock()
	if p.kill {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
	return p.err
}

func (p *treeKillProbe) calls() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int(nil), p.pids...)
}

// TestWebSearchWindowsNpmCancelKillsTreeByPid: cancelling the context runs the
// tree kill with npm's own pid, and the tree kill alone ends the process.
func TestWebSearchWindowsNpmCancelKillsTreeByPid(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := startSleeper(t, ctx)
	probe := &treeKillProbe{kill: true}
	cmd.Cancel = npmTreeCancel("windows", cmd, probe.killTree)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	cancel()
	if !waitExited(t, cmd, 5*time.Second) {
		t.Fatal("npm still runs after cancel: the Windows cancel did not end it")
	}
	if got, want := probe.calls(), []int{cmd.Process.Pid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tree kill called with %v, want npm's pid %v", got, want)
	}
}

// TestWebSearchWindowsNpmCancelFallsBackToProcessKill: when taskkill fails
// (missing, access denied, pid already gone), npm itself is still killed.
func TestWebSearchWindowsNpmCancelFallsBackToProcessKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := startSleeper(t, ctx)
	probe := &treeKillProbe{err: errors.New("taskkill: access denied")}
	cmd.Cancel = npmTreeCancel("windows", cmd, probe.killTree)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	cancel()
	if !waitExited(t, cmd, 5*time.Second) {
		t.Fatal("npm still runs after a failed tree kill: no fallback to Process.Kill")
	}
	if got := probe.calls(); len(got) != 1 || got[0] != cmd.Process.Pid {
		t.Fatalf("tree kill called with %v, want once with npm's pid %d before the fallback", got, cmd.Process.Pid)
	}
}

func TestWebSearchWindowsNpmTreeKillCommand(t *testing.T) {
	got := windowsNpmTreeKillArgs(4242)
	want := []string{"taskkill", "/T", "/F", "/PID", "4242"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree kill command = %q, want %q", got, want)
	}
	if killNpmTree == nil {
		t.Fatal("killNpmTree is nil: the Windows cancel would have no tree kill to call")
	}
}
