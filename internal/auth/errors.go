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
