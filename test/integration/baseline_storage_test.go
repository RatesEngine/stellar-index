//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestBaselineStorageRoundTrip exercises UpsertBaseline → LatestBaseline
// against a real TimescaleDB with the volatility_baseline_1m migration
// applied. Confirms:
//
//   - LatestBaseline on empty table returns ErrBaselineNotFound (the
//     bootstrap signal the aggregator's confidence-score loop branches on)
//   - UpsertBaseline writes a row and a follow-up LatestBaseline reads
//     back the same Median + MAD + N
//   - A second UpsertBaseline on the same pair OVERWRITES (current-state
//     semantics — only the latest baseline persists)
//   - The MinSamples check rejects a baseline with N < 2 BEFORE touching
//     the DB
//   - The window-end > window-start CHECK rejects bad window args
func TestBaselineStorageRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	xlm, _ := canonical.ParseAsset("native")
	usd, _ := canonical.ParseAsset("fiat:USD")
	pair, _ := canonical.NewPair(xlm, usd)

	// ─── LatestBaseline on empty table → ErrBaselineNotFound ────────
	if _, err := store.LatestBaseline(ctx, pair); !errors.Is(err, timescale.ErrBaselineNotFound) {
		t.Fatalf("LatestBaseline on empty table: err = %v, want ErrBaselineNotFound", err)
	}

	// ─── Initial Upsert + read-back ─────────────────────────────────
	t0 := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	sb := timescale.StoredBaseline{
		Pair:        pair,
		ComputedAt:  t0,
		WindowStart: t0.Add(-30 * 24 * time.Hour),
		WindowEnd:   t0,
		Baseline: baseline.Baseline{
			Median: 0.0001,
			MAD:    0.0148,
			N:      120,
		},
	}
	if err := store.UpsertBaseline(ctx, sb); err != nil {
		t.Fatalf("UpsertBaseline: %v", err)
	}

	got, err := store.LatestBaseline(ctx, pair)
	if err != nil {
		t.Fatalf("LatestBaseline: %v", err)
	}
	if got.Baseline.Median != 0.0001 {
		t.Errorf("Median = %v, want 0.0001", got.Baseline.Median)
	}
	if got.Baseline.MAD != 0.0148 {
		t.Errorf("MAD = %v, want 0.0148", got.Baseline.MAD)
	}
	if got.Baseline.N != 120 {
		t.Errorf("N = %d, want 120", got.Baseline.N)
	}
	if !got.ComputedAt.Equal(t0.UTC()) {
		t.Errorf("ComputedAt = %v, want %v", got.ComputedAt, t0)
	}

	// ─── Re-upsert overwrites (current-state semantics) ─────────────
	t1 := t0.Add(1 * time.Hour)
	sb2 := sb
	sb2.ComputedAt = t1
	sb2.WindowEnd = t1
	sb2.WindowStart = t1.Add(-30 * 24 * time.Hour)
	sb2.Baseline.Median = 0.0002
	sb2.Baseline.MAD = 0.0149
	sb2.Baseline.N = 121
	if err := store.UpsertBaseline(ctx, sb2); err != nil {
		t.Fatalf("UpsertBaseline (overwrite): %v", err)
	}

	got, err = store.LatestBaseline(ctx, pair)
	if err != nil {
		t.Fatalf("LatestBaseline (after overwrite): %v", err)
	}
	if got.Baseline.Median != 0.0002 {
		t.Errorf("Median didn't advance; got %v, want 0.0002", got.Baseline.Median)
	}
	if got.Baseline.N != 121 {
		t.Errorf("N didn't advance; got %d, want 121", got.Baseline.N)
	}

	// ─── CountBaselines reports the row count ───────────────────────
	count, err := store.CountBaselines(ctx)
	if err != nil {
		t.Fatalf("CountBaselines: %v", err)
	}
	if count != 1 {
		t.Errorf("CountBaselines = %d, want 1 (one pair upserted twice)", count)
	}

	// ─── Validation: N < MinSamples is rejected pre-flight ──────────
	bad := sb
	bad.Baseline.N = 1
	if err := store.UpsertBaseline(ctx, bad); err == nil {
		t.Error("UpsertBaseline with N=1 should fail; got nil")
	}

	// ─── Validation: window_end ≤ window_start is rejected ──────────
	bad = sb
	bad.WindowStart = t1
	bad.WindowEnd = t1 // equal → must reject
	if err := store.UpsertBaseline(ctx, bad); err == nil {
		t.Error("UpsertBaseline with equal window_start/window_end should fail; got nil")
	}

	// ─── Distinct pair has its own row ──────────────────────────────
	other, _ := canonical.ParseAsset("fiat:EUR")
	pair2, _ := canonical.NewPair(xlm, other)
	sb3 := sb
	sb3.Pair = pair2
	if err := store.UpsertBaseline(ctx, sb3); err != nil {
		t.Fatalf("UpsertBaseline pair2: %v", err)
	}

	count, err = store.CountBaselines(ctx)
	if err != nil {
		t.Fatalf("CountBaselines: %v", err)
	}
	if count != 2 {
		t.Errorf("CountBaselines = %d, want 2 (two distinct pairs)", count)
	}

	// LatestBaseline on the second pair is independent of the first.
	got, err = store.LatestBaseline(ctx, pair2)
	if err != nil {
		t.Fatalf("LatestBaseline pair2: %v", err)
	}
	if !got.Pair.Equal(pair2) {
		t.Errorf("Pair = %v, want %v", got.Pair, pair2)
	}
}
