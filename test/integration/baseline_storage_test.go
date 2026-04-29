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
// applied (including 0008's multi-window columns). Confirms:
//
//   - Empty-table read returns ErrBaselineNotFound
//   - Upsert + LatestBaseline round-trips a full multi-window struct
//   - Partial baselines (Day1/Day7 nil) round-trip with the
//     nullable columns
//   - Re-upsert overwrites (current-state semantics)
//   - Pre-flight checks (Day30 nil, N < MinSamples, window-validity)
//     reject before touching the DB
//   - Distinct pairs are isolated
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

	// ─── Empty table → ErrBaselineNotFound ──────────────────────────
	if _, err := store.LatestBaseline(ctx, pair); !errors.Is(err, timescale.ErrBaselineNotFound) {
		t.Fatalf("LatestBaseline on empty table: err = %v, want ErrBaselineNotFound", err)
	}

	// ─── Full three-window upsert + read-back ───────────────────────
	t0 := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	d1 := &baseline.Baseline{Median: 0.0001, MAD: 0.0010, N: 60}
	d7 := &baseline.Baseline{Median: 0.0002, MAD: 0.0050, N: 50}
	d30 := &baseline.Baseline{Median: 0.0003, MAD: 0.0148, N: 120}
	sb := timescale.StoredBaseline{
		Pair:        pair,
		ComputedAt:  t0,
		WindowStart: t0.Add(-30 * 24 * time.Hour),
		WindowEnd:   t0,
		Multi:       baseline.MultiBaseline{Day1: d1, Day7: d7, Day30: d30},
	}
	if err := store.UpsertBaseline(ctx, sb); err != nil {
		t.Fatalf("UpsertBaseline: %v", err)
	}

	got, err := store.LatestBaseline(ctx, pair)
	if err != nil {
		t.Fatalf("LatestBaseline: %v", err)
	}
	if got.Multi.Day30 == nil {
		t.Fatal("Day30 nil after round-trip")
	}
	if got.Multi.Day30.Median != 0.0003 || got.Multi.Day30.MAD != 0.0148 || got.Multi.Day30.N != 120 {
		t.Errorf("Day30 = %+v, want {0.0003, 0.0148, 120}", got.Multi.Day30)
	}
	if got.Multi.Day7 == nil || got.Multi.Day7.N != 50 {
		t.Errorf("Day7 round-trip wrong: %+v", got.Multi.Day7)
	}
	if got.Multi.Day1 == nil || got.Multi.Day1.N != 60 {
		t.Errorf("Day1 round-trip wrong: %+v", got.Multi.Day1)
	}

	// ─── Partial baseline (Day1/Day7 nil — bootstrap) ──────────────
	other, _ := canonical.ParseAsset("fiat:EUR")
	pair2, _ := canonical.NewPair(xlm, other)
	sbPartial := timescale.StoredBaseline{
		Pair:        pair2,
		ComputedAt:  t0,
		WindowStart: t0.Add(-30 * 24 * time.Hour),
		WindowEnd:   t0,
		Multi:       baseline.MultiBaseline{Day30: d30}, // Day1 + Day7 nil
	}
	if err := store.UpsertBaseline(ctx, sbPartial); err != nil {
		t.Fatalf("UpsertBaseline (partial): %v", err)
	}
	gotPartial, err := store.LatestBaseline(ctx, pair2)
	if err != nil {
		t.Fatalf("LatestBaseline (partial): %v", err)
	}
	if gotPartial.Multi.Day30 == nil {
		t.Error("Day30 nil; expected populated")
	}
	if gotPartial.Multi.Day1 != nil {
		t.Errorf("Day1 should round-trip as nil; got %+v", gotPartial.Multi.Day1)
	}
	if gotPartial.Multi.Day7 != nil {
		t.Errorf("Day7 should round-trip as nil; got %+v", gotPartial.Multi.Day7)
	}

	// ─── Re-upsert overwrites ──────────────────────────────────────
	t1 := t0.Add(1 * time.Hour)
	sb2 := sb
	sb2.ComputedAt = t1
	sb2.WindowEnd = t1
	sb2.WindowStart = t1.Add(-30 * 24 * time.Hour)
	sb2.Multi.Day30 = &baseline.Baseline{Median: 0.0009, MAD: 0.02, N: 121}
	sb2.Multi.Day1 = nil // overwrite to bootstrap on this scale
	if err := store.UpsertBaseline(ctx, sb2); err != nil {
		t.Fatalf("UpsertBaseline (overwrite): %v", err)
	}
	got, err = store.LatestBaseline(ctx, pair)
	if err != nil {
		t.Fatalf("LatestBaseline (after overwrite): %v", err)
	}
	if got.Multi.Day30.Median != 0.0009 {
		t.Errorf("Day30.Median didn't advance; got %v, want 0.0009", got.Multi.Day30.Median)
	}
	if got.Multi.Day1 != nil {
		t.Errorf("Day1 should round-trip as nil after overwrite; got %+v", got.Multi.Day1)
	}

	// ─── Validation: Day30 nil rejected pre-flight ─────────────────
	bad := sb
	bad.Multi.Day30 = nil
	if err := store.UpsertBaseline(ctx, bad); err == nil {
		t.Error("UpsertBaseline with Day30 nil should fail; got nil")
	}

	// ─── Validation: Day30.N < MinSamples rejected ─────────────────
	bad = sb
	bad.Multi.Day30 = &baseline.Baseline{Median: 0, MAD: 0, N: 1}
	if err := store.UpsertBaseline(ctx, bad); err == nil {
		t.Error("UpsertBaseline with N=1 should fail; got nil")
	}

	// ─── Validation: window_end ≤ window_start rejected ────────────
	bad = sb
	bad.WindowStart = t1
	bad.WindowEnd = t1
	if err := store.UpsertBaseline(ctx, bad); err == nil {
		t.Error("UpsertBaseline with equal window_start/window_end should fail; got nil")
	}

	// ─── CountBaselines ────────────────────────────────────────────
	count, err := store.CountBaselines(ctx)
	if err != nil {
		t.Fatalf("CountBaselines: %v", err)
	}
	if count != 2 {
		t.Errorf("CountBaselines = %d, want 2 (pair + pair2)", count)
	}
}
