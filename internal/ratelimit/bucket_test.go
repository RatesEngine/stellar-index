package ratelimit_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/ratelimit"
)

func newRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

func TestBucket_AllowsUpToLimit(t *testing.T) {
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 3, time.Minute)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		r, err := b.Take(ctx, "key")
		if err != nil {
			t.Fatalf("take %d: %v", i, err)
		}
		if !r.Allowed {
			t.Errorf("request %d should be allowed", i)
		}
		if r.Count != i {
			t.Errorf("count = %d, want %d", r.Count, i)
		}
		if r.Remaining != 3-i {
			t.Errorf("remaining = %d, want %d", r.Remaining, 3-i)
		}
	}
}

func TestBucket_RejectsOverLimit(t *testing.T) {
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 2, time.Minute)
	ctx := context.Background()

	// Use up the budget.
	for i := 0; i < 2; i++ {
		r, _ := b.Take(ctx, "key")
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Third request should be denied.
	r, err := b.Take(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}
	if r.Allowed {
		t.Error("third request should be denied")
	}
	if r.RetryAfter <= 0 {
		t.Errorf("retry_after should be > 0 when denied, got %v", r.RetryAfter)
	}
	if r.Remaining != 0 {
		t.Errorf("remaining should be 0 when over, got %d", r.Remaining)
	}
}

func TestBucket_WindowsAreIsolated(t *testing.T) {
	rdb, mr := newRedis(t)

	// Inject a controllable clock so the minute-bucket derivation
	// advances with the fake redis TTL clock. Production uses
	// time.Now directly.
	fakeNow := time.Unix(1_750_000_000, 0) // arbitrary fixed point
	b := ratelimit.New(rdb, 1, time.Minute,
		ratelimit.WithClock(func() time.Time { return fakeNow }),
	)
	ctx := context.Background()

	// Exhaust window 1.
	r1, _ := b.Take(ctx, "k")
	if !r1.Allowed {
		t.Fatal("first request should be allowed")
	}
	r2, _ := b.Take(ctx, "k")
	if r2.Allowed {
		t.Fatal("second in same window should be denied")
	}

	// Advance BOTH Go clock (bucket key derivation) and redis TTL
	// clock (key expiration) by 1 minute.
	fakeNow = fakeNow.Add(61 * time.Second)
	mr.FastForward(61 * time.Second)

	r3, _ := b.Take(ctx, "k")
	if !r3.Allowed {
		t.Error("new window should allow again")
	}
}

func TestBucket_KeysAreIndependent(t *testing.T) {
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)
	ctx := context.Background()

	// Exhaust one key; the other should still be allowed.
	if r, _ := b.Take(ctx, "alice"); !r.Allowed {
		t.Fatal("alice 1st should be allowed")
	}
	if r, _ := b.Take(ctx, "alice"); r.Allowed {
		t.Fatal("alice 2nd should be denied")
	}
	if r, _ := b.Take(ctx, "bob"); !r.Allowed {
		t.Fatal("bob 1st should be allowed (independent key)")
	}
}

func TestBucket_ColonInKeyDoesNotCollide(t *testing.T) {
	// Keys containing `:` (IPv6 addresses, future API-key formats)
	// must not collide with distinct keys. Previously Take() built
	// the Redis key as `rl:<key>:<minute>`, so two IPv6 clients
	// whose addresses share a prefix could land on the same slot.
	// url.QueryEscape in Take() closes this.
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)
	ctx := context.Background()

	// Two realistic IPv6 addresses. Without escaping, the bucket
	// key for "2001:db8::1" in minute M ends with ":<M>"; for
	// "2001:db8::1:<M>" in minute 0 also ends with ":<M>". The
	// safety here is that neither collides with the other OR with
	// the minute suffix.
	if r, _ := b.Take(ctx, "2001:db8::1"); !r.Allowed {
		t.Fatal("client A 1st should be allowed")
	}
	if r, _ := b.Take(ctx, "2001:db8::1"); r.Allowed {
		t.Fatal("client A 2nd should be denied (same key)")
	}
	if r, _ := b.Take(ctx, "2001:db8::2"); !r.Allowed {
		t.Fatal("client B 1st should be allowed (distinct IPv6)")
	}
}

