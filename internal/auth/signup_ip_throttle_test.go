package auth_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/auth"
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

// keep strconv import live in case future tests want explicit
// window-bucket assertions.
var _ = strconv.Itoa
