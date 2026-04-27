package auth

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
)

// fixedClock returns a deterministic now() for expiry tests.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// newTestValidator wires miniredis + a Redis client + a validator
// with a fixed clock. Returns the validator + the miniredis handle
// (so tests can SET records directly) + the clock anchor.
func newTestValidator(t *testing.T) (*RedisAPIKeyValidator, *miniredis.Miniredis, time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// Anchor time to a stable reference so expiry comparisons are
	// reproducible regardless of when the test runs.
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	v := NewRedisAPIKeyValidator(rdb, WithClock(fixedClock(now)))
	return v, mr, now
}

// seedKey serialises rec and writes it under the canonical
// cachekeys.APIKey(hash(key)) location. Mirrors what the admin
// seeding path does — the validator's contract is "if a record is
// present at this key shape, here's the lookup behaviour".
func seedKey(t *testing.T, mr *miniredis.Miniredis, key string, rec APIKeyRecord) {
	t.Helper()
	hash := HashAPIKey(key)
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("seed marshal: %v", err)
	}
	mr.Set(cachekeys.APIKey(hash), string(body))
}

// TestRedisAPIKey_LookupHappyPath is the everyday allow-path: a
// freshly-seeded record with no expiry and no revocation returns the
// owner Subject with the apikey tier and the seeded scopes.
func TestRedisAPIKey_LookupHappyPath(t *testing.T) {
	v, mr, _ := newTestValidator(t)
	seedKey(t, mr, "rek_test_abc123", APIKeyRecord{
		Identifier: "owner-42",
		Tier:       TierAPIKey,
		Scopes:     []string{"price:read", "history:read"},
	})

	got, err := v.Lookup(context.Background(), "rek_test_abc123")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Identifier != "owner-42" {
		t.Errorf("Identifier = %q, want owner-42", got.Identifier)
	}
	if got.Tier != TierAPIKey {
		t.Errorf("Tier = %q, want apikey", got.Tier)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "price:read" {
		t.Errorf("Scopes = %v, want [price:read, history:read]", got.Scopes)
	}
}

// TestRedisAPIKey_LookupNotFound covers the deny-path: an unknown
// key (no record in Redis) returns ErrUnauthorized. The 404 vs 401
// distinction matters — we must not leak "this key existed at some
// point" via a different sentinel for revoked vs absent.
func TestRedisAPIKey_LookupNotFound(t *testing.T) {
	v, _, _ := newTestValidator(t)
	_, err := v.Lookup(context.Background(), "rek_does_not_exist")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Lookup of absent key: got %v, want ErrUnauthorized", err)
	}
}

// TestRedisAPIKey_LookupRevoked confirms a revoked record returns
// ErrUnauthorized — same surface as not-found, deliberately. Don't
// distinguish "wrong key" from "revoked key" on the wire; that's
// information leak.
func TestRedisAPIKey_LookupRevoked(t *testing.T) {
	v, mr, now := newTestValidator(t)
	seedKey(t, mr, "rek_revoked", APIKeyRecord{
		Identifier: "owner-12",
		Tier:       TierAPIKey,
		RevokedAt:  now.Add(-time.Hour),
	})

	_, err := v.Lookup(context.Background(), "rek_revoked")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("revoked: got %v, want ErrUnauthorized", err)
	}
}

// TestRedisAPIKey_LookupExpired confirms an expired record returns
// ErrTokenExpired (NOT ErrUnauthorized) — the middleware uses this
// to set a more useful WWW-Authenticate header so the client knows
// to refresh rather than guess at credential validity.
func TestRedisAPIKey_LookupExpired(t *testing.T) {
	v, mr, now := newTestValidator(t)
	seedKey(t, mr, "rek_expired", APIKeyRecord{
		Identifier: "owner-77",
		Tier:       TierAPIKey,
		ExpiresAt:  now.Add(-time.Minute),
	})

	_, err := v.Lookup(context.Background(), "rek_expired")
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("expired: got %v, want ErrTokenExpired", err)
	}
}

// TestRedisAPIKey_LookupExactlyAtExpiry pins the boundary. A record
// whose expires_at equals now() is treated as expired — `now.Before(exp)`
// is false at equality. This matters for tests that seed a record
// with ExpiresAt == fixed-clock and would otherwise flake on
// machine clock skew.
func TestRedisAPIKey_LookupExactlyAtExpiry(t *testing.T) {
	v, mr, now := newTestValidator(t)
	seedKey(t, mr, "rek_at_boundary", APIKeyRecord{
		Identifier: "owner-boundary",
		Tier:       TierAPIKey,
		ExpiresAt:  now,
	})

	_, err := v.Lookup(context.Background(), "rek_at_boundary")
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("at-expiry: got %v, want ErrTokenExpired", err)
	}
}

