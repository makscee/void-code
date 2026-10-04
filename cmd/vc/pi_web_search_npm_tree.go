package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// npmTreeKillTimeout bounds the tree kill itself: exec runs Cancel on its own
// goroutine and Wait cannot move on until it returns.
const npmTreeKillTimeout = 10 * time.Second

// npmTreeCancel is the exec.Cmd.Cancel for npm on goos, compiled on every OS
// so its branch logic is tested everywhere.
//
// On Windows npm is npm.cmd under cmd.exe, then node; killing only the top
// process leaves node running in the stage. So the whole tree is killed by
// npm's pid, and if that fails (taskkill missing, access denied, pid already
// gone) npm itself still is, and that result is what Cancel reports.
//
// Elsewhere it is exec's default, killing npm only; the Unix build does not
// use it and kills npm's process group instead (pi_web_search_npm_unix.go).
func npmTreeCancel(goos string, cmd *exec.Cmd, killTree func(pid int) error) func() error {
	if goos != "windows" {
		return func() error { return cmd.Process.Kill() }
	}
	return func() error {
		if err := killTree(cmd.Process.Pid); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}

// windowsNpmTreeKillArgs is the command line that kills npm's tree on Windows.
func windowsNpmTreeKillArgs(pid int) []string {
	return []string{"taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)}
}

// killNpmTree is the production tree kill: runs windowsNpmTreeKillArgs(pid),
// without a console window where the platform has one to hide.
var killNpmTree = func(pid int) error {
	ctx, cancel := context.WithTimeout(context.Background(), npmTreeKillTimeout)
	defer cancel()
	args := windowsNpmTreeKillArgs(pid)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	hideConsoleWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
