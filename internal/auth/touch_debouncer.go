// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisTouchDebouncer is the Redis-SETNX adapter for the
// `middleware.TouchDebouncer` seam (F-1226 wave 39, codex audit-
// 2026-05-12). Gates calls into `APIKeyStore.TouchUsage` so the
// hot api_keys row gets at most one UPDATE per (keyID, TTL)
// window even under sustained customer traffic.
//
// Key layout: `touch:apikey:<keyID>` with the configured TTL.
// Value is a fixed sentinel — only the existence of the key
// matters; ownership is implicit. The TTL is the safety net for
// a process that crashes between the SETNX and the actual
// TouchUsage call (the next caller after expiry retries).
type RedisTouchDebouncer struct {
	rdb redis.Cmdable
	ttl time.Duration
}

// DefaultTouchDebounceTTL is the recommended SETNX TTL when the
// caller doesn't pass a custom value. 5 minutes matches the
// audit's "debounce to once-per-minute" guidance with safety
// margin (operators reading "last seen" tolerate up-to-5-minute
// staleness; tighter intervals add UPDATE pressure with no
// product benefit).
const DefaultTouchDebounceTTL = 5 * time.Minute

// NewRedisTouchDebouncer constructs a debouncer. rdb MUST be
// non-nil — the api binary only wires this when Redis is
// reachable. Pass `0` for `ttl` to use [DefaultTouchDebounceTTL].
func NewRedisTouchDebouncer(rdb redis.Cmdable, ttl time.Duration) *RedisTouchDebouncer {
	if rdb == nil {
		panic("auth: NewRedisTouchDebouncer: rdb must not be nil")
	}
	if ttl <= 0 {
		ttl = DefaultTouchDebounceTTL
	}
	return &RedisTouchDebouncer{rdb: rdb, ttl: ttl}
}

// touchKey returns the Redis key for a debounce window. Kept
// separate from the F-1255 `signup:lock:` family and the
// F-1218 `signup:` reservation family so the three namespaces
// don't collide; the `touch:apikey:*` family is pre-listed in
// the Redis ACL allow-list (F-1254).
func touchKey(keyID string) string {
	return "touch:apikey:" + keyID
}

// ShouldTouch implements `middleware.TouchDebouncer`. SETNX
// with the configured TTL. Returns (true, nil) on win,
// (false, nil) on contention (already touched within the
// window), and (false, err) on a Redis-side failure. The
// middleware treats every non-success branch as "skip this
// tick" — last_used updates are bookkeeping, not load-bearing.
func (d *RedisTouchDebouncer) ShouldTouch(ctx context.Context, keyID string) (bool, error) {
	if keyID == "" {
		return false, nil
	}
	ok, err := d.rdb.SetNX(ctx, touchKey(keyID), "1", d.ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx %s: %w", touchKey(keyID), err)
	}
	return ok, nil
}
