// Binary vc — void-code relay harness for Claude Code.
//
// Version is injected at build time:
//
//	go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=v0.0.1" ./cmd/vc
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/harness/relay"
	"github.com/makscee/void-code/internal/version"
	"github.com/makscee/void-code/internal/welcome"
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

	// First-launch welcome screen — shown once, blocks until dismissed, then
	// writes sentinel so it never appears again.  Must run before Cobra parses
	// arguments so the TUI exits cleanly before any spawn.
	sentinelPath := welcome.DefaultSentinelPath()
	if welcome.NeedsWelcome(sentinelPath) {
		_ = welcome.Run(sentinelPath)
	}

	Execute()
}

// runSpawn is the default RunE for rootCmd — no sub-command means "launch claude".
func runSpawn(cmd *cobra.Command, args []string) error {
	cfg := config.OSResolve()

	// TODO(VCD-3): load token from ~/.void-code/token via tokenstore.Load.
	// For now read the raw file if present; VCD-3 replaces with proper store.
	token := loadTokenStub()

	caPath, err := resolveCA(cfg)
	if err != nil {
		// Non-fatal: warn and continue; claude may still work without proxy CA.
		fmt.Fprintf(os.Stderr, "vc: warning: cannot resolve relay CA: %v\n", err)
		caPath = ""
	}

	env := relay.BuildEnv(os.Environ(), cfg.RelayHost, token, caPath)

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

// resolveCA determines the relay CA path in priority order:
//  1. VC_RELAY_CA env override (cfg.CAOverride).
//  2. Cached file at ~/.void-code/relay-ca.pem (FetchCA returns it if present,
//     fetches from <authHost>/vc/relay-ca.pem otherwise).
//  3. On network failure, write the embedded fallback CA to the cache dir
//     so first-run-offline always has a working CA.
func resolveCA(cfg config.Config) (string, error) {
	if cfg.CAOverride != "" {
		return cfg.CAOverride, nil
	}

	cacheDir, err := config.CacheDir()
	if err != nil {
		return writeFallbackCA("")
	}

	caPath, err := relay.FetchCA(http.DefaultClient, cfg.AuthHost, cacheDir)
	if err != nil {
		// Network unavailable or server error — fall back to embedded CA.
		return writeFallbackCA(cacheDir)
	}
	return caPath, nil
}

// writeFallbackCA writes the build-time-embedded relay-ca.pem to cacheDir
// (creating the directory as needed) and returns the path.
// If cacheDir is empty a temp file is used.
func writeFallbackCA(cacheDir string) (string, error) {
	if len(relayCA) == 0 {
		return "", fmt.Errorf("relay: embedded CA is empty")
	}

	var dest string
	if cacheDir == "" {
		f, err := os.CreateTemp("", "vc-relay-ca-*.pem")
		if err != nil {
			return "", fmt.Errorf("relay: temp CA: %w", err)
		}
		dest = f.Name()
		if _, err := f.Write(relayCA); err != nil {
			f.Close()
			return "", fmt.Errorf("relay: temp CA write: %w", err)
		}
		return dest, f.Close()
	}

	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", fmt.Errorf("relay: mkdir cache: %w", err)
	}
	dest = filepath.Join(cacheDir, "relay-ca.pem")
	if err := os.WriteFile(dest, relayCA, 0600); err != nil {
		return "", fmt.Errorf("relay: write fallback CA: %w", err)
	}
	return dest, nil
}

// loadTokenStub reads ~/.void-code/token if it exists and returns the trimmed
// content.  Returns empty string when absent.  VCD-3 replaces this with
// tokenstore.Load.
func loadTokenStub() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(home, ".void-code", "token"))
	if err != nil {
		return ""
	}
	// Trim trailing whitespace / newlines.
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] != '\n' && data[i] != '\r' && data[i] != ' ' && data[i] != '\t' {
			return string(data[:i+1])
		}
	}
	return ""
}

// isNotFound reports whether err indicates the binary was not found in PATH.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false
	}
	return true
}
