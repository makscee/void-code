package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DeviceStartResult holds the response from POST /v1/auth/device/start.
type DeviceStartResult struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

// DevicePollResult holds the credentials returned by a successful device poll.
type DevicePollResult struct {
	Token  string
	UserID string
}

// DeviceStart begins the device-authorization flow by calling
// POST <authHost>/v1/auth/device/start.
func DeviceStart(authHost string, httpClient *http.Client) (DeviceStartResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	// Body is optional; send empty object.
	req, err := http.NewRequest(http.MethodPost,
		authHost+"/v1/auth/device/start",
		bytes.NewReader([]byte("{}")))
	if err != nil {
		return DeviceStartResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return DeviceStartResult{}, fmt.Errorf("POST device/start: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return DeviceStartResult{}, fmt.Errorf("device/start returned status %d", resp.StatusCode)
	}

	var r struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return DeviceStartResult{}, fmt.Errorf("decoding device/start response: %w", err)
	}
	return DeviceStartResult{
		DeviceCode:              r.DeviceCode,
		UserCode:                r.UserCode,
		VerificationURI:         r.VerificationURI,
		VerificationURIComplete: r.VerificationURIComplete,
		ExpiresIn:               r.ExpiresIn,
		Interval:                r.Interval,
	}, nil
}

// DevicePoll calls POST <authHost>/v1/auth/device/poll once.
//
// Possible errors:
//   - ErrAuthPending     — still waiting (keep polling)
//   - ErrDeviceExpired   — code expired; stop polling
//   - ErrDeviceDenied    — user denied; stop polling
//   - other error        — stop polling
func DevicePoll(authHost, deviceCode string, httpClient *http.Client) (DevicePollResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	body, _ := json.Marshal(map[string]string{"device_code": deviceCode})
	req, err := http.NewRequest(http.MethodPost,
		authHost+"/v1/auth/device/poll",
		bytes.NewReader(body))
	if err != nil {
		return DevicePollResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return DevicePollResult{}, fmt.Errorf("POST device/poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var r struct {
			SessionToken string `json:"session_token"`
			UserID       string `json:"user_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return DevicePollResult{}, fmt.Errorf("decoding device/poll response: %w", err)
		}
		return DevicePollResult{Token: r.SessionToken, UserID: r.UserID}, nil
	}

	// Non-200: decode error body.
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)

	switch errBody.Error {
	case "authorization_pending":
		return DevicePollResult{}, ErrAuthPending
	case "expired_token":
		return DevicePollResult{}, ErrDeviceExpired
	case "access_denied":
		return DevicePollResult{}, ErrDeviceDenied
	case "slow_down":
		// Treat slow_down as pending — caller should increase interval.
		return DevicePollResult{}, ErrAuthPending
	default:
		return DevicePollResult{}, fmt.Errorf("device/poll error: %s (status %d)", errBody.Error, resp.StatusCode)
	}
}
