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
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/makscee/void-code/internal/harness"
	"github.com/makscee/void-code/internal/harness/relay"
	"github.com/makscee/void-code/internal/version"
	"github.com/makscee/void-code/internal/welcome"
	"github.com/spf13/cobra"
)

// meCache holds a cached result from FetchMe to avoid repeated auth-host calls.
var (
	meCacheResult *auth.MeResult
	meCacheExpiry time.Time
)

func main() {
	// --version short-circuit before Cobra parses anything.
	for _, a := range os.Args[1:] {
		if a == "--version" || a == "-v" {
			fmt.Printf("vc %s\n", version.Version)
			return
		}
	}

	// Persistent landing screen — shown on bare `vc` invocation (no sub-command).
	// Checks auth state, shows banner, waits for any keypress.
	// Any keypress → logged-in: spawn claude; logged-out: run login.
	// Skipped for sub-commands (login/logout/status/update) so automation works.
	subCmds := map[string]bool{"login": true, "logout": true, "status": true, "update": true}
	hasSubCmd := len(os.Args) > 1 && subCmds[os.Args[1]]
	if !hasSubCmd {
		state := resolveAuthState()
		result, err := welcome.Run(state)
		if err != nil {
			// welcome.Run already handled non-TTY fallback; ignore error here.
			_ = err
		}
		if result == welcome.RunLogin || !state.LoggedIn {
			// Drop into login flow.
			if err := runLoginInteractive(); err != nil {
				fmt.Fprintf(os.Stderr, "vc: login failed: %v\n", err)
				os.Exit(1)
			}
			// After login, proceed to spawn claude.
		}
		// Fall through to Execute() which calls runSpawn via rootCmd.
	}

	Execute()
}

// resolveAuthState checks token presence and fetches /v1/vc/me for sub-days.
// Never fatal — on any error it returns a graceful degraded state.
func resolveAuthState() welcome.AuthState {
	token := loadTokenStub()
	if token == "" {
		return welcome.AuthState{LoggedIn: false}
	}

	// Check 5-minute in-memory cache first.
	if meCacheResult != nil && time.Now().Before(meCacheExpiry) {
		return meResultToState(*meCacheResult)
	}

	cfg := config.OSResolve()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	me, err := auth.FetchMe(cfg.AuthHost, token, httpClient)
	if err != nil {
		// Auth host unreachable or token invalid — show degraded state.
		if err == auth.ErrNotLoggedIn {
			return welcome.AuthState{LoggedIn: false}
		}
		// Network error — still logged in, but sub status unknown.
		return welcome.AuthState{
			LoggedIn:   true,
			Identity:   "(unknown)",
			SubUnknown: true,
		}
	}

	// Cache result for 5 minutes.
	meCacheResult = &me
	meCacheExpiry = time.Now().Add(5 * time.Minute)
	return meResultToState(me)
}

func meResultToState(me auth.MeResult) welcome.AuthState {
	identity := me.Email
	if identity == "" {
		identity = me.UserID
	}
	return welcome.AuthState{
		LoggedIn:    true,
		Identity:    identity,
		SubDaysLeft: me.SubDaysLeft,
	}
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
