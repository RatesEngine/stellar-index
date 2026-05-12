package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSignupTracker implements [v1.SignupTracker] (the duplicate-
// signup guard) against Redis.
//
// Stores a flat hash:
//
//	signup:email:<sha256-hex>  →  <key_id>
//
// The mapping is set-once on first signup; a second signup for the
// same email-hash reads the existing key_id and the handler returns
// 409. There's no TTL — signups are intended to be permanent
// (operator-side cleanup if a customer needs the email freed).
//
// Safe for concurrent use.
type RedisSignupTracker struct {
	rdb redis.Cmdable
}

// NewRedisSignupTracker constructs a tracker. rdb MUST be non-nil
// — the api binary only wires this when Redis is reachable.
func NewRedisSignupTracker(rdb redis.Cmdable) *RedisSignupTracker {
	if rdb == nil {
		panic("auth: NewRedisSignupTracker: rdb must not be nil")
	}
	return &RedisSignupTracker{rdb: rdb}
}

// signupKey returns the Redis key for an email-hash. Centralised so
// the format is the single source of truth.
func signupKey(emailHash string) string {
	return "signup:email:" + emailHash
}

// LookupByEmailHash implements [v1.SignupTracker.LookupByEmailHash].
// Returns "" + nil for "no prior signup"; non-empty + nil for the
// existing key_id; "" + err for a Redis-side problem.
func (t *RedisSignupTracker) LookupByEmailHash(ctx context.Context, emailHash string) (string, error) {
	val, err := t.rdb.Get(ctx, signupKey(emailHash)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis get %s: %w", signupKey(emailHash), err)
	}
	return val, nil
}

// ErrSignupEmailReserved is returned by ReserveEmail when the
// email-hash already has a reservation or a confirmed signup —
// the caller surfaces 409 to the customer. F-1218 (codex
// audit-2026-05-12).
var ErrSignupEmailReserved = errors.New("auth: signup email already reserved")

// ReserveEmail atomically claims `emailHash` for an in-flight
// signup. SETNX with a "pending" placeholder so:
//
//   - First caller in a race wins, gets nil. Caller proceeds to
//     mint the key, then calls [ConfirmSignup] to upgrade the
//     placeholder to the real key_id.
//   - Second caller loses, gets [ErrSignupEmailReserved] before
//     any key mint happens. F-1218 (codex audit-2026-05-12):
//     pre-fix the lookup+mint+mark sequence was non-atomic, so
//     two concurrent signups for the same email each minted a
//     key and only the SETNX in MarkSignup resolved the race —
//     the customer ended up with one durable mapping but two
//     leaked keys.
//
// The placeholder has a 5-minute TTL so a crash between Reserve
// and Confirm doesn't strand the email forever. ConfirmSignup
// clears the TTL so durable mappings outlive process restarts.
//
// `emailHash` is the sha256 hex of the lowercased email; same
// shape [LookupByEmailHash] expects.
func (t *RedisSignupTracker) ReserveEmail(ctx context.Context, emailHash string) error {
	const placeholder = "pending"
	ok, err := t.rdb.SetNX(ctx, signupKey(emailHash), placeholder, signupReservationTTL).Result()
	if err != nil {
		return fmt.Errorf("redis setnx %s: %w", signupKey(emailHash), err)
	}
	if !ok {
		return ErrSignupEmailReserved
	}
	return nil
}

// signupReservationTTL is the lifetime of a pending reservation.
// 5 minutes is well above the worst-case mint latency (we've
// observed sub-second p99) and short enough that a crashed
// handler doesn't strand a customer's email for hours.
const signupReservationTTL = 5 * time.Minute

// MarkSignup implements [v1.SignupTracker.MarkSignup]. Upgrades
// the reservation placeholder to the real key_id and clears the
// TTL so the durable mapping outlives process restarts.
//
// Safe to call even when ReserveEmail wasn't called (single-stage
// flow): the SET overrides any prior value and persists. The
// race-window-tight path is Reserve → mint → MarkSignup; the
// single-stage path is just MarkSignup.
func (t *RedisSignupTracker) MarkSignup(ctx context.Context, emailHash, keyID string) error {
	// SET … KEEPTTL=false (default) clears any TTL set by ReserveEmail.
	if err := t.rdb.Set(ctx, signupKey(emailHash), keyID, 0).Err(); err != nil {
		return fmt.Errorf("redis set %s: %w", signupKey(emailHash), err)
	}
	return nil
}
