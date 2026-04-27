package auth

import "errors"

// Sentinel errors. Middleware translates these to HTTP status codes:
//
//	ErrUnauthorized       → 401 (caller can fix by presenting valid creds)
//	ErrForbidden          → 403 (caller's creds are valid but lack scope)
//	ErrTokenExpired       → 401 with WWW-Authenticate hint
//	ErrTokenMalformed     → 400 (the token isn't even decodable)
//	ErrNotImplemented     → 503 (validator stub; not configured yet)
//
// Code outside this package should compare via [errors.Is], not
// string match — wrappers add context but preserve sentinels.
var (
	// ErrUnauthorized — credential was missing or didn't validate.
	// 401 Unauthorized; clients can retry with a fresh credential.
	ErrUnauthorized = errors.New("auth: credential missing or invalid")

	// ErrForbidden — credential is valid but the subject lacks the
	// scope/role required for the action. 403 Forbidden; clients
	// shouldn't retry without an admin re-issuing a higher-tier
	// credential.
	ErrForbidden = errors.New("auth: subject not authorised for this action")

	// ErrTokenExpired — JWT exp claim has passed. Distinct from
	// ErrUnauthorized so the middleware can set a more useful
	// WWW-Authenticate header.
	ErrTokenExpired = errors.New("auth: token expired")

	// ErrTokenMalformed — credential bytes don't parse as a token
	// at all (bad base64, missing dots, etc.). 400 Bad Request.
	ErrTokenMalformed = errors.New("auth: token malformed")

	// ErrNotImplemented — the validator's protocol surface is
	// scaffolded but the body is a stub. Lands as 503 Service
	// Unavailable in production; fail-loud rather than silently
	// authorising or silently rejecting. Removed once the body
	// implementation lands.
	ErrNotImplemented = errors.New("auth: validator not implemented in this build")
)
