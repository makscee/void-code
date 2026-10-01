//go:build windows

package main

import "os/exec"

// configureNpmProcessTree keeps exec's default on Windows: cancellation kills
// npm's own process (cmd.exe for npm.cmd), and WaitDelay bounds how long a
// surviving child may hold the output pipe. Killing the whole tree there
// (job objects) is a separate piece of work.
func configureNpmProcessTree(*exec.Cmd) {}
