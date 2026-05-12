package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMarkEmailVerified_HappyPath — flips a freshly-minted
// key's EmailVerifiedAt to the supplied timestamp and the
// updated record round-trips through a subsequent SCAN. F-1218
// wave 45 (codex audit-2026-05-12).
func TestMarkEmailVerified_HappyPath(t *testing.T) {
	store, _, now := newTestStore(t)
	ctx := context.Background()
	rec, _, err := store.Create(ctx, CreateAPIKeyRequest{
		Identifier:      "signup-test123",
		Tier:            TierAPIKey,
		RateLimitPerMin: 1000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !rec.EmailVerifiedAt.IsZero() {
		t.Fatalf("seed EmailVerifiedAt = %v, want zero", rec.EmailVerifiedAt)
	}

	at := now.Add(time.Hour)
	updated, err := store.MarkEmailVerified(ctx, rec.KeyID, at)
	if err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	if !updated.EmailVerifiedAt.Equal(at) {
		t.Errorf("EmailVerifiedAt = %v, want %v", updated.EmailVerifiedAt, at)
	}

	// Idempotent re-mark with a different timestamp updates the
	// stamp without erroring (the verify handler relies on this
	// when the customer clicks the link twice in 24h).
	at2 := now.Add(2 * time.Hour)
	updated, err = store.MarkEmailVerified(ctx, rec.KeyID, at2)
	if err != nil {
		t.Fatalf("MarkEmailVerified second: %v", err)
	}
	if !updated.EmailVerifiedAt.Equal(at2) {
		t.Errorf("EmailVerifiedAt re-mark = %v, want %v", updated.EmailVerifiedAt, at2)
	}
}

// TestMarkEmailVerified_NotFound — a typo'd or absent KeyID
// returns ErrKeyNotFound (matches the UpdateRateLimit shape so
// downstream callers can errors.Is uniformly).
func TestMarkEmailVerified_NotFound(t *testing.T) {
	store, _, _ := newTestStore(t)
	_, err := store.MarkEmailVerified(context.Background(), "kid_definitely_not_real", time.Now())
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("err = %v, want ErrKeyNotFound", err)
	}
}

// TestMarkEmailVerified_ZeroAtUsesNow — passing a zero `at`
// causes the store to stamp `now()`, matching the verify-handler
// production wiring (which passes time.Time{} so the customer
// sees "verified at <now>").
func TestMarkEmailVerified_ZeroAtUsesNow(t *testing.T) {
	store, _, fixedNow := newTestStore(t)
	ctx := context.Background()
	rec, _, err := store.Create(ctx, CreateAPIKeyRequest{
		Identifier:      "signup-zeroAt",
		Tier:            TierAPIKey,
		RateLimitPerMin: 1000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := store.MarkEmailVerified(ctx, rec.KeyID, time.Time{})
	if err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	if !updated.EmailVerifiedAt.Equal(fixedNow.UTC()) {
		t.Errorf("EmailVerifiedAt = %v, want fixedNow %v", updated.EmailVerifiedAt, fixedNow.UTC())
	}
}

// TestMarkEmailVerified_RejectsEmptyKeyID — defence-in-depth.
func TestMarkEmailVerified_RejectsEmptyKeyID(t *testing.T) {
	store, _, _ := newTestStore(t)
	if _, err := store.MarkEmailVerified(context.Background(), "", time.Now()); err == nil {
		t.Error("expected error for empty keyID")
	}
}
