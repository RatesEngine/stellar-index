package auth

import (
	"context"
	"errors"
	"fmt"

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

// MarkSignup implements [v1.SignupTracker.MarkSignup]. SETNX-style:
// won't overwrite a prior mapping if one was created in a race
// (the lookup-then-create sequence isn't atomic; if two signups for
// the same email land on the same Redis tick, both pass the lookup
// check, both call MarkSignup, only the first wins. The second
// caller's key is still minted but the lookup is stable).
func (t *RedisSignupTracker) MarkSignup(ctx context.Context, emailHash, keyID string) error {
	ok, err := t.rdb.SetNX(ctx, signupKey(emailHash), keyID, 0).Result()
	if err != nil {
		return fmt.Errorf("redis setnx %s: %w", signupKey(emailHash), err)
	}
	if !ok {
		// Race lost — the existing mapping wins. Not an error per se;
		// the handler's response was already sent. Log-only is the
		// right behaviour at this layer; the handler logger picks up
		// the warning.
		return nil
	}
	return nil
}
