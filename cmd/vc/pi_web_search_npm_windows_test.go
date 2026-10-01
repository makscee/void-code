//go:build windows

package main

// processGone is only reached through the fake npm, which is a POSIX shell
// script; those tests skip on Windows before they get here.
func processGone(int) bool {
	panic("processGone: the fake-npm tests skip on Windows")
}
