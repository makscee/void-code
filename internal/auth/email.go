package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// EmailStartResult holds the confirmation returned by POST /v1/auth/login/email/start.
type EmailStartResult struct{}

// EmailVerifyResult holds the session credentials returned by a successful OTP verify.
type EmailVerifyResult struct {
	Token string
}

// ErrOTPWrong is returned when the server rejects the OTP code.
var ErrOTPWrong = fmt.Errorf("wrong or expired OTP code")

// EmailStart sends POST <authHost>/v1/auth/login/email/start.
func EmailStart(authHost, email string, httpClient *http.Client) (EmailStartResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	body, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequest(http.MethodPost,
		authHost+"/v1/auth/login/email/start",
		bytes.NewReader(body))
	if err != nil {
		return EmailStartResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return EmailStartResult{}, fmt.Errorf("POST email/start: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return EmailStartResult{}, nil
	}
	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	return EmailStartResult{}, fmt.Errorf("email/start error: %s (status %d)", errBody.Error, resp.StatusCode)
}

// EmailVerify sends POST <authHost>/v1/auth/login/email/verify with email + code.
// Returns ErrOTPWrong on 401, or the session credentials on 200.
func EmailVerify(authHost, email, code string, httpClient *http.Client) (EmailVerifyResult, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	body, _ := json.Marshal(map[string]string{"email": email, "code": code})
	req, err := http.NewRequest(http.MethodPost,
		authHost+"/v1/auth/login/email/verify",
		bytes.NewReader(body))
	if err != nil {
		return EmailVerifyResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return EmailVerifyResult{}, fmt.Errorf("POST email/verify: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return EmailVerifyResult{}, ErrOTPWrong
	}

	if resp.StatusCode == http.StatusOK {
		var r struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return EmailVerifyResult{}, fmt.Errorf("decoding email/verify response: %w", err)
		}
		return EmailVerifyResult{Token: r.Token}, nil
	}

	var errBody struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&errBody)
	return EmailVerifyResult{}, fmt.Errorf("email/verify error: %s (status %d)", errBody.Error, resp.StatusCode)
}
