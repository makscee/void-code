//go:build windows

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the child gets no console of its own,
// so a desktop launch does not flash one.
const createNoWindow = 0x08000000

// configureNpmProcessTree makes cancellation kill npm's whole tree (cmd.exe,
// node and anything below) with taskkill, falling back to npm alone; see
// npmTreeCancel. WaitDelay still bounds how long a survivor may hold the
// output pipe.
func configureNpmProcessTree(cmd *exec.Cmd) {
	cmd.Cancel = npmTreeCancel(runtime.GOOS, cmd, killNpmTree)
}

func hideConsoleWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
