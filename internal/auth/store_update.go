package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
)

// ErrKeyNotFound is returned by [RedisAPIKeyStore.UpdateRateLimit]
// when no key with the supplied KeyID was found in Redis. Operators
// see this when they typo a KeyID on the upgrade-key CLI; downstream
// callers (Stripe webhook handler) treat it as "this customer never
// signed up — refund the payment, ask them to sign up first."
var ErrKeyNotFound = errors.New("auth: key_id not found")

// UpdateRateLimit lifts (or lowers) the per-minute rate-limit budget
// of an existing API key, identified by its public KeyID. Used by:
//
//   - `ratesengine-ops upgrade-key` (operator-side manual paid
//     upgrades pre-Stripe-webhook).
//   - The future Stripe webhook handler that fires on
//     `payment_intent.succeeded` and lifts the customer's keys to
//     the paid tier they bought.
//
// Implementation: SCANs the `apikey:*` keyspace until it finds the
// record whose KeyID matches. O(N) in key count — fine for v1's
// thousands-of-keys scale; if we ever need to scale into the
// hundreds-of-thousands range, add a `apikey-byid:<keyid>` index
// in Create + drop the SCAN.
//
// Returns the updated record (with the new RateLimitPerMin)
// and nil on success, or [ErrKeyNotFound] if no matching key
// exists, or a wrapped error on Redis I/O failure.
func (s *RedisAPIKeyStore) UpdateRateLimit(ctx context.Context, keyID string, newRateLimitPerMin int) (APIKeyRecord, error) {
	if keyID == "" {
		return APIKeyRecord{}, errors.New("auth: UpdateRateLimit: keyID is required")
	}
	if newRateLimitPerMin < 0 {
		return APIKeyRecord{}, fmt.Errorf("auth: UpdateRateLimit: rate-limit %d must be >= 0 (zero means tier default)", newRateLimitPerMin)
	}

	iter := s.rdb.Scan(ctx, 0, cachekeys.APIKey("*"), 1000).Iterator()
	for iter.Next(ctx) {
		k := iter.Val()
		raw, err := s.rdb.Get(ctx, k).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				// Key vanished between SCAN and GET — race with a
				// delete. Skip; the next SCAN cursor will continue.
				continue
			}
			return APIKeyRecord{}, fmt.Errorf("auth: UpdateRateLimit: redis get %s: %w", k, err)
		}
		var rec APIKeyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			// Malformed record — log-and-continue rather than abort
			// the whole upgrade scan. Operator can clean these up
			// out-of-band; the customer-facing UpdateRateLimit path
			// shouldn't fail because some other key on the box has
			// drift.
			continue
		}
		if rec.KeyID != keyID {
			continue
		}

		// Found it. Apply the new rate-limit + write back.
		rec.RateLimitPerMin = newRateLimitPerMin
		body, err := json.Marshal(rec)
		if err != nil {
			return APIKeyRecord{}, fmt.Errorf("auth: UpdateRateLimit: marshal: %w", err)
		}
		if err := s.rdb.Set(ctx, k, body, 0).Err(); err != nil {
			return APIKeyRecord{}, fmt.Errorf("auth: UpdateRateLimit: redis set %s: %w", k, err)
		}
		return rec, nil
	}
	if err := iter.Err(); err != nil {
		return APIKeyRecord{}, fmt.Errorf("auth: UpdateRateLimit: redis scan: %w", err)
	}
	return APIKeyRecord{}, ErrKeyNotFound
}
