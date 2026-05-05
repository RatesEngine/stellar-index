package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/platform"
)

// PostgresAPIKeyValidator reads keys from the Postgres
// `api_keys` table (the dashboard's source of truth) with an
// optional Redis read-through cache.
//
// This is the cutover validator: keys minted by the dashboard
// (`internal/api/v1/dashboardkeys`) authenticate without a
// separate mirror-write step. Existing Redis-only keys (minted
// by `/v1/signup` before the cutover) still work — the cache is
// consulted first and falls back to Postgres only on miss, so
// the validator transparently serves both populations until
// every legacy record has been rotated through dashboard mint.
//
// On a Postgres-served lookup the result is written back into
// the Redis cache so subsequent requests for the same key
// short-circuit at the cache. The dashboard's Revoke handler is
// responsible for DEL'ing the cache entry; we don't TTL-out
// records aggressively because revocations are a cold path.
//
// On Redis I/O failures we degrade-not-fail: a transient cache
// outage still authenticates calls via Postgres directly. The
// inverse is not true — Postgres being down means nobody
// authenticates (the keys table is the source of truth).
type PostgresAPIKeyValidator struct {
	keys     platform.APIKeyStore
	accounts platform.AccountStore
	cache    redis.Cmdable
	now      func() time.Time
	// cacheTTL bounds the cache row lifetime — if a revoke DEL
	// fails (rare), the row eventually rolls off rather than
	// authenticating a revoked key indefinitely.
	cacheTTL time.Duration
}

// PostgresValidatorOptions configures a [PostgresAPIKeyValidator].
type PostgresValidatorOptions struct {
	Keys     platform.APIKeyStore
	Accounts platform.AccountStore
	// Cache, when non-nil, enables the Redis read-through path.
	// Recommended in production; nil falls through to per-request
	// Postgres lookups (workable but ~10x slower than the cache hit).
	Cache    redis.Cmdable
	Now      func() time.Time
	CacheTTL time.Duration
}

