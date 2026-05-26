//go:build !windows

package update

import (
	"os"
	"syscall"
)

func restartUnix(newBinaryPath string, args []string) error {
	return syscall.Exec(newBinaryPath, args, os.Environ())
}

func restartWindows(newBinaryPath string, args []string) error {
	return ErrRestartUnsupported
}
