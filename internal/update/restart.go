package update

import (
	"fmt"
	"os"
	"runtime"
)

// RestartWithNewBinary replaces the current process with newBinaryPath, passing
// through the original os.Args.  On unix it uses syscall.Exec (in-place
// replacement).  On windows it spawns a new process and exits, because
// syscall.Exec is not available on that platform.
func RestartWithNewBinary(newBinaryPath string) error {
	return restartImpl(newBinaryPath, os.Args)
}

// restartImpl is split out for testability (args injection).
func restartImpl(newBinaryPath string, args []string) error {
	if runtime.GOOS == "windows" {
		return restartWindows(newBinaryPath, args)
	}
	return restartUnix(newBinaryPath, args)
}

// ErrRestartUnsupported is returned when the restart mechanism is not available.
var ErrRestartUnsupported = fmt.Errorf("restart not supported on this platform")
