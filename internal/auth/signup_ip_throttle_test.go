package auth_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// TestRedisSignupIPThrottle_Allows_UpToCap pins the first
// `Max` increments succeed.
func TestRedisSignupIPThrottle_Allows_UpToCap(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:    3,
		Window: time.Hour,
	})
	ctx := context.Background()
	const ip = "203.0.113.7"

	for i := 0; i < 3; i++ {
		if err := tt.CheckIP(ctx, ip); err != nil {
			t.Fatalf("attempt %d: want nil, got %v", i+1, err)
		}
	}
}

// TestRedisSignupIPThrottle_Blocks_OverCap pins that the (Max+1)th
// increment returns ErrSignupRateLimited.
func TestRedisSignupIPThrottle_Blocks_OverCap(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:    2,
		Window: time.Hour,
	})
	ctx := context.Background()
	const ip = "203.0.113.7"

	for i := 0; i < 2; i++ {
		if err := tt.CheckIP(ctx, ip); err != nil {
			t.Fatalf("attempt %d under cap: %v", i+1, err)
		}
	}
	err := tt.CheckIP(ctx, ip)
	if !errors.Is(err, auth.ErrSignupRateLimited) {
		t.Fatalf("attempt 3 over cap: want ErrSignupRateLimited, got %v", err)
	}
}

// TestRedisSignupIPThrottle_DistinctIPs_IndependentBuckets
// confirms two IPs share no state.
func TestRedisSignupIPThrottle_DistinctIPs_IndependentBuckets(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:    1,
		Window: time.Hour,
	})
	ctx := context.Background()

	if err := tt.CheckIP(ctx, "203.0.113.1"); err != nil {
		t.Fatalf("ip1 first: %v", err)
	}
	if err := tt.CheckIP(ctx, "203.0.113.2"); err != nil {
		t.Fatalf("ip2 first: %v", err)
	}
	if err := tt.CheckIP(ctx, "203.0.113.1"); !errors.Is(err, auth.ErrSignupRateLimited) {
		t.Fatalf("ip1 second: want ErrSignupRateLimited, got %v", err)
	}
	if err := tt.CheckIP(ctx, "203.0.113.2"); !errors.Is(err, auth.ErrSignupRateLimited) {
		t.Fatalf("ip2 second: want ErrSignupRateLimited, got %v", err)
	}
}

// TestRedisSignupIPThrottle_EmptyIP_FallsOpen pins that an
// IP-less request (production shouldn't see — Caddy + Cloudflare
// always populate one) doesn't trigger the throttle.
func TestRedisSignupIPThrottle_EmptyIP_FallsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:    1,
		Window: time.Hour,
	})
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if err := tt.CheckIP(ctx, ""); err != nil {
			t.Fatalf("attempt %d (empty ip): want nil, got %v", i+1, err)
		}
	}
}

// TestRedisSignupIPThrottle_DefaultsApplied confirms zero-value
// options pick the documented defaults (5/hour, "signup-ip:" prefix).
func TestRedisSignupIPThrottle_DefaultsApplied(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{})
	ctx := context.Background()
	const ip = "203.0.113.42"

	for i := 0; i < 5; i++ {
		if err := tt.CheckIP(ctx, ip); err != nil {
			t.Fatalf("default-cap attempt %d: %v", i+1, err)
		}
	}
	if err := tt.CheckIP(ctx, ip); !errors.Is(err, auth.ErrSignupRateLimited) {
		t.Fatalf("default-cap attempt 6: want ErrSignupRateLimited, got %v", err)
	}

	// Confirm the key prefix used (sanity-check the namespace
	// without coupling tightly to the format).
	for _, k := range mr.Keys() {
		if len(k) >= len("signup-ip:") && k[:len("signup-ip:")] == "signup-ip:" {
			return
		}
	}
	t.Errorf("no key with `signup-ip:` prefix found in miniredis (have %v)", mr.Keys())
}

// TestRedisSignupIPThrottle_DwellTime_FailsOpenInsideWindow pins
// the F-0049 / F-0149 inversion: Redis errors observed inside the
// dwell-time window still propagate as wrapped Redis errors (so
// the handler falls open), preserving the transient-blip UX.
func TestRedisSignupIPThrottle_DwellTime_FailsOpenInsideWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	// Close the client immediately to force every CheckIP into the
	// Redis-error path; same shape as ratelimit's
	// TestBucket_TakeReturnsErrorOnRedisOutage.
	if err := rdb.Close(); err != nil {
		t.Fatalf("close redis: %v", err)
	}

	fakeNow := time.Unix(1_750_000_000, 0)
	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:       3,
		Window:    time.Hour,
		DwellTime: 30 * time.Second,
		NowFn:     func() time.Time { return fakeNow },
	})

	err := tt.CheckIP(context.Background(), "203.0.113.7")
	if err == nil {
		t.Fatal("first error: want wrapped Redis err, got nil")
	}
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("first error: must NOT be ErrThrottleUnavailable yet (dwell-clock just armed): %v", err)
	}

	// Advance to dwell-time edge but not past — still inside the
	// fail-open window.
	fakeNow = fakeNow.Add(20 * time.Second)
	err = tt.CheckIP(context.Background(), "203.0.113.7")
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("err inside window: must remain fail-open, got ErrThrottleUnavailable")
	}
	if err == nil {
		t.Fatal("err inside window: want wrapped Redis err, got nil")
	}
}

