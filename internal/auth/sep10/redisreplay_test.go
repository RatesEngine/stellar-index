package sep10_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
	"github.com/StellarAtlas/stellar-atlas/internal/auth/sep10"
)

// TestRedisReplayGuard_FirstClaimSucceeds_SecondReturnsUnauthorized
// pins F-1224 (audit-2026-05-12): the same challenge tx hash can
// only be claimed once inside the TTL window. A second submission
// returns auth.ErrUnauthorized so the wider Verify path can
// classify it as a replay rather than re-issuing a fresh JWT.
func TestRedisReplayGuard_FirstClaimSucceeds_SecondReturnsUnauthorized(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	g := sep10.NewRedisReplayGuard(rdb)
	ctx := context.Background()
	const txHash = "abcdef1234567890"

	if err := g.MarkSeenIfFresh(ctx, txHash, 15*time.Minute); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	err := g.MarkSeenIfFresh(ctx, txHash, 15*time.Minute)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("second claim: want auth.ErrUnauthorized, got %v", err)
	}
}

// TestRedisReplayGuard_TTLExpiresAllowsResubmit confirms the dedupe
// slot is bounded by the TTL the caller passes — after the window
// elapses the same hash can be claimed again. Mirrors SEP-10
// challenge re-issue flow: a new challenge for the same client
// produces a new XDR + new hash, but a *replayed* expired XDR will
// also fail the time-bounds check upstream of MarkSeenIfFresh, so
// this test only proves the TTL contract, not the upstream guard.
func TestRedisReplayGuard_TTLExpiresAllowsResubmit(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	g := sep10.NewRedisReplayGuard(rdb)
	ctx := context.Background()
	const txHash = "deadbeefcafebabe"

	if err := g.MarkSeenIfFresh(ctx, txHash, 30*time.Second); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	mr.FastForward(31 * time.Second)

	if err := g.MarkSeenIfFresh(ctx, txHash, 30*time.Second); err != nil {
		t.Fatalf("re-claim after TTL: want nil, got %v", err)
	}
}

// TestRedisReplayGuard_DistinctHashesIndependentSlots confirms two
// distinct challenge hashes don't collide.
func TestRedisReplayGuard_DistinctHashesIndependentSlots(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	g := sep10.NewRedisReplayGuard(rdb)
	ctx := context.Background()

	if err := g.MarkSeenIfFresh(ctx, "hash-a", time.Minute); err != nil {
		t.Fatalf("claim hash-a: %v", err)
	}
	if err := g.MarkSeenIfFresh(ctx, "hash-b", time.Minute); err != nil {
		t.Fatalf("claim hash-b: %v", err)
	}
}
