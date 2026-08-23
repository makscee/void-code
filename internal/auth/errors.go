package auth

import "errors"

// Sentinel errors returned by Exchange and DevicePoll.
var (
	// ErrInvalidCode is returned when the local regex rejects the access code.
	ErrInvalidCode = errors.New("invalid access-code format")

	// ErrCodeInvalid is returned when void-auth responds 400 (not found / consumed).
	ErrCodeInvalid = errors.New("access code not found or already used")

	// ErrCodeExpired is returned when void-auth responds 410.
	ErrCodeExpired = errors.New("access code expired")

	// ErrAuthPending is returned by DevicePoll when the user hasn't approved yet.
	ErrAuthPending = errors.New("authorization pending")

	// ErrDeviceExpired is returned by DevicePoll when the device code has expired.
	ErrDeviceExpired = errors.New("device code expired")

	// ErrDeviceDenied is returned by DevicePoll when the device flow was denied.
	ErrDeviceDenied = errors.New("device flow denied")

	ErrDeviceSlowDown    = errors.New("device polling too quickly")
	ErrDeviceConsumed    = errors.New("device authorization already consumed")
	ErrDeviceInvalid     = errors.New("invalid device authorization")
	ErrDeviceMalformed   = errors.New("malformed identity response")
	ErrDeviceRateLimited = errors.New("device authorization rate limited")
)

// ErrAccessNotGranted is returned when a verifying service answers 402: the
// token was accepted and the session verified, but nobody has granted the
// subject behind it access yet. It is deliberately not ErrNotLoggedIn — the
// sign-in already worked, so repeating it changes nothing; what is missing can
// only be handed out by an operator.
//
// What "granted access" is made of on the server (a subscription row, an
// operator grant, a trial) is an open product question, so neither the sentinel
// nor its text names one. Relay's wire name for the same refusal,
// budget_exceeded, is the cautionary example: it points at a monthly budget the
// case has nothing to do with.
var ErrAccessNotGranted = errors.New("access has not been granted to this account yet — an operator has to grant it")
