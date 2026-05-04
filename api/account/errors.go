package account

import "errors"

// Account-specific error sentinels. These translate upstream dependency
// errors (go-proton-api, go-srp) into package-owned values so that
// consumers do not need to import implementation dependencies.

var (
	// ErrSessionExpired indicates that the session tokens have expired
	// and the session cannot be restored without re-authentication.
	ErrSessionExpired = errors.New("account: session expired")

	// ErrAuthFailed indicates that authentication failed (bad credentials,
	// SRP proof mismatch, or server rejection).
	ErrAuthFailed = errors.New("account: authentication failed")

	// ErrHVRequired indicates that the server requires human verification
	// (CAPTCHA) before the request can proceed.
	ErrHVRequired = errors.New("account: human verification required")

	// ErrForkFailed indicates that the session fork protocol failed.
	ErrForkFailed = errors.New("account: session fork failed")
)
