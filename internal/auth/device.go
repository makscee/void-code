package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const PublicDeviceClientID = "vc"

type DeviceStartResult struct {
	DeviceCode       string
	UserCode         string
	VerificationPath string
	ExpiresIn        int
	Interval         int
}

type DevicePollResult struct {
	Token     string
	Audience  string
	ExpiresAt int64
}

func deviceEndpoint(authHost, action string) (string, error) {
	base, err := url.Parse(strings.TrimRight(authHost, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid identity service URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/identity-stage/api/device/" + action
	return base.String(), nil
}

func postDevice(authHost, action string, body any, httpClient *http.Client) (*http.Response, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	endpoint, err := deviceEndpoint(authHost, action)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("cannot encode device request")
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("cannot build device request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("identity service is unavailable")
	}
	return resp, nil
}

func DeviceStart(authHost, deviceLabel string, httpClient *http.Client) (DeviceStartResult, error) {
	resp, err := postDevice(authHost, "start", map[string]string{"client_id": PublicDeviceClientID, "device_label": deviceLabel}, httpClient)
	if err != nil {
		return DeviceStartResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return DeviceStartResult{}, ErrDeviceRateLimited
	}
	if resp.StatusCode != http.StatusCreated {
		return DeviceStartResult{}, fmt.Errorf("device authorization start failed (status %d)", resp.StatusCode)
	}
	var r struct {
		DeviceCode       string `json:"deviceCode"`
		UserCode         string `json:"userCode"`
		VerificationPath string `json:"verificationPath"`
		IntervalSeconds  int    `json:"intervalSeconds"`
	}
	if err := decodeOne(resp.Body, &r); err != nil || r.DeviceCode == "" || r.UserCode == "" || r.VerificationPath != "/device" || r.IntervalSeconds < 1 {
		return DeviceStartResult{}, ErrDeviceMalformed
	}
	return DeviceStartResult{DeviceCode: r.DeviceCode, UserCode: r.UserCode, VerificationPath: r.VerificationPath, ExpiresIn: 600, Interval: r.IntervalSeconds}, nil
}

func DevicePoll(authHost, deviceCode string, httpClient *http.Client) (DevicePollResult, error) {
	return devicePollAt(authHost, deviceCode, httpClient, time.Now)
}

func devicePollAt(authHost, deviceCode string, httpClient *http.Client, now func() time.Time) (DevicePollResult, error) {
	resp, err := postDevice(authHost, "poll", map[string]string{"client_id": PublicDeviceClientID, "device_code": deviceCode}, httpClient)
	if err != nil {
		return DevicePollResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		var r struct {
			Token     string `json:"token"`
			Audience  string `json:"audience"`
			ExpiresAt int64  `json:"expiresAt"`
		}
		if err := decodeOne(resp.Body, &r); err != nil || r.Token == "" || r.Audience != "vc" || r.ExpiresAt <= now().UnixMilli() {
			return DevicePollResult{}, ErrDeviceMalformed
		}
		return DevicePollResult{Token: r.Token, Audience: r.Audience, ExpiresAt: r.ExpiresAt}, nil
	}
	var body struct {
		Status string `json:"status"`
	}
	if decodeOne(resp.Body, &body) != nil {
		return DevicePollResult{}, ErrDeviceMalformed
	}
	switch body.Status {
	case "pending":
		return DevicePollResult{}, ErrAuthPending
	case "slow_down":
		return DevicePollResult{}, ErrDeviceSlowDown
	case "expired":
		return DevicePollResult{}, ErrDeviceExpired
	case "denied":
		return DevicePollResult{}, ErrDeviceDenied
	case "consumed":
		return DevicePollResult{}, ErrDeviceConsumed
	case "invalid":
		return DevicePollResult{}, ErrDeviceInvalid
	default:
		return DevicePollResult{}, ErrDeviceMalformed
	}
}

func decodeOne(r io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r, 64<<10))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("unexpected response data")
	}
	return nil
}
