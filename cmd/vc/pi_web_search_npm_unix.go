//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureNpmProcessTree starts npm in its own process group and makes
// cancellation kill the group: npm, its node and anything they spawned. A
// separate group also keeps a Ctrl-C meant for Pi from killing npm.
func configureNpmProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return cmd.Process.Kill()
		}
		return nil
	}
}
