//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// processGone reports whether pid no longer runs. A zombie counts as gone: it
// was killed and only waits for whoever inherited it to reap it, which on a CI
// runner without a reaping init may never happen.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	out, err := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		// ps exits non-zero when the pid is unknown: it vanished in between.
		return syscall.Kill(pid, 0) != nil
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
