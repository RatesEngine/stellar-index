package ratelimit

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Bucket is a per-(client × window) counter.
//
// Safe for concurrent use. One Bucket serves many callers —
// construct a single instance at binary startup and share it across
// handlers.
type Bucket struct {
	rdb    redis.Cmdable
	max    int
	window time.Duration

	// keyPrefix is "rl:" for production; overridable in tests to
	// avoid collisions with other test files using the same miniredis.
	keyPrefix string

	// nowFn is the clock source. time.Now in production; tests
	// override via WithClock to deterministically advance windows.
	nowFn func() time.Time
}

// Option configures a Bucket at construction.
type Option func(*Bucket)

// WithClock overrides the time source used to derive window buckets.
// Primarily for tests that use miniredis's FastForward and need the
// bucket key to advance with it.
func WithClock(now func() time.Time) Option {
	return func(b *Bucket) { b.nowFn = now }
}

// WithKeyPrefix overrides the "rl:" key prefix — useful only when
// sharing a Redis with another non-ratesengine caller, which we don't.
// Exposed for completeness + test isolation.
func WithKeyPrefix(prefix string) Option {
	return func(b *Bucket) { b.keyPrefix = prefix }
}

// Result carries the outcome of [Bucket.Take].
type Result struct {
	// Allowed is true iff the request fits within the current
	// window's budget.
	Allowed bool

	// Remaining is max - current-count after this increment.
	// Clamped to zero when over-limit.
	Remaining int

	// RetryAfter is the time until the window resets. Non-zero
	// only when Allowed is false.
	RetryAfter time.Duration

	// Count is the incremented request count in this window.
	// Useful for debug logging.
	Count int
}

// New constructs a rate-limiter.
//
//   - max    — requests per window.
//   - window — size of the fixed window.
//
// Panics on invalid arguments (zero or negative).
func New(rdb redis.Cmdable, limit int, window time.Duration, opts ...Option) *Bucket {
	if limit <= 0 {
		panic("ratelimit: max must be positive")
	}
	// Windows below 1s would integer-divide to zero in Take()'s
	// bucket-key derivation (`Unix() / int64(window.Seconds())` →
	// divide by zero). The bucket scheme is inherently seconds-
	// based; rejecting sub-second windows fails-fast instead of
	// surfacing the panic at first request.
	if window < time.Second {
		panic("ratelimit: window must be >= 1s")
	}
	b := &Bucket{
		rdb:       rdb,
		max:       limit,
		window:    window,
		keyPrefix: "rl:",
		nowFn:     time.Now,
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Max returns the configured per-window limit. Useful for surfacing
// it in HTTP response headers without threading another value.
func (b *Bucket) Max() int { return b.max }

// Window returns the configured window size.
func (b *Bucket) Window() time.Duration { return b.window }

// lua is the atomic rate-limit check.
//
// KEYS[1]  — the rate-limit key (e.g. "rl:rek_abc:12345")
// ARGV[1]  — TTL in seconds (window * 2 so keys drain)
// ARGV[2]  — max (the limit)
//
// Returns  — two-element array [count, retry_after_seconds].
//
//	retry_after is 0 when allowed.
const lua = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
end
local max_count = tonumber(ARGV[2])
if current > max_count then
  local ttl = redis.call('TTL', KEYS[1])
  if ttl < 0 then ttl = 0 end
  return {current, ttl}
end
return {current, 0}
`

// luaScript is compiled once at package init. go-redis's Script
// caches the SHA inside the value; re-creating it per Take() call
// wasted a SHA-1 + string-alloc on every rate-limit check. One
// compile per process is enough — the Lua source is immutable.
var luaScript = redis.NewScript(lua)

// Take increments the counter for key in the current window and
// returns whether the request is allowed. One Redis round-trip.
//
// Callers should fail open on error — a Redis outage must not take
// the whole API offline.
func (b *Bucket) Take(ctx context.Context, key string) (Result, error) {
	minute := b.nowFn().Unix() / int64(b.window.Seconds())
	// url.QueryEscape the caller-supplied key before concatenating
	// with ":<minute>". Callers pass things like IPv6 addresses (which
	// contain `:`), future API-key strings, SEP-10 account IDs; any
	// `:` in the key would make two distinct keys collide on the same
	// Redis slot — a silent cross-client rate-limit smear. Escaping
	// all RFC 3986 reserved chars also hardens against injection of
	// exotic control bytes. Still human-readable in Redis for ops.
	rlKey := b.keyPrefix + url.QueryEscape(key) + ":" + strconv.FormatInt(minute, 10)
	// Double-window TTL so keys drain naturally. Floor-at-1 guards
	// sub-second windows — Redis treats EXPIRE 0 as "no expiry",
	// which would leak keys forever for any window < 500ms. 1s
	// floor is still finite; the exact value doesn't matter because
	// those windows would roll over on the next Take() anyway.
	ttlSeconds := int(b.window.Seconds() * 2)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}

	resRaw, err := luaScript.Run(ctx, b.rdb, []string{rlKey},
		ttlSeconds, b.max,
	).Result()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: eval: %w", err)
	}

	arr, ok := resRaw.([]any)
	if !ok || len(arr) != 2 {
		return Result{}, fmt.Errorf("ratelimit: unexpected result shape: %T", resRaw)
	}

	count, ok := arr[0].(int64)
	if !ok {
		return Result{}, fmt.Errorf("ratelimit: count not int64: %T", arr[0])
	}
	retryTTL, ok := arr[1].(int64)
	if !ok {
		return Result{}, fmt.Errorf("ratelimit: retry_after not int64: %T", arr[1])
	}

	// Use `count` as the authoritative allow-signal, not retryTTL.
	// The Lua clamps a negative TTL to 0 on the deny path, and Go
	// must not read "retryTTL == 0" as "allowed" — a TTL<0 race
	// (key lazy-evicted between INCR and TTL) would otherwise flip
	// a deny into an allow. Pair-comparing against max_count is the
	// same check the Lua makes; keeping them symmetric eliminates
	// the decoupling bug entirely.
	allowed := int(count) <= b.max
	remaining := b.max - int(count)
	if remaining < 0 {
		remaining = 0
	}
	// RetryAfter is "time until caller can succeed". That's the
	// REMAINING SECONDS IN THE CURRENT WINDOW, not the Redis-side
	// key TTL (which is 2× window for drain purposes). Computing
	// on the Go side instead of reading ttl from Lua means the
	// client's Retry-After header doesn't sit at a drain-padded
	// value that's up to 60× longer than the actual reset. Floor
	// at 1s so the HTTP header is never zero on a denial.
	var retryAfter time.Duration
	if !allowed {
		windowSec := int64(b.window.Seconds())
		elapsed := b.nowFn().Unix() - minute*windowSec
		remainingSec := windowSec - elapsed
		if remainingSec < 1 {
			remainingSec = 1
		}
		retryAfter = time.Duration(remainingSec) * time.Second
	}
	_ = retryTTL // intentionally unused — kept on the wire for debug
	return Result{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: retryAfter,
		Count:      int(count),
	}, nil
}
