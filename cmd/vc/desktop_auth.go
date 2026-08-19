package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

type desktopAuthEvent struct {
	Type            string `json:"type"`
	State           string `json:"state,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
	ExpiresIn       int    `json:"expiresIn,omitempty"`
}

type desktopAuthDeps struct {
	start  func(string, string, *http.Client) (auth.DeviceStartResult, error)
	poll   func(string, string, *http.Client) (auth.DevicePollResult, error)
	load   func() (string, bool, error)
	save   func(string) error
	revoke func(string) error
	verify func(string) error
	wait   func(context.Context, time.Duration) error
	client *http.Client
}

func defaultDesktopAuthDeps(authHost string) desktopAuthDeps {
	client := &http.Client{Timeout: 15 * time.Second}
	return desktopAuthDeps{
		start: auth.DeviceStart, poll: auth.DevicePoll, load: auth.Load, save: auth.Save,
		revoke: func(token string) error { return auth.RevokeSession(authHost, token, client) },
		verify: func(token string) error { _, err := auth.FetchProviders(authHost, token, client); return err },
		wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
		client: client,
	}
}

func writeDesktopAuthEvent(out io.Writer, event desktopAuthEvent) error {
	return json.NewEncoder(out).Encode(event)
}

func runDesktopAuthStatus(out io.Writer, deps desktopAuthDeps) error {
	token, _, err := deps.load()
	state := "sign_in_required"
	if err == nil && strings.TrimSpace(token) != "" {
		// A definitive 401 is stale authorization. Transient service/network
		// failures do not erase an otherwise usable credential; chat startup
		// remains the authoritative relay admission check.
		if verifyErr := deps.verify(token); !errors.Is(verifyErr, auth.ErrNotLoggedIn) {
			state = "ready"
		}
	}
	return writeDesktopAuthEvent(out, desktopAuthEvent{Type: "status", State: state})
}

func runDesktopAuthStart(ctx context.Context, out io.Writer, warnings io.Writer, authHost string, deps desktopAuthDeps) error {
	started, err := deps.start(authHost, voidCodeDeviceLabel(runtime.GOOS), deps.client)
	if err != nil {
		return fmt.Errorf("starting device authorization: %w", err)
	}
	if err := writeDesktopAuthEvent(out, desktopAuthEvent{
		Type: "authorization", VerificationURL: deviceBrowserURL(authHost, started.VerificationPath),
		UserCode: started.UserCode, ExpiresIn: started.ExpiresIn,
	}); err != nil {
		return err
	}

	interval := started.Interval
	if interval < 1 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(started.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		if err := deps.wait(ctx, time.Duration(interval)*time.Second); err != nil {
			return fmt.Errorf("device authorization cancelled")
		}
		result, err := deps.poll(authHost, started.DeviceCode, deps.client)
		if errors.Is(err, auth.ErrAuthPending) {
			continue
		}
		if errors.Is(err, auth.ErrDeviceSlowDown) {
			interval += 5
			continue
		}
		if err != nil {
			return fmt.Errorf("device authorization did not complete: %w", err)
		}
		if err := replaceDeviceCredential(result.Token, warnings, deps.load, deps.save, deps.revoke); err != nil {
			return fmt.Errorf("saving authorization: %w", err)
		}
		return writeDesktopAuthEvent(out, desktopAuthEvent{Type: "complete", State: "ready"})
	}
	return fmt.Errorf("device authorization expired")
}

func newDesktopAuthCommand(out, warnings io.Writer, cfg config.Config, deps desktopAuthDeps) *cobra.Command {
	cmd := &cobra.Command{Use: "desktop-auth", Hidden: true, SilenceUsage: true}
	cmd.AddCommand(&cobra.Command{Use: "status", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return runDesktopAuthStatus(out, deps)
	}})
	cmd.AddCommand(&cobra.Command{Use: "start", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		return runDesktopAuthStart(command.Context(), out, warnings, cfg.AuthHost, deps)
	}})
	return cmd
}

func init() {
	cfg := config.OSResolve()
	rootCmd.AddCommand(newDesktopAuthCommand(rootCmd.OutOrStdout(), rootCmd.ErrOrStderr(), cfg, defaultDesktopAuthDeps(cfg.AuthHost)))
}
