package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrMigrationUnavailable = errors.New("migration unavailable")
	ErrMigrationConflict    = errors.New("migration conflict")
	ErrMigrationInvalidOTP  = errors.New("invalid migration proof")
	ErrMigrationSource      = errors.New("migration source unavailable")
	ErrEntitlementDenied    = errors.New("active VC entitlement required")
)

type MigrationStartResult struct{ Exchange string }
type MigrationCompleteResult struct {
	Token     string
	SubjectID string
	Audience  string
	ExpiresAt int64
}

func migrationEndpoint(host, action string) (string, error) {
	base, err := url.Parse(strings.TrimRight(host, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("invalid identity service URL")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/v1/public/migration/" + action
	return base.String(), nil
}

func postMigration(host, action string, body any, client *http.Client) (*http.Response, error) {
	endpoint, err := migrationEndpoint(host, action)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, ErrMigrationUnavailable
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, ErrMigrationUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrMigrationSource
	}
	return resp, nil
}

func migrationError(resp *http.Response) error {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decodeOne(resp.Body, &body) != nil {
		return ErrMigrationUnavailable
	}
	switch body.Error.Code {
	case "invalid_proof":
		return ErrMigrationInvalidOTP
	case "uuid_conflict", "email_conflict", "identity_ambiguous", "replayed":
		return ErrMigrationConflict
	case "temporarily_unavailable", "source_unavailable":
		return ErrMigrationSource
	default:
		return ErrMigrationUnavailable
	}
}

func MigrationStart(host, legacyToken string, client *http.Client) (MigrationStartResult, error) {
	resp, err := postMigration(host, "start", map[string]string{"client_id": "vc", "legacy_token": legacyToken}, client)
	if err != nil {
		return MigrationStartResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return MigrationStartResult{}, migrationError(resp)
	}
	var body struct {
		OK       bool   `json:"ok"`
		Exchange string `json:"exchange"`
	}
	if decodeOne(resp.Body, &body) != nil || !body.OK || body.Exchange == "" {
		return MigrationStartResult{}, ErrMigrationUnavailable
	}
	return MigrationStartResult{Exchange: body.Exchange}, nil
}

func MigrationComplete(host, exchange, legacyToken, code string, client *http.Client) (MigrationCompleteResult, error) {
	resp, err := postMigration(host, "complete", map[string]string{"client_id": "vc", "exchange": exchange, "legacy_token": legacyToken, "code": code}, client)
	if err != nil {
		return MigrationCompleteResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return MigrationCompleteResult{}, migrationError(resp)
	}
	var body struct {
		OK        bool   `json:"ok"`
		Token     string `json:"token"`
		SubjectID string `json:"subject_id"`
		Audience  string `json:"audience"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if decodeOne(resp.Body, &body) != nil || !body.OK || body.Token == "" || body.SubjectID == "" || body.Audience != publicDeviceAudience() || body.ExpiresAt <= time.Now().UnixMilli() {
		return MigrationCompleteResult{}, ErrMigrationUnavailable
	}
	return MigrationCompleteResult{body.Token, body.SubjectID, body.Audience, body.ExpiresAt}, nil
}

// FetchRelayMe validates a VC bearer through relay authentication and its read-only entitlement check.
func FetchRelayMe(relayURL, token string, client *http.Client) (string, error) {
	endpoint := strings.TrimRight(relayURL, "/") + "/v1/vc/me"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", ErrMigrationSource
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", ErrMigrationSource
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPaymentRequired {
		return "", ErrEntitlementDenied
	}
	if resp.StatusCode != http.StatusOK {
		return "", ErrMigrationSource
	}
	var body struct {
		SubjectID string `json:"subject_id"`
	}
	if err := decodeOne(resp.Body, &body); err != nil || body.SubjectID == "" {
		return "", ErrMigrationSource
	}
	return body.SubjectID, nil
}

func MigrationGuidance(err error) string {
	switch {
	case errors.Is(err, ErrMigrationInvalidOTP):
		return "The code is invalid or expired. Run `vc login` again."
	case errors.Is(err, ErrMigrationConflict):
		return "Migration could not be completed safely. Contact Maks."
	case errors.Is(err, ErrEntitlementDenied):
		return "An active VC subscription is required. Contact Maks."
	case errors.Is(err, ErrMigrationUnavailable):
		return "Migration is unavailable. If your subscription is active, contact Maks to prepare your verified email."
	default:
		return "Migration is temporarily unavailable. Your existing login is unchanged; try again later."
	}
}

func RedactedMigrationError(err error) error { return fmt.Errorf("%s", MigrationGuidance(err)) }
