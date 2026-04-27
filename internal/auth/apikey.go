package auth

import "context"

// APIKeyValidator looks up an API key and returns the Subject it
// authenticates. Implementations live behind a backing store
// (Redis or Postgres); the interface keeps the middleware
// store-agnostic.
//
// Production implementation lands in Phase 5 alongside the
// `/v1/account/keys` self-service endpoints. The current
// [NoopAPIKeyValidator] returns [ErrNotImplemented] so a deployment
// with auth_mode=apikey but no validator wired fails loud rather
// than silently accepting any key.
type APIKeyValidator interface {
	// Lookup resolves the supplied key bytes to a Subject. Returns
	// [ErrUnauthorized] if the key isn't recognised, [ErrTokenExpired]
	// if it's been revoked, [ErrNotImplemented] if the validator is
	// a stub.
	//
	// The key bytes are passed verbatim (no whitespace strip — that
	// happens in the middleware). Implementations MUST NOT log the
	// key value; treat it as a secret throughout.
	Lookup(ctx context.Context, key string) (Subject, error)
}

// NoopAPIKeyValidator is the placeholder used when auth_mode=apikey
// is configured but no validator implementation is wired. Every
// Lookup returns [ErrNotImplemented]; the middleware translates
// that to 503 Service Unavailable.
//
// This is intentionally not a "permissive" stub — silently
// authorising any key would be far worse than failing the request.
type NoopAPIKeyValidator struct{}

// Lookup implements [APIKeyValidator]. Always returns
// [ErrNotImplemented].
func (NoopAPIKeyValidator) Lookup(_ context.Context, _ string) (Subject, error) {
	return Subject{}, ErrNotImplemented
}

// Compile-time check.
var _ APIKeyValidator = NoopAPIKeyValidator{}
