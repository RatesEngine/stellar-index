package sep10

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// RedisReplayGuard is the production [ReplayGuard] backed by Redis
// SETNX with TTL. Each successfully-verified SEP-10 challenge tx
// hash claims a key for the remaining challenge-window lifetime;
// a second submission of the same signed XDR finds the key already
// present and returns [auth.ErrUnauthorized].
//
// F-1224 (audit-2026-05-12). The 15-min default ChallengeTTL means
// the dedupe key set is bounded at ~ ChallengeTTL × max-verify-rate,
// which on R1 is well under 1 MB even at 1k verifies/sec.
//
// Cache key prefix:
//
//	sep10:seen:<sha256-base64-url-no-pad of signedXDR>
//
// Owned by this file rather than internal/cachekeys/ because the
// SEP-10 replay set is conceptually an auth concern, not a price-
// cache one — the cachekeys package is the price-cache namespace
// per ADR-0007.
type RedisReplayGuard struct {
	rdb redis.UniversalClient
}

// NewRedisReplayGuard constructs a replay guard against the supplied
// Redis client. Callers typically pass the same client used by the
// rest of the auth subsystem (`internal/auth/apikey_redis.go`).
func NewRedisReplayGuard(rdb redis.UniversalClient) *RedisReplayGuard {
	return &RedisReplayGuard{rdb: rdb}
}

// MarkSeenIfFresh claims the dedupe slot for txHash. Returns
// [auth.ErrUnauthorized] when the slot is already taken (replay).
// Other Redis errors propagate as-is so callers can distinguish
// "rejected as replay" from "couldn't reach Redis to check".
func (g *RedisReplayGuard) MarkSeenIfFresh(ctx context.Context, txHash string, ttl time.Duration) error {
	key := redisReplayKey(txHash)
	ok, err := g.rdb.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return fmt.Errorf("sep10 replay-guard: SETNX %s: %w", key, err)
	}
	if !ok {
		return fmt.Errorf("%w: challenge already redeemed", auth.ErrUnauthorized)
	}
	return nil
}

// redisReplayKey returns the Redis namespace key for a hashed
// challenge tx. Sole-builder pattern matches the cachekeys/
// convention; tests can compare keys without re-deriving.
func redisReplayKey(txHash string) string {
	return "sep10:seen:" + txHash
}

// Compile-time interface conformance check.
var _ ReplayGuard = (*RedisReplayGuard)(nil)

// ErrReplayGuardUnavailable is returned by callers that wrapped a
// nil ReplayGuard but were configured to require one. Exposed so
// the binary wiring can fail-loud at startup rather than silently
// disabling replay defence in production.
var ErrReplayGuardUnavailable = errors.New("sep10: ReplayGuard required but not configured")
