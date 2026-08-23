package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The chain behind "Запросить доступ" is four links long — desktop → vc →
// Relay → Keys — and this file is the client half of the third one: the two
// calls vc makes to Relay.
//
//	POST <AccessCheckHost>/v1/vc/access-requests      ask, or read back the one already open
//	GET  <AccessCheckHost>/v1/vc/access-requests/me   read my own state
//
// The host is the access-check host and not the sign-in host, for the reason
// written down in config.Config.AccessCheckHost: in production the two are
// different services and the same bearer gets opposite verdicts from them.
// This route is served next to /v1/vc/me, so it follows the check.
const (
	accessRequestPath     = "/v1/vc/access-requests"
	accessRequestMePath   = "/v1/vc/access-requests/me"
	accessRequestTimeout  = 10 * time.Second
	maxAccessRequestBytes = 1 << 20
)

// The three words the queue uses. A fourth one is not a state this client may
// act on: it would travel to the desktop, whose renderer answers an unknown
// word with silence — so an unrecognised status is read as "we did not get an
// answer we understand", which is what it is.
const (
	AccessRequestOpen     = "open"
	AccessRequestGranted  = "granted"
	AccessRequestDeclined = "declined"
)

// ErrAccessRequestUnavailable is the sentinel for "we could not get an answer",
// and it exists to be told apart from the answer "there is no request".
//
// The two look identical at the end of the call — the client holds no request
// either way — and only one of them is news. Rendering silence as "you have not
// asked yet" offers the button to somebody who already pressed it, and hides an
// outage behind a screen that looks like it is working. Callers branch on this
// sentinel, never on prose an upstream happened to send.
//
// The text names nothing internal on purpose: whatever the upstream said about
// its own insides is not something the person reading this can act on, and the
// credential must never come back out.
var ErrAccessRequestUnavailable = errors.New("the access-request service did not answer, so nothing is known about any request right now")

// AccessRequest is one row of the operator's queue as far as this client is
// allowed to know it: whether an ask exists, and when it happened.
//
// There is deliberately no expiry, entitlement or balance here. An ask creates
// nothing — the grant stays an operator path — so a server that sent one of
// those along would find no field to put it in, and nothing to display it from.
type AccessRequest struct {
	ID         string
	SubjectID  string
	Status     string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

// AskForAccess files a request for access and returns it.
//
// Keys answers 201 the first time and 200 afterwards with the row it already
// has; both are the same news to the person waiting — "your ask is in" — so
// both come back as a request and neither as an error. The returned CreatedAt
// is whatever the server holds, which for the second press is the FIRST
// submission: the waiting screen has one fact to show and that is it.
func AskForAccess(host, token string, httpClient *http.Client) (AccessRequest, error) {
	req, ok, err := callAccessRequestRoute(http.MethodPost, host+accessRequestPath, token, httpClient,
		http.StatusOK, http.StatusCreated)
	if err != nil {
		return AccessRequest{}, err
	}
	// The ask route has no "there is nothing" answer — Relay turns that case
	// into a 503 — so an empty one is silence wearing the shape of an answer.
	// Accepting it would invent the single state the button exists to change.
	if !ok {
		return AccessRequest{}, fmt.Errorf("the ask produced no request: %w", ErrAccessRequestUnavailable)
	}
	return req, nil
}

// FetchAccessRequest reads the caller's own request without creating one.
//
// ok reports whether there is a request at all: ok=false with a nil error is
// the server saying "you have never asked", which is an answer and the one the
// button is for. Every failure to obtain an answer arrives as an error carrying
// ErrAccessRequestUnavailable instead, and never as ok=false.
func FetchAccessRequest(host, token string, httpClient *http.Client) (AccessRequest, bool, error) {
	return callAccessRequestRoute(http.MethodGet, host+accessRequestMePath, token, httpClient, http.StatusOK)
}

// callAccessRequestRoute performs one call and turns the answer into at most
// one of: a request, "there is none", or a reason we have nothing.
func callAccessRequestRoute(method, url, token string, httpClient *http.Client, accepted ...int) (AccessRequest, bool, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: accessRequestTimeout}
	}

	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		return AccessRequest{}, false, fmt.Errorf("the request could not be built: %w", ErrAccessRequestUnavailable)
	}
	// The bearer is everything the call carries: no query and no body. The
	// subject is the one Relay verified for this session (src/vc-access-requests.ts
	// reads user.userId and ignores the request), so anything the client added
	// would be a second, unverified opinion about who is asking.
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))

	resp, err := httpClient.Do(request)
	if err != nil {
		// The transport error names the host and port it failed to reach.
		// None of that is the caller's to print, so it stops here.
		return AccessRequest{}, false, fmt.Errorf("the service could not be reached: %w", ErrAccessRequestUnavailable)
	}
	defer resp.Body.Close()

	// A refused bearer keeps its own sentinel. Relay judges our token before it
	// ever speaks to Keys, so 401 means here what it means everywhere else —
	// and it is the one failure in this file that signing in again fixes.
	if resp.StatusCode == http.StatusUnauthorized {
		return AccessRequest{}, false, ErrNotLoggedIn
	}
	if !acceptedStatus(accepted, resp.StatusCode) {
		// 402 lands here too, and that is the point: the person asking is
		// precisely the one with no access, so answering them "no access"
		// instead of showing their request closes the last door open to them.
		return AccessRequest{}, false, fmt.Errorf("the service answered %d: %w", resp.StatusCode, ErrAccessRequestUnavailable)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAccessRequestBytes))
	if err != nil {
		return AccessRequest{}, false, fmt.Errorf("the answer could not be read: %w", ErrAccessRequestUnavailable)
	}
	return parseAccessRequestEnvelope(body)
}