// TestRedisSignupIPThrottle_DwellTime_FailsClosedAfterWindow pins
// the J40 protection: sustained Redis errors past the dwell-time
// return ErrThrottleUnavailable so the handler emits 503.
func TestRedisSignupIPThrottle_DwellTime_FailsClosedAfterWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if err := rdb.Close(); err != nil {
		t.Fatalf("close redis: %v", err)
	}

	fakeNow := time.Unix(1_750_000_000, 0)
	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:       3,
		Window:    time.Hour,
		DwellTime: 30 * time.Second,
		NowFn:     func() time.Time { return fakeNow },
	})

	// Arm the dwell-clock.
	if err := tt.CheckIP(context.Background(), "203.0.113.7"); err == nil {
		t.Fatal("arm: want err, got nil")
	}

	// Cross the dwell-time threshold. The doc string says ">" so
	// pick a duration that comfortably exceeds 30s.
	fakeNow = fakeNow.Add(31 * time.Second)
	err := tt.CheckIP(context.Background(), "203.0.113.7")
	if !errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("after dwell-time: want ErrThrottleUnavailable, got %v", err)
	}
}

// TestRedisSignupIPThrottle_DwellTime_SuccessResetsClock pins the
// recovery semantics: a single Redis success after an error window
// clears the dwell-clock so a later error starts a fresh window —
// transient blips don't accumulate across recoveries.
func TestRedisSignupIPThrottle_DwellTime_SuccessResetsClock(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	fakeNow := time.Unix(1_750_000_000, 0)
	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:       3,
		Window:    time.Hour,
		DwellTime: 30 * time.Second,
		NowFn:     func() time.Time { return fakeNow },
	})

	// 1. Force a Redis error by setting the bucket's key to a string
	//    that INCR can't increment — miniredis returns
	//    "value is not an integer or out of range".
	const ip = "203.0.113.42"
	// Build the same key shape CheckIP uses to plant a poison value.
	// Window=1h ⇒ windowStart = unix/3600.
	windowStart := fakeNow.Unix() / 3600
	mr.Set("signup-ip:"+ip+":"+strconv.FormatInt(windowStart, 10), "not-a-number")

	err := tt.CheckIP(context.Background(), ip)
	if err == nil {
		t.Fatal("step 1: want INCR err, got nil")
	}
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("step 1: must NOT be ErrThrottleUnavailable (clock just armed): %v", err)
	}

	// 2. Heal Redis (delete the poison key) and run a success.
	mr.Del("signup-ip:" + ip + ":" + strconv.FormatInt(windowStart, 10))
	if err := tt.CheckIP(context.Background(), ip); err != nil {
		t.Fatalf("step 2: want nil after heal, got %v", err)
	}

	// 3. Re-poison Redis and advance past the ORIGINAL dwell-time —
	//    the success in step 2 should have reset the clock so this
	//    error starts a fresh window and falls open (not 503).
	mr.Set("signup-ip:"+ip+":"+strconv.FormatInt(windowStart, 10), "not-a-number")
	fakeNow = fakeNow.Add(60 * time.Second)
	err = tt.CheckIP(context.Background(), ip)
	if err == nil {
		t.Fatal("step 3: want INCR err, got nil")
	}
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("step 3: success in step 2 should have reset the dwell-clock; got ErrThrottleUnavailable")
	}
}

// TestRedisSignupIPThrottle_DwellTime_Disabled pins that a negative
// DwellTime preserves the pre-F-0049 fail-open-always behaviour —
// operators who explicitly opt out never see ErrThrottleUnavailable.
func TestRedisSignupIPThrottle_DwellTime_Disabled(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if err := rdb.Close(); err != nil {
		t.Fatalf("close redis: %v", err)
	}

	fakeNow := time.Unix(1_750_000_000, 0)
	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:       3,
		Window:    time.Hour,
		DwellTime: -1, // disabled
		NowFn:     func() time.Time { return fakeNow },
	})

	// First error arms the (unused) clock.
	if err := tt.CheckIP(context.Background(), "203.0.113.7"); err == nil {
		t.Fatal("arm: want err, got nil")
	}
	// Advance well past any plausible dwell-time.
	fakeNow = fakeNow.Add(10 * time.Minute)
	err := tt.CheckIP(context.Background(), "203.0.113.7")
	if errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Errorf("disabled: must never return ErrThrottleUnavailable, got %v", err)
	}
}

// TestRedisSignupIPThrottle_DwellTime_DefaultApplied confirms the
// zero-value DwellTime picks DefaultSignupThrottleDwellTime (30s).
func TestRedisSignupIPThrottle_DwellTime_DefaultApplied(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if err := rdb.Close(); err != nil {
		t.Fatalf("close redis: %v", err)
	}

	fakeNow := time.Unix(1_750_000_000, 0)
	tt := auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{
		Max:    3,
		Window: time.Hour,
		// DwellTime intentionally unset.
		NowFn: func() time.Time { return fakeNow },
	})

	if err := tt.CheckIP(context.Background(), "203.0.113.7"); err == nil {
		t.Fatal("arm: want err, got nil")
	}
	// Just under default 30s — must still fail-open.
	fakeNow = fakeNow.Add(29 * time.Second)
	if err := tt.CheckIP(context.Background(), "203.0.113.7"); errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("at t+29s: must fail-open under default 30s, got ErrThrottleUnavailable")
	}
	// Past default 30s — must fail-closed.
	fakeNow = fakeNow.Add(5 * time.Second) // total t+34s
	if err := tt.CheckIP(context.Background(), "203.0.113.7"); !errors.Is(err, auth.ErrThrottleUnavailable) {
		t.Fatalf("at t+34s: want ErrThrottleUnavailable under default 30s, got %v", err)
	}
}

// keep strconv import live in case future tests want explicit
// window-bucket assertions.
var _ = strconv.Itoa
