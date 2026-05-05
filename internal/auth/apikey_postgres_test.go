package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/platform"
)

func TestPostgresValidator_HappyPath_PostgresHit(t *testing.T) {
	keys, accounts, _ := newStubs()
	v, err := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
	})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	plaintext := "rek_postgres_test_001"
	acct := seedActiveAccount(accounts, "acme")
	seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 1500)

	sub, err := v.Lookup(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if sub.Identifier != "acct:acme" {
		t.Errorf("Identifier = %q", sub.Identifier)
	}
	if sub.Tier != auth.TierAPIKey {
		t.Errorf("Tier = %v", sub.Tier)
	}
	if sub.RateLimitPerMin != 1500 {
		t.Errorf("RateLimitPerMin = %d", sub.RateLimitPerMin)
	}
}

func TestPostgresValidator_RevokedKey_Unauthorized(t *testing.T) {
	keys, accounts, _ := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
	})

	plaintext := "rek_revoked"
	acct := seedActiveAccount(accounts, "x")
	rec := seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 1000)
	rec.RevokedAt = time.Now()
	keys.byID[rec.ID] = rec
	keys.byHash[hexHashOf(plaintext)] = rec

	_, err := v.Lookup(context.Background(), plaintext)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPostgresValidator_ExpiredKey_TokenExpired(t *testing.T) {
	keys, accounts, _ := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
		Now:      func() time.Time { return time.Now() },
	})
	plaintext := "rek_expired"
	acct := seedActiveAccount(accounts, "expired")
	rec := seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 100)
	rec.ExpiresAt = time.Now().Add(-1 * time.Minute)
	keys.byID[rec.ID] = rec
	keys.byHash[hexHashOf(plaintext)] = rec

	_, err := v.Lookup(context.Background(), plaintext)
	if !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got %v", err)
	}
}

func TestPostgresValidator_SuspendedAccount_Unauthorized(t *testing.T) {
	keys, accounts, _ := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
	})
	plaintext := "rek_suspended_acct"
	acct := seedActiveAccount(accounts, "s")
	acct.Status = platform.AccountSuspended
	accounts.byID[acct.ID] = acct
	seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 100)

	_, err := v.Lookup(context.Background(), plaintext)
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for suspended account, got %v", err)
	}
}

func TestPostgresValidator_AbsentKey_Unauthorized(t *testing.T) {
	keys, accounts, _ := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
	})
	_, err := v.Lookup(context.Background(), "rek_nonexistent")
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPostgresValidator_CacheReadThrough(t *testing.T) {
	keys, accounts, rdb := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keys,
		Accounts: accounts,
		Cache:    rdb,
		CacheTTL: 5 * time.Minute,
	})

	plaintext := "rek_cache_test"
	acct := seedActiveAccount(accounts, "cached")
	seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 2000)

	// First lookup: Postgres miss → write-back to cache.
	if _, err := v.Lookup(context.Background(), plaintext); err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	pgCount := keys.byHashCallCount
	if pgCount == 0 {
		t.Fatal("expected Postgres lookup on cold cache")
	}

	// Second lookup: cache hit → no additional Postgres call.
	if _, err := v.Lookup(context.Background(), plaintext); err != nil {
		t.Fatalf("second lookup: %v", err)
	}
	if keys.byHashCallCount != pgCount {
		t.Errorf("Postgres lookup count grew on cache hit: was %d now %d", pgCount, keys.byHashCallCount)
	}
}

func TestPostgresValidator_Invalidate(t *testing.T) {
	keys, accounts, rdb := newStubs()
	v, _ := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys: keys, Accounts: accounts, Cache: rdb,
	})
	plaintext := "rek_invalidate"
	acct := seedActiveAccount(accounts, "invalid")
	seedKey(keys, plaintext, acct.ID, platform.APIKeyTierAPIKey, 1000)

	// Populate the cache.
	if _, err := v.Lookup(context.Background(), plaintext); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if err := v.InvalidateCachedKey(context.Background(), hexHashOf(plaintext)); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if _, err := rdb.Get(context.Background(), cachekeys.APIKey(hexHashOf(plaintext))).Result(); !errors.Is(err, redis.Nil) {
		t.Errorf("cache entry not removed after invalidate: %v", err)
	}
}

