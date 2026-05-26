package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
	"github.com/spf13/cobra"
)

var loginDeviceFlag bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with void-code (access code or device flow)",
	Long: `Authenticate with void-code.

Default: access-code flow — reads VC_CODE from env or prompts interactively.
  VC_CODE=ABCD-EFGH vc login

Device flow (--device): opens a browser URL + polls until approved.
  vc login --device

Credentials are written to ~/.void-code/token (mode 0600).`,
	RunE: runLogin,
}

func init() {
	loginCmd.Flags().BoolVar(&loginDeviceFlag, "device", false, "Use device-code flow instead of access-code")
	rootCmd.AddCommand(loginCmd)
}

func runLogin(_ *cobra.Command, _ []string) error {
	cfg := config.OSResolve()

	if loginDeviceFlag {
		return runDeviceFlow(cfg)
	}

	// Flow 1a: check $VC_CODE env.
	code := os.Getenv(config.EnvCode)
	if code == "" {
		// $VC_CODE absent → fall through to device flow.
		return runDeviceFlow(cfg)
	}
	return runCodeExchange(cfg, code)
}

// runCodeExchange performs Flow 1a: exchange an access code for a session token.
func runCodeExchange(cfg config.Config, code string) error {
	res, err := auth.Exchange(cfg.AuthHost, code, nil)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCode):
			return fmt.Errorf("invalid access-code format (expected AAAA-BBBB): %w", err)
		case errors.Is(err, auth.ErrCodeInvalid):
			return fmt.Errorf("access code not found or already used — check your code")
		case errors.Is(err, auth.ErrCodeExpired):
			return fmt.Errorf("access code has expired — request a new one")
		default:
			return fmt.Errorf("login failed: %w", err)
		}
	}
	if err := auth.Save(res.Token); err != nil {
		return fmt.Errorf("saving token: %w", err)
	}
	fmt.Printf("Logged in as %s\n", res.UserID)
	return nil
}

// runDeviceFlow performs Flow 1b: device-authorization flow.
func runDeviceFlow(cfg config.Config) error {
	httpClient := &http.Client{Timeout: 15 * time.Second}

	start, err := auth.DeviceStart(cfg.AuthHost, httpClient)
	if err != nil {
		return fmt.Errorf("starting device flow: %w", err)
	}

	fmt.Printf("\nOpen this URL in your browser to authorize:\n\n  %s\n\n", start.VerificationURIComplete)
	fmt.Printf("Your device code: %s\n\nWaiting for authorization", start.UserCode)

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
			if errors.Is(err, auth.ErrDeviceExpired) {
				fmt.Println()
				return fmt.Errorf("device code expired — run vc login --device again")
			}
			if errors.Is(err, auth.ErrDeviceDenied) {
				fmt.Println()
				return fmt.Errorf("authorization denied")
			}
			fmt.Println()
			return fmt.Errorf("device poll failed: %w", err)
		}

		// Approved.
		fmt.Println()
		if err := auth.Save(res.Token); err != nil {
			return fmt.Errorf("saving token: %w", err)
		}
		fmt.Printf("Logged in as %s\n", res.UserID)
		return nil
	}

	fmt.Println()
	return fmt.Errorf("device flow timed out — run vc login --device again")
}
