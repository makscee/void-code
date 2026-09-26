package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ProviderInfo is one entry in the user's granted-provider list from
// GET /v1/vc/providers (VAU-29). SAFE fields only — never any credential.
type ProviderInfo struct {
	ID   string // stable provider id, sent verbatim as the x-void-provider header
	Name string // human display label for the menu row
	Type string // safe provider type from auth, used for compatibility classification
}

// providerTransportError marks failures from the HTTP transport itself. Keeping
// this distinction out of response decoding prevents callers from retrying a
// timeout that happened after an HTTP response was already received.
type providerTransportError struct {
	err error
}

func (e *providerTransportError) Error() string { return e.err.Error() }
func (e *providerTransportError) Unwrap() error { return e.err }

// IsProviderTransportTimeout reports only a timeout/deadline returned while
// establishing or receiving the HTTP response, never a response-body error.
func IsProviderTransportTimeout(err error) bool {
	var transportErr *providerTransportError
	if !errors.As(err, &transportErr) {
		return false
	}
	if errors.Is(transportErr.err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(transportErr.err, &netErr) && netErr.Timeout()
}

// FetchProviders calls GET <authHost>/v1/vc/providers with the bearer token.
// Returns the universal ∪ granted provider list (safe fields only). The server
// degrades to {"providers":[]} on void-keys failure, so an empty list is normal
// and never an error.
func FetchProviders(authHost, token string, httpClient *http.Client) ([]ProviderInfo, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, authHost+"/v1/vc/providers", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET vc/providers: %w", &providerTransportError{err: err})
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, ErrNotLoggedIn
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vc/providers returned status %d", resp.StatusCode)
	}

	var r struct {
		Providers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decoding vc/providers response: %w", err)
	}
	out := make([]ProviderInfo, 0, len(r.Providers))
	for _, p := range r.Providers {
		out = append(out, ProviderInfo{ID: p.ID, Name: p.Name, Type: p.Type})
	}
	return out, nil
}