func TestBucket_AtomicUnderConcurrency(t *testing.T) {
	// Atomicity check — fire 100 concurrent takes with limit=5 and
	// verify exactly 5 are allowed. A non-atomic INCR+EXPIRE would
	// let duplicates through.
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 5, time.Minute)
	ctx := context.Background()

	const parallel = 100
	results := make(chan bool, parallel)

	for i := 0; i < parallel; i++ {
		go func() {
			r, err := b.Take(ctx, "concurrent")
			if err != nil {
				results <- false
				return
			}
			results <- r.Allowed
		}()
	}

	allowed := 0
	for i := 0; i < parallel; i++ {
		if <-results {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("got %d allowed (want exactly 5)", allowed)
	}
}

func TestBucket_RetryAfterIsWindowRemaining(t *testing.T) {
	rdb, _ := newRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)
	ctx := context.Background()

	_, _ = b.Take(ctx, "k")  // consume
	r, _ := b.Take(ctx, "k") // should reject
	if r.Allowed {
		t.Fatal("second should be denied")
	}
	// RetryAfter is "seconds until caller can succeed" = remaining
	// in the current fixed window, not Redis's drain TTL. Window
	// is 60s, so RetryAfter must be in [1, 60] — never exceed the
	// window size (old bug: it was Redis's 2×window drain TTL).
	if r.RetryAfter < time.Second || r.RetryAfter > time.Minute {
		t.Errorf("retry_after = %v (want [1s, 60s] = remaining-in-window)", r.RetryAfter)
	}
}

func TestBucket_DenyHoldsWhenTTLReportsNegative(t *testing.T) {
	// Regression: the Lua clamps a negative TTL to 0 on the deny path
	// (see bucket.go's `if ttl < 0 then ttl = 0`), so Go MUST NOT read
	// "retryTTL == 0" as "allowed". Use `count > max` as the
	// authoritative signal.
	//
	// Reproduce the condition by stripping the TTL from the bucket's
	// Redis key via miniredis's Persist — simulating the rare race
	// where a key survives past its EXPIRE (e.g. lazy eviction ordering).
	rdb, mr := newRedis(t)
	fakeNow := time.Unix(1_750_000_000, 0)
	b := ratelimit.New(rdb, 1, time.Minute,
		ratelimit.WithClock(func() time.Time { return fakeNow }),
	)
	ctx := context.Background()

	// Consume the budget.
	if r, _ := b.Take(ctx, "k"); !r.Allowed {
		t.Fatal("first should be allowed")
	}

	// Derive the bucket key the same way Take() does and strip its
	// TTL. Under the old code, the next Take() would see retryTTL=0
	// and incorrectly report Allowed=true even though count > max.
	minute := fakeNow.Unix() / int64(time.Minute.Seconds())
	bucketKey := "rl:k:" + strconv.FormatInt(minute, 10)
	if _, err := mr.Get(bucketKey); err != nil {
		t.Fatalf("bucket key %q missing: %v", bucketKey, err)
	}
	mr.SetTTL(bucketKey, 0) // 0 → no TTL in miniredis

	r, err := b.Take(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if r.Allowed {
		t.Error("denial must survive TTL<0 race — got Allowed=true with count > max")
	}
	if r.Count <= 1 {
		t.Errorf("expected count > max after second take, got %d", r.Count)
	}
}

func TestBucket_ZeroArgsPanic(t *testing.T) {
	rdb, _ := newRedis(t)

	for _, tc := range []struct {
		name   string
		max    int
		window time.Duration
	}{
		{"zero max", 0, time.Minute},
		{"negative max", -1, time.Minute},
		{"zero window", 5, 0},
		{"negative window", 5, -time.Second},
		{"sub-second window", 5, 500 * time.Millisecond},
		{"millisecond window", 5, time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			ratelimit.New(rdb, tc.max, tc.window)
		})
	}
}
