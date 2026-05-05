package auth

import (
	"context"
	"errors"
	"testing"
)

// newTestStore lives in store_redis_test.go; reuse its
// (store, miniredis, anchor-time) shape and discard what we
// don't need.

func TestUpdateRateLimit_HappyPath(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	rec, _, err := store.Create(ctx, CreateAPIKeyRequest{
		Identifier:      "customer-acme",
		Label:           "production",
		Tier:            TierAPIKey,
		RateLimitPerMin: 1000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := store.UpdateRateLimit(ctx, rec.KeyID, 10000)
	if err != nil {
		t.Fatalf("UpdateRateLimit: %v", err)
	}
	if updated.KeyID != rec.KeyID {
		t.Errorf("KeyID = %q, want %q", updated.KeyID, rec.KeyID)
	}
	if updated.RateLimitPerMin != 10000 {
		t.Errorf("RateLimitPerMin = %d, want 10000", updated.RateLimitPerMin)
	}
	// Other fields preserved.
	if updated.Identifier != rec.Identifier {
		t.Errorf("Identifier mutated: %q != %q", updated.Identifier, rec.Identifier)
	}
	if updated.Label != rec.Label {
		t.Errorf("Label mutated: %q != %q", updated.Label, rec.Label)
	}
	if !updated.CreatedAt.Equal(rec.CreatedAt) {
		t.Errorf("CreatedAt mutated: %v != %v", updated.CreatedAt, rec.CreatedAt)
	}
}

func TestUpdateRateLimit_NotFound(t *testing.T) {
	store, _, _ := newTestStore(t)
	_, err := store.UpdateRateLimit(context.Background(), "kid_definitely_not_real", 5000)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("err = %v, want ErrKeyNotFound", err)
	}
}

func TestUpdateRateLimit_FindsCorrectKeyAmongMultiple(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	r1, _, _ := store.Create(ctx, CreateAPIKeyRequest{Identifier: "c1", Tier: TierAPIKey, RateLimitPerMin: 1000})
	r2, _, _ := store.Create(ctx, CreateAPIKeyRequest{Identifier: "c2", Tier: TierAPIKey, RateLimitPerMin: 1000})
	r3, _, _ := store.Create(ctx, CreateAPIKeyRequest{Identifier: "c3", Tier: TierAPIKey, RateLimitPerMin: 1000})

	// Lift r2 only.
	updated, err := store.UpdateRateLimit(ctx, r2.KeyID, 50000)
	if err != nil {
		t.Fatalf("UpdateRateLimit: %v", err)
	}
	if updated.KeyID != r2.KeyID {
		t.Errorf("returned KeyID = %q, want %q", updated.KeyID, r2.KeyID)
	}
	if updated.RateLimitPerMin != 50000 {
		t.Errorf("RateLimitPerMin = %d, want 50000", updated.RateLimitPerMin)
	}
	if updated.Identifier != "c2" {
		t.Errorf("Identifier = %q, want c2", updated.Identifier)
	}

	// Verify r1 and r3 are untouched by running a no-op
	// UpdateRateLimit (set to their current value) which returns
	// the record. If they'd been mutated by the r2 update, this
	// would surface.
	for _, kid := range []string{r1.KeyID, r3.KeyID} {
		got, err := store.UpdateRateLimit(ctx, kid, 1000)
		if err != nil {
			t.Errorf("re-read %s: %v", kid, err)
			continue
		}
		if got.RateLimitPerMin != 1000 {
			t.Errorf("%s RateLimitPerMin = %d, want 1000 (untouched)", kid, got.RateLimitPerMin)
		}
	}
}

func TestUpdateRateLimit_RejectsNegative(t *testing.T) {
	store, _, _ := newTestStore(t)
	ctx := context.Background()
	r1, _, _ := store.Create(ctx, CreateAPIKeyRequest{Identifier: "c1", Tier: TierAPIKey, RateLimitPerMin: 1000})
	_, err := store.UpdateRateLimit(ctx, r1.KeyID, -1)
	if err == nil {
		t.Errorf("expected error for negative rate-limit, got nil")
	}
}

func TestUpdateRateLimit_RejectsEmptyKeyID(t *testing.T) {
	store, _, _ := newTestStore(t)
	_, err := store.UpdateRateLimit(context.Background(), "", 5000)
	if err == nil {
		t.Errorf("expected error for empty keyID, got nil")
	}
}
