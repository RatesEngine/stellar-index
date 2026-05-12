package auth

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisTouchDebouncer_FirstWinsRestSkip — first call in the
// window returns true; subsequent calls within the TTL return
// false; after the TTL elapses the next caller wins again.
// F-1226 wave 39 (codex audit-2026-05-12).
func TestRedisTouchDebouncer_FirstWinsRestSkip(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	d := NewRedisTouchDebouncer(rdb, 5*time.Minute)
	ctx := context.Background()

	ok, err := d.ShouldTouch(ctx, "K1")
	if err != nil || !ok {
		t.Fatalf("first ShouldTouch: ok=%v err=%v", ok, err)
	}
	ok, err = d.ShouldTouch(ctx, "K1")
	if err != nil || ok {
		t.Errorf("second ShouldTouch (in window): ok=%v want false", ok)
	}

	// Advance miniredis past the TTL → next call wins.
	mr.FastForward(6 * time.Minute)
	ok, err = d.ShouldTouch(ctx, "K1")
	if err != nil || !ok {
		t.Errorf("post-TTL ShouldTouch: ok=%v err=%v want true", ok, err)
	}
}

// TestRedisTouchDebouncer_PerKeyIsolation — two distinct keys
// must NOT serialise against each other (different keys, separate
// debounce windows).
func TestRedisTouchDebouncer_PerKeyIsolation(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	d := NewRedisTouchDebouncer(rdb, 5*time.Minute)
	ctx := context.Background()

	a, _ := d.ShouldTouch(ctx, "K1")
	b, _ := d.ShouldTouch(ctx, "K2")
	if !a || !b {
		t.Errorf("first calls per-key = (K1=%v, K2=%v), want both true", a, b)
	}
	c, _ := d.ShouldTouch(ctx, "K1")
	if c {
		t.Errorf("second K1 in window = %v, want false", c)
	}
}

// TestRedisTouchDebouncer_EmptyKeyID — an empty keyID is a
// no-op return false (the middleware skips), keeping Redis out
// of the picture entirely.
func TestRedisTouchDebouncer_EmptyKeyID(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	d := NewRedisTouchDebouncer(rdb, 0)
	ok, err := d.ShouldTouch(context.Background(), "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if ok {
		t.Error("ShouldTouch(\"\") = true, want false")
	}
}

// TestRedisTouchDebouncer_DefaultTTL — passing 0 selects the
// DefaultTouchDebounceTTL (5 minutes).
func TestRedisTouchDebouncer_DefaultTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	d := NewRedisTouchDebouncer(rdb, 0)
	if d.ttl != DefaultTouchDebounceTTL {
		t.Errorf("ttl = %v, want %v (DefaultTouchDebounceTTL)", d.ttl, DefaultTouchDebounceTTL)
	}
}
