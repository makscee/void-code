//go:build windows

package pibin

// Windows has no Unix execute permission bits to check.
func nodeIsExecutable(string) bool { return true }
