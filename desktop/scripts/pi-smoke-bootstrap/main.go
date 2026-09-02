// Command pi-smoke-bootstrap stands in for `vc pi-bootstrap` while the bundled Pi smoke runs.
//
// The smoke checks that a bundled Pi still loads the extension vc installs, and that extension asks
// vc which providers are granted by running VC_BOOTSTRAP_EXECUTABLE with the single argument
// pi-bootstrap. What the smoke needs back is a registered provider, not a live relay -- so this
// prints the answer the smoke put in the environment and nothing else.
//
// It is a Go program rather than a shell script because the extension spawns it through
// execFileSync without a shell, and Node has refused to spawn .cmd and .bat that way since 18.20:
// a shell script cannot be the stub on Windows, and one stub for POSIX beside another for Windows
// is two fixtures for one role. Go already cross-compiles for windows/amd64 in this repository.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "pi-bootstrap" {
		fmt.Fprintf(os.Stderr, "pi-smoke-bootstrap: expected exactly one argument, pi-bootstrap, got %v\n", os.Args[1:])
		os.Exit(1)
	}
	answer := os.Getenv("VC_SMOKE_BOOTSTRAP_JSON")
	if answer == "" {
		fmt.Fprintln(os.Stderr, "pi-smoke-bootstrap: VC_SMOKE_BOOTSTRAP_JSON is empty, so there is no bootstrap answer to give")
		os.Exit(1)
	}
	fmt.Print(answer)
}
