package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// WebSession is the result of POST /v1/vc/web-session (VCD-66 mint endpoint).
// Token is the short-lived (1-day) magic-link token. NOTE (VCD-80): the prod
// server emits a broken magic-link URL (auth.makscee.ru/profile?ml=…, 404), so
// callers build the redeem URL from Token via browser.RedeemURL instead.
type WebSession struct {
	Token string
}

// MintWebSession calls POST <authHost>/v1/vc/web-session with the bearer token.
func MintWebSession(authHost, token string, httpClient *http.Client) (WebSession, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequest(http.MethodPost, authHost+"/v1/vc/web-session", nil)
	if err != nil {
		return WebSession{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return WebSession{}, fmt.Errorf("POST vc/web-session: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return WebSession{}, ErrNotLoggedIn
	}
	if resp.StatusCode != http.StatusOK {
		return WebSession{}, fmt.Errorf("vc/web-session returned status %d", resp.StatusCode)
	}

	var r struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return WebSession{}, fmt.Errorf("decoding vc/web-session response: %w", err)
	}
	if r.Token == "" {
		return WebSession{}, fmt.Errorf("vc/web-session returned empty token")
	}
	return WebSession{Token: r.Token}, nil
}
