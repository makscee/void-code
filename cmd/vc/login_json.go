package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/makscee/void-code/internal/auth"
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
		return err
	}

	if err := encoder.Encode(map[string]string{
		"event":           "prompt",
		"userCode":        start.UserCode,
		"verificationUrl": deps.browserURL(start.VerificationPath),
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
