package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSignupIPThrottle implements [v1.SignupIPThrottle] (the per-IP
// signup rate-limit boundary, declared in v1 to keep the v1 package
// the source of truth for its own boundaries) with a sliding-window
// Redis counter.
//
// The signup endpoint sees one request per attempted account; the
// global anonymous rate limit caps at 60/min per IP — plenty for
// browsing the public surfaces but lets a single IP bulk-mint
// 60 accounts/min × 60 min = 3,600/hr of email→key_id pairs.
// The default 5/hour cap here closes that vector while still
// letting a legitimate operator onboarding a small team through a
// single shared egress complete normally; operators tune up via
// `signup_ip_max_per_window` in the API config.
//
// F-1232 (audit-2026-05-12).
type RedisSignupIPThrottle struct {
	rdb       redis.UniversalClient
	max       int
	window    time.Duration
	keyPrefix string
}

// SignupIPThrottleOptions tunes a [RedisSignupIPThrottle].
type SignupIPThrottleOptions struct {
	// Max is the maximum number of signups permitted per IP within
	// Window. Default 5 — tight enough to block bulk-mint, loose
	// enough that a legitimate operator onboarding a small team
	// through a single shared egress completes normally.
	Max int
	// Window is the rolling-window length. Default 1 hour.
	Window time.Duration
	// KeyPrefix is the Redis key namespace for this throttle.
	// Default "signup-ip:". Override only in tests.
	KeyPrefix string
}

// NewRedisSignupIPThrottle constructs the throttle. rdb MUST be
// non-nil; pass a no-op v1.SignupIPThrottle (or simply leave the
// Options.SignupIPThrottle field nil) for deployments without
// Redis.
func NewRedisSignupIPThrottle(rdb redis.UniversalClient, opts SignupIPThrottleOptions) *RedisSignupIPThrottle {
	if rdb == nil {
		panic("auth: NewRedisSignupIPThrottle: rdb must not be nil")
	}
	if opts.Max <= 0 {
		opts.Max = 5
	}
	if opts.Window <= 0 {
		opts.Window = time.Hour
	}
	if opts.KeyPrefix == "" {
		opts.KeyPrefix = "signup-ip:"
	}
	return &RedisSignupIPThrottle{
		rdb:       rdb,
		max:       opts.Max,
		window:    opts.Window,
		keyPrefix: opts.KeyPrefix,
	}
}

// CheckIP increments the per-IP counter for the current window.
// Returns nil while under the cap, [ErrSignupRateLimited] once the
// cap is reached, and a wrapped Redis error on transport failure
// (the handler treats Redis failures as fail-open — better than
// taking signup offline because Redis blipped).
func (t *RedisSignupIPThrottle) CheckIP(ctx context.Context, ip string) error {
	if ip == "" {
		// No usable IP — let the request through; the global
		// rate-limit middleware also failed to find one and capped
		// via its own fallback. F-1232 hardens against IP-rotators,
		// not against IP-less direct calls (which production
		// shouldn't see — Caddy + Cloudflare always populate one).
		return nil
	}
	// Use the same window-bucket trick as ratelimit.Bucket: round
	// the current minute to the window. Sliding-window approximate;
	// gives at most 2× the cap during a window-crossing burst,
	// which is acceptable for an abuse-prevention threshold (not
	// for a strict billing meter).
	windowStart := time.Now().Unix() / int64(t.window.Seconds())
	key := fmt.Sprintf("%s%s:%d", t.keyPrefix, ip, windowStart)

	count, err := t.rdb.Incr(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("signup throttle: INCR %s: %w", key, err)
	}
	// Set the TTL on first increment so the bucket drains on its own.
	if count == 1 {
		// Best-effort EXPIRE; if it fails the key persists until the
		// next manual cleanup but the next increment still works.
		_ = t.rdb.Expire(ctx, key, t.window*2).Err()
	}
	if int(count) > t.max {
		return ErrSignupRateLimited
	}
	return nil
}
