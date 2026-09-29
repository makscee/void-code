package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/browser"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

var openLoginURL = func(value string) { _ = browser.OpenURL(value, os.Stderr) }

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate through Void Identity device authorization",
	Long: `Authenticate through the central Void Identity device flow.

VC opens the central identity login, displays the device approval URL and code,
and polls at the server-prescribed cadence. Credentials are atomically written
to ~/.void-code/token with mode 0600.`,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().Bool("json", false, "emit device-flow progress as JSON lines instead of the interactive prompt (for callers with no terminal, e.g. the desktop app)")
	rootCmd.AddCommand(loginCmd)
}

// deviceFlowRunner and deviceLoginJSONRunner are indirections so tests can swap
// the destination runLogin dispatches to without making a network call.
var deviceFlowRunner = runDeviceFlow

var deviceLoginJSONRunner = func(cfg config.Config, out io.Writer) error {
	return runDeviceLoginJSON(newDeviceLoginDeps(cfg), out)
}

func runLogin(cmd *cobra.Command, _ []string) error {
	if cmd != nil {
		if jsonFlag, err := cmd.Flags().GetBool("json"); err == nil && jsonFlag {
			return deviceLoginJSONRunner(config.OSResolve(), os.Stdout)
		}
	}
	return deviceFlowRunner(config.OSResolve())
}

// runLoginInteractive is called from main.go when the banner detects logged-out
// state on any-key.  It runs the device flow, or returns an error.
func runLoginInteractive() error {
	return runLogin(nil, nil)
}

// ─── device flow (pairing code) ──────────────────────────────────────────────

func deviceBrowserURL(authHost, verificationPath string) string {
	return strings.TrimRight(authHost, "/") + verificationPath
}

func voidCodeDeviceLabel(goos string) string {
	platform := map[string]string{"darwin": "macOS", "windows": "Windows", "linux": "Linux"}[goos]
	if platform == "" {
		platform = "this platform"
	}
	return "Void Code on " + platform
}

const priorSessionCleanupWarning = "Warning: the new login was saved, but the previous Void Code session could not be revoked. Revoke the older Void Code entry manually in Identity Devices."

func replaceDeviceCredential(token string, warnings io.Writer, load func() (string, bool, error), save func(string) error, revoke func(string) error) error {
	previous, _, err := load()
	if err != nil && !errors.Is(err, auth.ErrNotLoggedIn) {
		return err
	}
	if err := save(token); err != nil {
		return err
	}
	if previous == "" || previous == token {
		return nil
	}
	if err := revoke(previous); err != nil {
		fmt.Fprintln(warnings, priorSessionCleanupWarning)
	}
	return nil
}

func installDeviceCredential(authHost, token string, httpClient *http.Client, warnings io.Writer) error {
	return replaceDeviceCredential(token, warnings, auth.Load, auth.Save, func(previous string) error {
		return auth.RevokeSession(authHost, previous, httpClient)
	})
}

// runDeviceFlow performs Flow 1b: pairing-code (device-authorization) flow.
func runDeviceFlow(cfg config.Config) error {
	httpClient := &http.Client{Timeout: 15 * time.Second}

	start, err := auth.DeviceStart(cfg.AuthHost, voidCodeDeviceLabel(runtime.GOOS), httpClient)
	if err != nil {
		return fmt.Errorf("starting pairing-code flow: %w", err)
	}

	verificationURL := deviceBrowserURL(cfg.AuthHost, start.VerificationPath)
	fmt.Printf("\nApprove this device in your browser:\n\n  %s\n\nDevice code: %s\n\nWaiting for authorization", verificationURL, start.UserCode)
	openLoginURL(verificationURL)

	interval := start.Interval
	if interval <= 0 {
		interval = 5
	}

	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Print(".")

		res, err := auth.DevicePoll(cfg.AuthHost, start.DeviceCode, httpClient)
		if err != nil {
			if errors.Is(err, auth.ErrAuthPending) {
				continue
			}
			if errors.Is(err, auth.ErrDeviceSlowDown) {
				interval += 5
				continue
			}
			if errors.Is(err, auth.ErrDeviceExpired) {
				fmt.Println()
				return fmt.Errorf("pairing code expired — run vc login again")
			}
			if errors.Is(err, auth.ErrDeviceDenied) {
				fmt.Println()
				return fmt.Errorf("authorization denied")
			}
			if errors.Is(err, auth.ErrDeviceConsumed) {
				fmt.Println()
				return fmt.Errorf("authorization was already consumed — run vc login again")
			}
			if errors.Is(err, auth.ErrDeviceInvalid) {
				fmt.Println()
				return fmt.Errorf("authorization is invalid — run vc login again")
			}
			fmt.Println()
			return fmt.Errorf("device authorization failed: %w", err)
		}

		// Approved.
		fmt.Println()
		if err := installDeviceCredential(cfg.AuthHost, res.Token, httpClient, os.Stderr); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}
		fmt.Printf("Logged in successfully.\n")
		return nil
	}

	fmt.Println()
	return fmt.Errorf("pairing flow timed out — run vc login again")
}
