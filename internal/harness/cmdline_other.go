//go:build !windows

package harness

import "os/exec"

// applyCmdLine is a no-op on Unix: exec.Cmd passes argv as a slice, so a space
// in the binary path or any arg cannot split a command line, and there is no
// .cmd/.bat shim layer to route around.  The Windows build
// (cmdline_windows.go) routes batch shims through cmd.exe instead.
func applyCmdLine(_ *exec.Cmd, _ string, _ []string) {}
