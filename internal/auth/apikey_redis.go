package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
)

// RedisAPIKeyValidator implements [APIKeyValidator] backed by Redis.
//
// Storage shape (one record per key):
//
//	KEY   apikey:<sha256-hex>
//	VALUE JSON [APIKeyRecord]
//
// Plaintext keys are never stored; the lookup hashes the
// caller-supplied bytes with SHA-256 and looks up by hash. A Redis
// dump leaks owner identifiers and tier mapping but never the key
// material itself.
//
// Lookup errors:
//
//   - record absent       → [ErrUnauthorized]
//   - revoked_at set      → [ErrUnauthorized]
//   - expires_at past     → [ErrTokenExpired]
//   - record undecodable  → wrapped non-sentinel (operator log signal)
//   - Redis I/O failure   → wrapped non-sentinel (middleware → 503)
//
// Concurrency: safe for use across goroutines — every Lookup is one
// GET. The validator carries no mutable state.
type RedisAPIKeyValidator struct {
	rdb redis.Cmdable
	now func() time.Time
}

// APIKeyRecord is the JSON shape stored at `apikey:<hash>`. The
// admin/seeding path is responsible for marshalling this; the
// validator only unmarshals.
//
// All time fields are RFC 3339; absent fields decode to the zero
// time and are interpreted as "no constraint" (no expiry / not
// revoked).
type APIKeyRecord struct {
	// KeyID — public-safe identifier for this key. Distinct from
	// the secret hash (the Redis key) so it can appear in logs and
	// /v1/account/me responses without leaking the credential.
	// Generated at issuance time; stable for the key's lifetime.
	KeyID string `json:"key_id"`

	// Identifier — owner-account reference. Plain string; the
	// validator passes it through to [Subject.Identifier]. Used as
	// the rate-limit bucket key for the apikey tier.
	Identifier string `json:"identifier"`

	// Label — human-readable name set by the customer at creation
	// (`/v1/account/keys` POST body). Unused by the auth layer;
	// surfaced via /v1/account/me. Optional.
	Label string `json:"label,omitempty"`

	// Tier — the [Tier] this key authenticates as. Production
	// records carry [TierAPIKey]; an operator key may use
	// [TierOperator] to unlock admin endpoints.
	Tier Tier `json:"tier"`

	// Scopes — optional capability list. Empty slice and absent are
	// equivalent ("no special scopes").
	Scopes []string `json:"scopes,omitempty"`

	// RateLimitPerMin — overrides the per-tier default (zero means
	// "use the tier default"). Set to a non-zero value for paid
	// customers on a custom plan.
	RateLimitPerMin int `json:"rate_limit_per_min,omitempty"`

	// CreatedAt — when the key was issued. Required — the
	// /v1/account/me response surfaces it. The store sets it on
	// Create; the validator passes it through.
	CreatedAt time.Time `json:"created_at,omitempty"`

	// ExpiresAt — zero means never. A non-zero value in the past
	// triggers [ErrTokenExpired].
	ExpiresAt time.Time `json:"expires_at,omitempty"`

	// RevokedAt — zero means active. Any non-zero value (even in
	// the future, which would be a bug in the writer) triggers
	// [ErrUnauthorized].
	RevokedAt time.Time `json:"revoked_at,omitempty"`
}

// RedisOption configures a [RedisAPIKeyValidator] at construction.
type RedisOption func(*RedisAPIKeyValidator)

// WithClock overrides the time source used for expiry comparison.
// Production uses time.Now; tests inject a fixed clock.
func WithClock(now func() time.Time) RedisOption {
	return func(v *RedisAPIKeyValidator) { v.now = now }
}

// NewRedisAPIKeyValidator constructs a validator that reads records
// from rdb. rdb MUST be non-nil — callers wire this only after
// confirming Redis is available; the auth middleware fails-loud at
// 503 if the validator field is left as the Noop stub.
func NewRedisAPIKeyValidator(rdb redis.Cmdable, opts ...RedisOption) *RedisAPIKeyValidator {
	if rdb == nil {
		panic("auth: NewRedisAPIKeyValidator: rdb must not be nil")
	}
	v := &RedisAPIKeyValidator{rdb: rdb, now: time.Now}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Lookup implements [APIKeyValidator]. One Redis GET per call;
// translates the record's expiry/revocation into the correct
// sentinel error.
func (v *RedisAPIKeyValidator) Lookup(ctx context.Context, key string) (Subject, error) {
	if key == "" {
		// Unreachable from the middleware (which short-circuits on
		// empty), but the validator is the trust boundary — an
		// admin tool that calls this directly must not be able to
		// authenticate as "the empty-string subject".
		return Subject{}, ErrUnauthorized
	}
	hash := hashAPIKey(key)
	raw, err := v.rdb.Get(ctx, cachekeys.APIKey(hash)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Subject{}, ErrUnauthorized
	}
	if err != nil {
		return Subject{}, fmt.Errorf("auth: apikey redis get: %w", err)
	}

	var rec APIKeyRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		// Malformed record is operator-side corruption, not caller
		// fault. Wrap a non-sentinel so the middleware's default
		// branch returns 401 (the safer of the two — we won't
		// surface details about why); a parallel log line on the
		// admin side will catch the corruption.
		return Subject{}, fmt.Errorf("auth: apikey record decode: %w", err)
	}

	if !rec.RevokedAt.IsZero() {
		return Subject{}, ErrUnauthorized
	}
	if !rec.ExpiresAt.IsZero() && !v.now().Before(rec.ExpiresAt) {
		return Subject{}, ErrTokenExpired
	}

	tier := rec.Tier
	if tier == "" {
		// Records seeded without an explicit tier default to the
		// apikey tier — the most-common case. An operator key
		// must set tier=operator explicitly.
		tier = TierAPIKey
	}
	return Subject{
		Identifier:      rec.Identifier,
		Tier:            tier,
		Scopes:          rec.Scopes,
		KeyID:           rec.KeyID,
		RateLimitPerMin: rec.RateLimitPerMin,
		CreatedAt:       rec.CreatedAt,
		Label:           rec.Label,
	}, nil
}

// HashAPIKey returns the hex-encoded SHA-256 of key. Exposed for
// admin tooling that seeds records into Redis — the admin path
// computes the hash, builds the record, calls
// [cachekeys.APIKey](hash), and SET'S the JSON bytes. Lookup re-
// derives the same hash on read.
func HashAPIKey(key string) string { return hashAPIKey(key) }

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Compile-time check.
var _ APIKeyValidator = (*RedisAPIKeyValidator)(nil)