// TestRedisAPIKey_FutureExpiryStillValid is the inverse boundary —
// a record whose expiry is one second after now() is still active.
func TestRedisAPIKey_FutureExpiryStillValid(t *testing.T) {
	v, mr, now := newTestValidator(t)
	seedKey(t, mr, "rek_future", APIKeyRecord{
		Identifier: "owner-future",
		Tier:       TierAPIKey,
		ExpiresAt:  now.Add(time.Second),
	})

	got, err := v.Lookup(context.Background(), "rek_future")
	if err != nil {
		t.Fatalf("future-expiry: %v", err)
	}
	if got.Identifier != "owner-future" {
		t.Errorf("Identifier = %q", got.Identifier)
	}
}

// TestRedisAPIKey_DefaultsToAPIKeyTier ensures a record with an empty
// tier field decodes as TierAPIKey. Operator keys MUST be opt-in via
// explicit tier="operator"; an unset field never escalates.
func TestRedisAPIKey_DefaultsToAPIKeyTier(t *testing.T) {
	v, mr, _ := newTestValidator(t)
	seedKey(t, mr, "rek_no_tier", APIKeyRecord{
		Identifier: "owner-default",
		// Tier deliberately empty
	})

	got, err := v.Lookup(context.Background(), "rek_no_tier")
	if err != nil {
		t.Fatalf("no-tier: %v", err)
	}
	if got.Tier != TierAPIKey {
		t.Errorf("default tier = %q, want apikey", got.Tier)
	}
}

// TestRedisAPIKey_OperatorTierPreserved ensures an explicit
// tier=operator survives the round-trip. The middleware uses this
// to gate /v1/admin/* endpoints.
func TestRedisAPIKey_OperatorTierPreserved(t *testing.T) {
	v, mr, _ := newTestValidator(t)
	seedKey(t, mr, "rek_admin", APIKeyRecord{
		Identifier: "ops-bot",
		Tier:       TierOperator,
	})

	got, err := v.Lookup(context.Background(), "rek_admin")
	if err != nil {
		t.Fatalf("operator: %v", err)
	}
	if got.Tier != TierOperator {
		t.Errorf("Tier = %q, want operator", got.Tier)
	}
}

// TestRedisAPIKey_EmptyKeyRejected confirms an empty input is
// rejected without a Redis round-trip. An admin tool that calls
// Lookup directly with "" must not land on a record (and certainly
// not on the empty-hash record if one were ever seeded).
func TestRedisAPIKey_EmptyKeyRejected(t *testing.T) {
	v, _, _ := newTestValidator(t)
	_, err := v.Lookup(context.Background(), "")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("empty key: got %v, want ErrUnauthorized", err)
	}
}

// TestRedisAPIKey_MalformedRecord exercises the operator-corruption
// branch. A record whose JSON doesn't decode is logged-but-401'd; we
// must not leak the corruption to the caller via a different
// sentinel.
func TestRedisAPIKey_MalformedRecord(t *testing.T) {
	v, mr, _ := newTestValidator(t)
	hash := HashAPIKey("rek_corrupt")
	mr.Set(cachekeys.APIKey(hash), "{not-json")

	_, err := v.Lookup(context.Background(), "rek_corrupt")
	if err == nil {
		t.Fatal("malformed record: want error, got nil")
	}
	// Wrapped non-sentinel — middleware's default branch fires (401).
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrTokenExpired) {
		t.Errorf("malformed should not match sentinel; got %v", err)
	}
}

// TestHashAPIKey_Stable pins the hash output so a future change to
// the hash function (HMAC, BLAKE2, etc.) is a deliberate decision
// that breaks this test. Every existing record in production Redis
// is keyed off this hash; rotating it is a wire break.
func TestHashAPIKey_Stable(t *testing.T) {
	const key = "rek_test_hash_pin"
	const want = "1690aa3074d262b0800b274269069eb492fc94043a7cef98b9c6a6c85a39f737"
	got := HashAPIKey(key)
	if got != want {
		t.Errorf("HashAPIKey(%q)\n  got  %s\n  want %s", key, got, want)
	}
}

// TestNewRedisAPIKeyValidator_PanicsOnNil confirms the constructor
// rejects a nil client. The auth middleware fails-loud upstream when
// the validator is the Noop stub; passing nil here would yield a
// runtime panic on the first request instead of a startup panic,
// which is much harder to debug.
func TestNewRedisAPIKeyValidator_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRedisAPIKeyValidator(nil) should panic")
		}
	}()
	_ = NewRedisAPIKeyValidator(nil)
}
