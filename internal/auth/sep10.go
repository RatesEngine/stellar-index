package auth

import (
	"context"
	"time"
)

// SEP10Validator implements the server side of Stellar Ecosystem
// Proposal 10 (SEP-10 / Web Authentication).
//
// Three protocol functions:
//
//	Challenge — issue a Stellar transaction the client must sign
//	Verify    — accept the signed transaction; issue a JWT
//	VerifyJWT — accept the JWT on subsequent requests
//
// The flow is two round-trips:
//
//  1. Client: GET /v1/auth/sep10/challenge?account=G… → server returns Challenge
//  2. Client signs the Challenge.TransactionXDR with its account key
//  3. Client: POST /v1/auth/sep10/token with the signed XDR → server returns JWT
//  4. Client uses `Authorization: Bearer <jwt>` on subsequent requests
//
// The middleware (`internal/api/v1/middleware/auth.go`) calls
// [SEP10Validator.VerifyJWT] on every request when auth_mode=sep10.
// The challenge/verify endpoints are mounted by the API server
// when an SEP10Validator is wired.
//
// The production implementation lives in
// [internal/auth/sep10.Validator] — `sep10.NewValidator(sep10.Options{…})`
// is built by `cmd/ratesengine-api/main.go`'s `buildSEP10Validator`.
// [NoopSEP10Validator] in this package is the graceful-degradation
// fallback used when the deployment hasn't configured the required
// env vars (signing seed + JWT secret); every method returns
// [ErrNotImplemented] so `/v1/auth/sep10/*` responds 503 while the
// rest of the API still serves. With `auth_mode=sep10` the
// missing-config path is a hard startup failure instead.
//
// References:
//
//   - https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0010.md
type SEP10Validator interface {
	// Challenge generates a SEP-10-conformant challenge transaction
	// for the given account. The transaction is unsigned and never
	// submitted to the network — it's a structured nonce. Returns
	// [ErrNotImplemented] from the stub.
	Challenge(ctx context.Context, account string) (Challenge, error)

	// Verify accepts a challenge transaction signed by the account
	// referenced in [Challenge]. On success returns a JWT bearing
	// the authenticated account as its subject. Returns
	// [ErrUnauthorized] for missing/invalid signatures,
	// [ErrTokenMalformed] for a non-parseable transaction.
	Verify(ctx context.Context, signedTransactionXDR string) (Token, error)

	// VerifyJWT validates a JWT issued by [Verify] (or an upstream
	// SEP-10 server we delegate to) and returns the Subject it
	// authenticates. Called on every request from the auth
	// middleware. Returns [ErrTokenExpired] when the exp claim has
	// passed, [ErrUnauthorized] for any other validation failure.
	VerifyJWT(ctx context.Context, jwt string) (Subject, error)
}

// Challenge is a SEP-10 challenge transaction. The TransactionXDR is
// what the client signs; the other fields are echoed for the
// client's own diagnostics.
type Challenge struct {
	// TransactionXDR — base64-encoded XDR of the unsigned
	// transaction. Client signs this with its account key (no
	// network submission).
	TransactionXDR string

	// NetworkPassphrase — the network the transaction was crafted
	// for ("Public Global Stellar Network ; September 2015" for
	// pubnet). Echoed so the client can verify it doesn't sign a
	// transaction crafted for the wrong network.
	NetworkPassphrase string

	// IssuedAt + ValidUntil — the challenge has a short lifetime
	// (typically 15 minutes). Verify rejects challenges past
	// ValidUntil.
	IssuedAt   time.Time
	ValidUntil time.Time
}

// Token is what [SEP10Validator.Verify] returns: the JWT plus
// metadata the client uses to know when to refresh.
type Token struct {
	// JWT — the bearer token the client sends on subsequent
	// requests as `Authorization: Bearer <JWT>`.
	JWT string

	// IssuedAt + ExpiresAt — token lifetime; clients should refresh
	// before ExpiresAt.
	IssuedAt  time.Time
	ExpiresAt time.Time

	// Subject — the authenticated identity. Same value VerifyJWT
	// returns; convenience for clients that want it without
	// decoding the JWT themselves.
	Subject Subject
}

// NoopSEP10Validator is the graceful-degradation fallback used when
// the production [internal/auth/sep10] validator can't be built
// (missing signing seed or JWT secret) AND `auth_mode` is not
// `sep10` — the API binary swaps in this Noop so unrelated
// endpoints keep serving while `/v1/auth/sep10/*` returns 503. With
// `auth_mode=sep10` the same missing-config path is a hard startup
// failure instead. Every method returns [ErrNotImplemented]; the
// challenge/token handlers translate to 503 Service Unavailable.
type NoopSEP10Validator struct{}

// Challenge implements [SEP10Validator].
func (NoopSEP10Validator) Challenge(_ context.Context, _ string) (Challenge, error) {
	return Challenge{}, ErrNotImplemented
}

// Verify implements [SEP10Validator].
func (NoopSEP10Validator) Verify(_ context.Context, _ string) (Token, error) {
	return Token{}, ErrNotImplemented
}

// VerifyJWT implements [SEP10Validator].
func (NoopSEP10Validator) VerifyJWT(_ context.Context, _ string) (Subject, error) {
	return Subject{}, ErrNotImplemented
}

// Compile-time check.
var _ SEP10Validator = NoopSEP10Validator{}
