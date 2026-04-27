package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
)

// newTestStore wires miniredis + a store with a fixed clock and a
// deterministic entropy source. The entropy source emits an
// incrementing byte pattern so generated KeyIDs / plaintexts are
// reproducible.
func newTestStore(t *testing.T) (*RedisAPIKeyStore, *miniredis.Miniredis, time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Date(2026, 4, 27, 18, 0, 0, 0, time.UTC)
	var counter byte
	deterministic := func(b []byte) (int, error) {
		for i := range b {
			b[i] = counter
			counter++
		}
		return len(b), nil
	}
	s := NewRedisAPIKeyStore(rdb,
		WithStoreClock(fixedClock(now)),
		withRandRead(deterministic))
	return s, mr, now
}

// TestRedisAPIKeyStore_CreateRoundTrip is the everyday path. Create
// returns a populated record + plaintext; the validator round-trips
// the same hash and finds the record.
func TestRedisAPIKeyStore_CreateRoundTrip(t *testing.T) {
	store, mr, now := newTestStore(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	validator := NewRedisAPIKeyValidator(rdb, WithClock(fixedClock(now)))

	rec, plaintext, err := store.Create(context.Background(), CreateAPIKeyRequest{
		Identifier:      "owner-1",
		Label:           "ci-bot",
		Tier:            TierAPIKey,
		RateLimitPerMin: 240,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(plaintext, "rek_") {
		t.Errorf("plaintext should start with rek_, got %q", plaintext)
	}
	if !strings.HasPrefix(rec.KeyID, "kid_") {
		t.Errorf("KeyID should start with kid_, got %q", rec.KeyID)
	}
	if rec.CreatedAt.IsZero() {
		t.Error("CreatedAt should be stamped")
	}
	if rec.Identifier != "owner-1" || rec.Label != "ci-bot" {
		t.Errorf("Identifier/Label not preserved: %+v", rec)
	}
	if rec.RateLimitPerMin != 240 {
		t.Errorf("RateLimitPerMin = %d, want 240", rec.RateLimitPerMin)
	}

	// Round-trip via the validator: the hash of the returned plaintext
	// must hit the record we just wrote.
	subject, err := validator.Lookup(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("Lookup of freshly-issued key: %v", err)
	}
	if subject.Identifier != "owner-1" {
		t.Errorf("Lookup Identifier = %q, want owner-1", subject.Identifier)
	}
	if subject.KeyID != rec.KeyID {
		t.Errorf("Lookup KeyID = %q, want %q", subject.KeyID, rec.KeyID)
	}
	if subject.RateLimitPerMin != 240 {
		t.Errorf("Lookup RateLimitPerMin = %d, want 240", subject.RateLimitPerMin)
	}
	if !subject.CreatedAt.Equal(rec.CreatedAt) {
		t.Errorf("Lookup CreatedAt = %v, want %v", subject.CreatedAt, rec.CreatedAt)
	}
}

// TestRedisAPIKeyStore_CreateDefaultsToAPIKeyTier confirms a request
// with no explicit tier produces a TierAPIKey record. Operator tier
// is opt-in only; an unset field never escalates.
func TestRedisAPIKeyStore_CreateDefaultsToAPIKeyTier(t *testing.T) {
	store, _, _ := newTestStore(t)
	rec, _, err := store.Create(context.Background(), CreateAPIKeyRequest{
		Identifier: "owner-default",
		// Tier deliberately empty
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.Tier != TierAPIKey {
		t.Errorf("default tier = %q, want apikey", rec.Tier)
	}
}

// TestRedisAPIKeyStore_CreateRequiresIdentifier — the store rejects
// an empty Identifier rather than letting an unowned key into Redis.
func TestRedisAPIKeyStore_CreateRequiresIdentifier(t *testing.T) {
	store, _, _ := newTestStore(t)
	_, _, err := store.Create(context.Background(), CreateAPIKeyRequest{})
	if err == nil {
		t.Fatal("Create with empty Identifier: want error, got nil")
	}
}

// TestRedisAPIKeyStore_CreateWritesUnderHashedKey verifies the
// stored Redis key matches the wire grammar from cachekeys. Keeps
// the writer + reader in lock-step.
func TestRedisAPIKeyStore_CreateWritesUnderHashedKey(t *testing.T) {
	store, mr, _ := newTestStore(t)
	_, plaintext, err := store.Create(context.Background(), CreateAPIKeyRequest{
		Identifier: "owner-3",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	hash := HashAPIKey(plaintext)
	body, err := mr.Get(cachekeys.APIKey(hash))
	if err != nil {
		t.Fatalf("expected record at %s: %v", cachekeys.APIKey(hash), err)
	}
	var got APIKeyRecord
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Identifier != "owner-3" {
		t.Errorf("Identifier = %q", got.Identifier)
	}
}

// TestRedisAPIKeyStore_CreatePropagatesExpiry confirms ExpiresAt
// from the request lands on the record. The validator then uses
// this to return ErrTokenExpired on lookup past the date.
func TestRedisAPIKeyStore_CreatePropagatesExpiry(t *testing.T) {
	store, _, now := newTestStore(t)
	expiry := now.Add(30 * 24 * time.Hour)
	rec, _, err := store.Create(context.Background(), CreateAPIKeyRequest{
		Identifier: "owner-expiring",
		ExpiresAt:  expiry,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !rec.ExpiresAt.Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", rec.ExpiresAt, expiry)
	}
}

// TestRedisAPIKeyStore_CreateRedisFailureDoesNotLeakPlaintext —
// when the SET fails, the plaintext returned is empty. This is
// load-bearing: a caller that ignores the error must not be able
// to surface a key that was never stored.
func TestRedisAPIKeyStore_CreateRedisFailureDoesNotLeakPlaintext(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	now := time.Date(2026, 4, 27, 18, 0, 0, 0, time.UTC)
	var counter byte
	deterministic := func(b []byte) (int, error) {
		for i := range b {
			b[i] = counter
			counter++
		}
		return len(b), nil
	}
	store := NewRedisAPIKeyStore(rdb,
		WithStoreClock(fixedClock(now)),
		withRandRead(deterministic))

	mr.Close() // simulate Redis outage

	rec, plaintext, err := store.Create(context.Background(), CreateAPIKeyRequest{
		Identifier: "owner-doomed",
	})
	if err == nil {
		t.Fatal("expected error when Redis is down")
	}
	if plaintext != "" {
		t.Errorf("plaintext should be empty on failure, got %q", plaintext)
	}
	if rec.KeyID != "" {
		t.Errorf("record should be empty on failure, got %+v", rec)
	}
}

// TestNewRedisAPIKeyStore_PanicsOnNil mirrors the validator's
// constructor — a nil client is operator misconfig that should
// surface at startup, not at first request.
func TestNewRedisAPIKeyStore_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewRedisAPIKeyStore(nil) should panic")
		}
	}()
	_ = NewRedisAPIKeyStore(nil)
}

// TestGenerateID_Deterministic — entropy is plumbed correctly. The
// deterministic source emits an incrementing byte pattern; the
// resulting ID must be the prefix + hex of those bytes.
func TestGenerateID_Deterministic(t *testing.T) {
	var counter byte = 10
	src := func(b []byte) (int, error) {
		for i := range b {
			b[i] = counter
			counter++
		}
		return len(b), nil
	}
	id, err := generateID(src, "kid_", 4)
	if err != nil {
		t.Fatalf("generateID: %v", err)
	}
	if id != "kid_0a0b0c0d" {
		t.Errorf("generateID = %q, want kid_0a0b0c0d", id)
	}
}

// TestGenerateID_PropagatesError — a failing entropy source bubbles
// up; an empty ID is never returned silently.
func TestGenerateID_PropagatesError(t *testing.T) {
	want := errors.New("entropy down")
	bad := func(_ []byte) (int, error) { return 0, want }
	_, err := generateID(bad, "kid_", 4)
	if !errors.Is(err, want) {
		t.Errorf("generateID error = %v, want %v", err, want)
	}
}
