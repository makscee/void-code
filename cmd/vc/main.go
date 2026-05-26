// Binary vc — void-code relay harness for Claude Code.
//
// Version is injected at build time:
//
//	go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=v0.0.1" ./cmd/vc
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/version"
	"github.com/spf13/cobra"
)

func main() {
	// --version short-circuit before Cobra parses anything.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("vc %s\n", version.Version)
			return
		}
	}
	Execute()
}

// runSpawn is the default RunE for rootCmd — no sub-command means "launch claude".
func runSpawn(cmd *cobra.Command, args []string) error {
	cfg := config.OSResolve()

	// TODO(VCD-3): load token from ~/.void-code/token
	// TODO(VCD-4): fetch/cache relay CA, build full relay env
	// Scaffold: just pass through env + set proxy vars as stubs.
	env := buildStubEnv(cfg)

	if err := harness.Spawn(context.Background(), "claude", args, env); err != nil {
		// If claude is not installed, print a friendly message.
		if isNotFound(err) {
			fmt.Fprintln(os.Stderr, "vc: claude not found in PATH — install with: npm install -g @anthropic-ai/claude-code")
			os.Exit(127)
		}
		// Propagate claude's exit code.
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// buildStubEnv constructs a minimal env slice for the scaffold phase.
// VCD-4 replaces this with full relay env injection.
func buildStubEnv(cfg config.Config) []string {
	env := make([]string, 0, len(os.Environ())+3)
	env = append(env, os.Environ()...)
	// Relay proxy stub — real token + CA wired in VCD-4.
	env = append(env,
		fmt.Sprintf("HTTPS_PROXY=http://%s", cfg.RelayHost),
		"NODE_EXTRA_CA_CERTS=", // placeholder; populated by VCD-4
	)
	return env
}

// isNotFound reports whether err indicates the binary was not found.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false
	}
	return true
}