func acceptedStatus(accepted []int, status int) bool {
	for _, code := range accepted {
		if code == status {
			return true
		}
	}
	return false
}

// parseAccessRequestEnvelope reads {"request": …} and refuses everything else.
//
// The key has to be PRESENT for its null to mean "never asked": a body with no
// request key at all — an error page, `null`, an unrelated object — is not a
// server saying there is nothing, it is a server saying something we do not
// understand. The distinction is the whole reason this function is separate.
func parseAccessRequestEnvelope(body []byte) (AccessRequest, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return AccessRequest{}, false, fmt.Errorf("the answer was not an object: %w", ErrAccessRequestUnavailable)
	}
	raw, present := envelope["request"]
	if !present {
		return AccessRequest{}, false, fmt.Errorf("the answer carried no request: %w", ErrAccessRequestUnavailable)
	}
	if string(bytes.TrimSpace(raw)) == "null" {
		// The one honest "there is nothing": a server that answered, and said so.
		return AccessRequest{}, false, nil
	}

	var wire struct {
		ID         string `json:"id"`
		SubjectID  string `json:"subject_id"`
		Status     string `json:"status"`
		CreatedAt  *int64 `json:"created_at"`
		ResolvedAt *int64 `json:"resolved_at"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return AccessRequest{}, false, fmt.Errorf("the request could not be read: %w", ErrAccessRequestUnavailable)
	}

	status := strings.TrimSpace(wire.Status)
	switch status {
	case AccessRequestOpen, AccessRequestGranted, AccessRequestDeclined:
	default:
		return AccessRequest{}, false, fmt.Errorf("the answer named a state this client does not know: %w", ErrAccessRequestUnavailable)
	}
	if strings.TrimSpace(wire.ID) == "" {
		return AccessRequest{}, false, fmt.Errorf("the request had no identifier: %w", ErrAccessRequestUnavailable)
	}
	// created_at is what the waiting screen shows; a request without one is
	// half an answer, and half an answer is not one.
	if wire.CreatedAt == nil {
		return AccessRequest{}, false, fmt.Errorf("the request had no submission time: %w", ErrAccessRequestUnavailable)
	}

	// created_at / resolved_at are epoch MILLISECONDS — void-relay's own
	// fixtures build them with Date.parse.
	parsed := AccessRequest{
		ID:        strings.TrimSpace(wire.ID),
		SubjectID: strings.TrimSpace(wire.SubjectID),
		Status:    status,
		CreatedAt: time.UnixMilli(*wire.CreatedAt).UTC(),
	}
	if wire.ResolvedAt != nil {
		resolved := time.UnixMilli(*wire.ResolvedAt).UTC()
		parsed.ResolvedAt = &resolved
	}
	return parsed, true, nil
}