func TestPostgresValidator_RequiresStores(t *testing.T) {
	if _, err := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{}); err == nil {
		t.Error("expected error when Keys nil")
	}
	stub, _, _ := newStubs()
	if _, err := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{Keys: stub}); err == nil {
		t.Error("expected error when Accounts nil")
	}
}

// ─── stubs ───────────────────────────────────────────────────────

func hexHashOf(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func newStubs() (*stubKeyStore, *stubAccountStore, redis.Cmdable) {
	mr, _ := miniredis.Run()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return newStubKeyStore(), newStubAccountStore(), rdb
}

func seedActiveAccount(s *stubAccountStore, slug string) platform.Account {
	a := platform.Account{
		ID:     uuid.New(),
		Name:   slug,
		Slug:   slug,
		Tier:   platform.TierFree,
		Status: platform.AccountActive,
	}
	s.byID[a.ID] = a
	return a
}

func seedKey(s *stubKeyStore, plaintext string, accountID uuid.UUID, tier platform.APIKeyTier, rateLimit int) platform.APIKey {
	sum := sha256.Sum256([]byte(plaintext))
	prefix := plaintext
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	rec := platform.APIKey{
		ID:              "kid_" + uuid.New().String()[:12],
		AccountID:       accountID,
		Name:            "test",
		KeyHash:         sum[:],
		KeyPrefix:       prefix,
		Tier:            tier,
		RateLimitPerMin: rateLimit,
		CreatedAt:       time.Now().UTC(),
	}
	s.byID[rec.ID] = rec
	s.byHash[hex.EncodeToString(sum[:])] = rec
	return rec
}

type stubKeyStore struct {
	mu              sync.Mutex
	byID            map[string]platform.APIKey
	byHash          map[string]platform.APIKey
	byHashCallCount int
}

func newStubKeyStore() *stubKeyStore {
	return &stubKeyStore{
		byID:   map[string]platform.APIKey{},
		byHash: map[string]platform.APIKey{},
	}
}

func (s *stubKeyStore) Create(_ context.Context, k platform.APIKey) (platform.APIKey, error) {
	return k, nil
}

func (s *stubKeyStore) Get(_ context.Context, id string) (platform.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.byID[id]
	if !ok {
		return platform.APIKey{}, platform.ErrNotFound
	}
	return k, nil
}

func (s *stubKeyStore) GetByHash(_ context.Context, hash []byte) (platform.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHashCallCount++
	k, ok := s.byHash[hex.EncodeToString(hash)]
	if !ok {
		return platform.APIKey{}, platform.ErrNotFound
	}
	return k, nil
}

func (s *stubKeyStore) ListForAccount(_ context.Context, _ uuid.UUID) ([]platform.APIKey, error) {
	return nil, nil
}
func (s *stubKeyStore) Update(_ context.Context, _ platform.APIKey) error { return nil }
func (s *stubKeyStore) Revoke(_ context.Context, _ string, _ uuid.UUID, _ string) error {
	return nil
}

func (s *stubKeyStore) TouchUsage(_ context.Context, _ string, _ net.IP, _ string) error {
	return nil
}

type stubAccountStore struct {
	mu   sync.Mutex
	byID map[uuid.UUID]platform.Account
}

func newStubAccountStore() *stubAccountStore {
	return &stubAccountStore{byID: map[uuid.UUID]platform.Account{}}
}

func (s *stubAccountStore) Create(_ context.Context, a platform.Account) (platform.Account, error) {
	return a, nil
}

func (s *stubAccountStore) Get(_ context.Context, id uuid.UUID) (platform.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[id]
	if !ok {
		return platform.Account{}, platform.ErrNotFound
	}
	return a, nil
}

func (s *stubAccountStore) GetBySlug(_ context.Context, _ string) (platform.Account, error) {
	return platform.Account{}, platform.ErrNotFound
}

func (s *stubAccountStore) GetByStripeCustomerID(_ context.Context, _ string) (platform.Account, error) {
	return platform.Account{}, platform.ErrNotFound
}
func (s *stubAccountStore) Update(_ context.Context, _ platform.Account) error { return nil }
func (s *stubAccountStore) Suspend(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (s *stubAccountStore) Unsuspend(_ context.Context, _ uuid.UUID) error { return nil }

var (
	_ platform.APIKeyStore  = (*stubKeyStore)(nil)
	_ platform.AccountStore = (*stubAccountStore)(nil)
)
