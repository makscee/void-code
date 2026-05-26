//go:build windows

package update

import (
	"os"
	"os/exec"
)

func restartUnix(newBinaryPath string, args []string) error {
	return ErrRestartUnsupported
}

func restartWindows(newBinaryPath string, args []string) error {
	cmd := exec.Command(newBinaryPath, args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
