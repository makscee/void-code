package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// accessCodeRegex is the canonical format enforced by void-auth.
var accessCodeRegex = regexp.MustCompile(`^[A-HJ-NP-Z2-9]{4}-[A-HJ-NP-Z2-9]{4}$`)

// ExchangeResult holds the credentials returned by a successful code exchange.
type ExchangeResult struct {
	Token  string
	UserID string
}

// Exchange sends a POST to <authHost>/v1/auth/access-codes/exchange with the
// supplied code and returns the session token + userID on success.
//
// Possible errors:
//   - invalid format     → wrapped ErrInvalidCode
//   - 400 from server    → wrapped ErrCodeInvalid (not found / consumed)
//   - 410 from server    → wrapped ErrCodeExpired
//   - other HTTP / net   → plain error
func Exchange(authHost, code string, httpClient *http.Client) (ExchangeResult, error) {
	if !accessCodeRegex.MatchString(code) {
		return ExchangeResult{}, fmt.Errorf("%w: %q", ErrInvalidCode, code)
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := http.NewRequest(http.MethodPost,
		authHost+"/v1/auth/access-codes/exchange",
		bytes.NewReader(body))
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("POST access-codes/exchange: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var r struct {
			Token  string `json:"token"`
			UserID string `json:"userId"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return ExchangeResult{}, fmt.Errorf("decoding response: %w", err)
		}
		return ExchangeResult{Token: r.Token, UserID: r.UserID}, nil
	case http.StatusBadRequest:
		return ExchangeResult{}, ErrCodeInvalid
	case http.StatusGone:
		return ExchangeResult{}, ErrCodeExpired
	default:
		return ExchangeResult{}, fmt.Errorf("unexpected status %d from auth server", resp.StatusCode)
	}
}
