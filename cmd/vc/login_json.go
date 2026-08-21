package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/makscee/void-code/internal/auth"
	"github.com/makscee/void-code/internal/config"
)

// deviceLoginDeps is runDeviceFlow's device-authorization flow with every network
// call, sleep, and clock read replaced by a seam, so runDeviceLoginJSON can be
// driven by a test instead of the identity service.
type deviceLoginDeps struct {
	start      func() (auth.DeviceStartResult, error)
	poll       func(deviceCode string) (auth.DevicePollResult, error)
	install    func(token string) error
	sleep      func(time.Duration)
	now        func() time.Time
	browserURL func(verificationPath string) string
}

// runDeviceLoginJSON runs the pairing-code flow for a caller with no terminal:
// the desktop app. It writes one JSON object per line to out instead of the
// human-formatted prompt runDeviceFlow prints, so the desktop can put the code
// on screen, open the browser, and learn the outcome without scraping text.
func runDeviceLoginJSON(deps deviceLoginDeps, out io.Writer) error {
	encoder := json.NewEncoder(out)

	start, err := deps.start()
	if err != nil {
		encoder.Encode(map[string]string{"event": "error", "reason": deviceLoginJSONStartReason(err)})
		return err
	}

	// map[string]string can't carry ExpiresIn as a number, which is how this
	// field went missing from the shipped binary in the first place: the
	// map type made it uncompilable to include, not merely easy to forget.
	if err := encoder.Encode(map[string]interface{}{
		"event":            "prompt",
		"userCode":         start.UserCode,
		"verificationUrl":  deps.browserURL(start.VerificationPath),
		"expiresInSeconds": start.ExpiresIn,
	}); err != nil {
		return err
	}

	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := deps.now().Add(time.Duration(start.ExpiresIn) * time.Second)

	for deps.now().Before(deadline) {
		deps.sleep(interval)

		res, err := deps.poll(start.DeviceCode)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrAuthPending):
				continue
			case errors.Is(err, auth.ErrDeviceSlowDown):
				interval += 5 * time.Second
				continue
			default:
				encoder.Encode(map[string]string{"event": "error", "reason": deviceLoginJSONReason(err)})
				return err
			}
		}

		if err := deps.install(res.Token); err != nil {
			encoder.Encode(map[string]string{"event": "error", "reason": "install_failed"})
			return err
		}
		return encoder.Encode(map[string]string{"event": "authorized"})
	}

	encoder.Encode(map[string]string{"event": "error", "reason": "timeout"})
	return fmt.Errorf("pairing flow timed out")
}

// newDeviceLoginDeps wires deviceLoginDeps to the real identity service and
// credential store, using the same auth host and same credential path
// (installDeviceCredential -> auth.Save) as the interactive device flow, so
// the desktop's JSON login lands in the exact place vc login already does.
func newDeviceLoginDeps(cfg config.Config) deviceLoginDeps {
	httpClient := &http.Client{Timeout: 15 * time.Second}

	return deviceLoginDeps{
		start: func() (auth.DeviceStartResult, error) {
			return auth.DeviceStart(cfg.AuthHost, voidCodeDeviceLabel(runtime.GOOS), httpClient)
		},
		poll: func(deviceCode string) (auth.DevicePollResult, error) {
			return auth.DevicePoll(cfg.AuthHost, deviceCode, httpClient)
		},
		install: func(token string) error {
			return installDeviceCredential(cfg.AuthHost, token, httpClient, os.Stderr)
		},
		sleep: time.Sleep,
		now:   time.Now,
		browserURL: func(verificationPath string) string {
			return deviceBrowserURL(cfg.AuthHost, verificationPath)
		},
	}
}

// deviceLoginJSONStartReason maps a device-start failure to the stable word
// the desktop branches on. Rate limiting is the one start failure a person
// can act on (wait and retry), so it gets the same word the poll path uses
// for the same condition; every other start failure keeps the generic
// "start_failed" word, since we don't yet know a distinct action to tell the
// user about it.
func deviceLoginJSONStartReason(err error) string {
	if errors.Is(err, auth.ErrDeviceRateLimited) {
		return "rate_limited"
	}
	return "start_failed"
}

// deviceLoginJSONReason maps a poll failure to the stable word the desktop
// branches on. It has to be a word, not the wrapped error text: prose changes
// with the message an upstream service happens to send back.
func deviceLoginJSONReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrDeviceExpired):
		return "expired"
	case errors.Is(err, auth.ErrDeviceDenied):
		return "denied"
	case errors.Is(err, auth.ErrDeviceConsumed):
		return "consumed"
	case errors.Is(err, auth.ErrDeviceInvalid):
		return "invalid"
	case errors.Is(err, auth.ErrDeviceRateLimited):
		return "rate_limited"
	default:
		return "failed"
	}
}
