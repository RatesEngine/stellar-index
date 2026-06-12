package ratelimit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/ratelimit"
)

// Take's eval-error path is the seam between the Redis client and
// the rate-limit layer. Per the package doc: callers should fail
// open on this kind of error — but Take itself MUST surface the
// error rather than quietly returning Allowed=false (which would
// manifest as a fleet-wide rate-limit jam during a Redis outage).
func TestBucket_TakeReturnsErrorOnRedisOutage(t *testing.T) {
	rdb, _ := newRedis(t)
	if err := rdb.Close(); err != nil {
		t.Fatalf("close redis: %v", err)
	}
	b := ratelimit.New(rdb, 3, time.Minute)

	_, err := b.Take(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error on closed redis client, got nil")
	}
	// Wrapping must be intact: "ratelimit: eval: …" prefix from the
	// %w fmt.Errorf; underlying ErrClosed reachable via errors.Is.
	if !errors.Is(err, redis.ErrClosed) {
		t.Errorf("error chain lost ErrClosed: %v", err)
	}
}