// NewPostgresAPIKeyValidator constructs the cutover validator.
// Returns an error if the required platform stores aren't wired
// — the validator is useless without them.
func NewPostgresAPIKeyValidator(opts PostgresValidatorOptions) (*PostgresAPIKeyValidator, error) {
	if opts.Keys == nil {
		return nil, errors.New("auth: PostgresAPIKeyValidator: Keys store is required")
	}
	if opts.Accounts == nil {
		return nil, errors.New("auth: PostgresAPIKeyValidator: Accounts store is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	ttl := opts.CacheTTL
	if ttl == 0 {
		ttl = 1 * time.Hour
	}
	return &PostgresAPIKeyValidator{
		keys:     opts.Keys,
		accounts: opts.Accounts,
		cache:    opts.Cache,
		now:      now,
		cacheTTL: ttl,
	}, nil
}

// Lookup implements [APIKeyValidator]. Cache → Postgres → 401.
func (v *PostgresAPIKeyValidator) Lookup(ctx context.Context, key string) (Subject, error) {
	if key == "" {
		return Subject{}, ErrUnauthorized
	}
	hexHash := hashAPIKey(key)

	// 1. Cache.
	if v.cache != nil {
		if sub, ok, err := v.cacheLookup(ctx, hexHash); ok {
			return sub, err
		}
	}

	// 2. Postgres canonical lookup. Hash is the key column.
	rawHash, err := decodeHexHash(hexHash)
	if err != nil {
		// Unreachable — hashAPIKey produces a valid hex string.
		return Subject{}, fmt.Errorf("auth: hash decode: %w", err)
	}
	pgKey, err := v.keys.GetByHash(ctx, rawHash)
	if err != nil {
		if errors.Is(err, platform.ErrNotFound) {
			return Subject{}, ErrUnauthorized
		}
		return Subject{}, fmt.Errorf("auth: postgres lookup: %w", err)
	}
	if !pgKey.RevokedAt.IsZero() {
		return Subject{}, ErrUnauthorized
	}
	if !pgKey.ExpiresAt.IsZero() && !v.now().Before(pgKey.ExpiresAt) {
		return Subject{}, ErrTokenExpired
	}

	acct, err := v.accounts.Get(ctx, pgKey.AccountID)
	if err != nil {
		// Orphaned key (FK should prevent this, but defensive). Treat
		// as unauthorised — the operator log captures the why.
		return Subject{}, ErrUnauthorized
	}
	if acct.Status != platform.AccountActive {
		return Subject{}, ErrUnauthorized
	}

	sub := Subject{
		Identifier:      "acct:" + acct.Slug,
		Tier:            pgTierToAuthTier(pgKey.Tier),
		KeyID:           pgKey.ID,
		RateLimitPerMin: pgKey.RateLimitPerMin,
		CreatedAt:       pgKey.CreatedAt,
		Label:           pgKey.Name,
		KeyPrefix:       pgKey.KeyPrefix,
	}

	// 3. Cache write-back. Best-effort; a write failure doesn't
	// affect this request (the caller already has the Subject).
	if v.cache != nil {
		v.cacheStore(ctx, hexHash, sub, pgKey)
	}
	return sub, nil
}

// cacheLookup returns (Subject, hit, sentinelErr). hit=false
// means "miss or transient cache failure — fall through to
// Postgres". When hit=true the second return is the sentinel the
// caller should propagate (or nil for a successful auth).
func (v *PostgresAPIKeyValidator) cacheLookup(ctx context.Context, hexHash string) (Subject, bool, error) {
	raw, err := v.cache.Get(ctx, cachekeys.APIKey(hexHash)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Subject{}, false, nil
	}
	if err != nil {
		// Cache I/O error — degrade-not-fail. Fall through to
		// Postgres so a transient Redis blip doesn't take auth
		// down. The error is intentionally swallowed; an operator
		// log on the parent .Get path catches the cache state.
		return Subject{}, false, nil //nolint:nilerr // deliberate degrade-not-fail
	}
	var rec APIKeyRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		// Corrupt cache entry — same degrade-not-fail rationale
		// as above. Postgres lookup will rebuild the cache row.
		return Subject{}, false, nil //nolint:nilerr // deliberate degrade-not-fail
	}
	if !rec.RevokedAt.IsZero() {
		return Subject{}, true, ErrUnauthorized
	}
	if !rec.ExpiresAt.IsZero() && !v.now().Before(rec.ExpiresAt) {
		return Subject{}, true, ErrTokenExpired
	}
	tier := rec.Tier
	if tier == "" {
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
		KeyPrefix:       rec.KeyPrefix,
	}, true, nil
}

// cacheStore writes the Postgres-derived Subject into Redis with
// the configured TTL. Mirrors the legacy APIKeyRecord JSON shape
// so the cache and the legacy /v1/signup writers share one
// schema.
func (v *PostgresAPIKeyValidator) cacheStore(ctx context.Context, hexHash string, sub Subject, pgKey platform.APIKey) {
	rec := APIKeyRecord{
		KeyID:           sub.KeyID,
		Identifier:      sub.Identifier,
		Label:           sub.Label,
		KeyPrefix:       sub.KeyPrefix,
		Tier:            sub.Tier,
		Scopes:          sub.Scopes,
		RateLimitPerMin: sub.RateLimitPerMin,
		CreatedAt:       sub.CreatedAt,
		ExpiresAt:       pgKey.ExpiresAt,
	}
	body, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = v.cache.Set(ctx, cachekeys.APIKey(hexHash), body, v.cacheTTL).Err()
}

// pgTierToAuthTier maps the platform-side enum to the auth-side
// Tier. Distinct enums because the platform table also covers
// future tier values (e.g. partner) the auth middleware doesn't
// yet have rate-limit buckets for.
func pgTierToAuthTier(t platform.APIKeyTier) Tier {
	switch t {
	case platform.APIKeyTierOperator:
		return TierOperator
	case platform.APIKeyTierPartner, platform.APIKeyTierAPIKey:
		fallthrough
	default:
		return TierAPIKey
	}
}

// decodeHexHash converts a hex string back into its raw byte
// form for the Postgres bytea column lookup.
func decodeHexHash(hexHash string) ([]byte, error) {
	out := make([]byte, len(hexHash)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexNibble(hexHash[i*2])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(hexHash[i*2+1])
		if err != nil {
			return nil, err
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("auth: invalid hex byte %q", c)
}

// InvalidateCachedKey removes a key's cached record from Redis.
// Called by the dashboard's Revoke handler so a revoked key
// stops authenticating immediately rather than waiting for the
// cache TTL to roll it off.
//
// hexHash is the SHA-256 of the plaintext, hex-encoded. Callers
// who only have a key_id need to look up the hash from
// platform.APIKeyStore.Get first — we don't add a Get-by-id
// here because the dashboard handler already has the row.
func (v *PostgresAPIKeyValidator) InvalidateCachedKey(ctx context.Context, hexHash string) error {
	if v.cache == nil {
		return nil
	}
	return v.cache.Del(ctx, cachekeys.APIKey(hexHash)).Err()
}

// Compile-time check.
var _ APIKeyValidator = (*PostgresAPIKeyValidator)(nil)
