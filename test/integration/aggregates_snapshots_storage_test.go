//go:build integration

// Integration tests for the rewritten_vwap_snapshots storage methods
// shipped alongside ADR-0025 phase 1 (Option B). The migration adds
// an empty hypertable; these tests verify round-trip + ordering
// guarantees.
//
// Choose between this and the bucketed (Option A) test suite — only
// one of (this file + its migration) and (the bucketed file + its
// migration) should land on main.

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestInsertRewrittenVWAPSnapshot_RoundTrips — write one snapshot
// per (pair, window, observed_at) and read each back. Verifies the
// insert path + per-window indexing.
func TestInsertRewrittenVWAPSnapshot_RoundTrips(t *testing.T) {
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

	now := time.Now().UTC().Truncate(time.Second)
	for _, window := range []time.Duration{5 * time.Minute, 1 * time.Hour, 24 * time.Hour} {
		row := timescale.VwapSnapshotRow{
			ObservedAt:    now,
			BaseAsset:     "native",
			QuoteAsset:    "fiat:USD",
			WindowSeconds: int(window.Seconds()),
			VWAP:          "0.157352774926",
			Volume:        "12345.6789",
			VolumeUSD:     "2954.526",
			TradeCount:    42,
			Sources:       []string{"soroswap", "sdex"},
		}
		if err := store.InsertRewrittenVWAPSnapshot(ctx, row); err != nil {
			t.Fatalf("InsertRewrittenVWAPSnapshot window=%s: %v", window, err)
		}

		got, err := store.LatestRewrittenVWAPSnapshot(ctx, pair, window)
		if err != nil {
			t.Fatalf("LatestRewrittenVWAPSnapshot window=%s: %v", window, err)
		}
		if got.WindowSeconds != int(window.Seconds()) {
			t.Errorf("window=%s: got WindowSeconds=%d, want %d",
				window, got.WindowSeconds, int(window.Seconds()))
		}
		if got.VWAP != "0.157352774926" {
			t.Errorf("window=%s: VWAP=%q, want %q", window, got.VWAP, "0.157352774926")
		}
		if got.TradeCount != 42 {
			t.Errorf("window=%s: TradeCount=%d, want 42", window, got.TradeCount)
		}
		if len(got.Sources) != 2 {
			t.Errorf("window=%s: Sources=%v, want [soroswap sdex]", window, got.Sources)
		}
	}
}

// TestInsertRewrittenVWAPSnapshot_DuplicateObservedAtIsNoop — when
// the orchestrator restart-replays a tick at the same observed_at,
// the second insert is a silent no-op (NOT an error). The
// ON CONFLICT clause guards this; the test pins the contract.
func TestInsertRewrittenVWAPSnapshot_DuplicateObservedAtIsNoop(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)

	row := timescale.VwapSnapshotRow{
		ObservedAt:    now,
		BaseAsset:     "native",
		QuoteAsset:    "fiat:USD",
		WindowSeconds: 300,
		VWAP:          "0.157",
		Volume:        "1",
		TradeCount:    1,
		Sources:       []string{"soroswap"},
	}
	if err := store.InsertRewrittenVWAPSnapshot(ctx, row); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Second insert at same (pair, window, observed_at) — must NOT
	// error; the existing row stays.
	row.VWAP = "0.158"
	if err := store.InsertRewrittenVWAPSnapshot(ctx, row); err != nil {
		t.Fatalf("duplicate insert (should be no-op): %v", err)
	}
	got, err := store.LatestRewrittenVWAPSnapshot(ctx, pair, 5*time.Minute)
	if err != nil {
		t.Fatalf("LatestRewrittenVWAPSnapshot: %v", err)
	}
	// First write wins (DO NOTHING semantics) — we don't overwrite,
	// because the orchestrator would have computed the same VWAP
	// from the same trade snapshot anyway.
	if got.VWAP != "0.157" {
		t.Errorf("VWAP=%q, want 0.157 (first-write wins under ON CONFLICT DO NOTHING)", got.VWAP)
	}
}

// TestLatestRewrittenVWAPSnapshot_NotFound — empty pair returns
// sql.ErrNoRows. The price-reader fallback chain treats this as
// "no snapshot data, try Redis next."
func TestLatestRewrittenVWAPSnapshot_NotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	xlm, _ := canonical.ParseAsset("crypto:XLM")
	eur, _ := canonical.ParseAsset("fiat:EUR")
	pair, _ := canonical.NewPair(xlm, eur)
	if _, err := store.LatestRewrittenVWAPSnapshot(ctx, pair, 5*time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err=%v, want sql.ErrNoRows", err)
	}
}

// TestLatestRewrittenVWAPSnapshot_PerWindowIsolated — snapshots for
// different windows on the same pair are isolated. Asking for the
// 5m window doesn't return a 1h row.
func TestLatestRewrittenVWAPSnapshot_PerWindowIsolated(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)

	// Insert 1h window only; ask for 5m window — should miss.
	if err := store.InsertRewrittenVWAPSnapshot(ctx, timescale.VwapSnapshotRow{
		ObservedAt: now, BaseAsset: "native", QuoteAsset: "fiat:USD",
		WindowSeconds: 3600, VWAP: "0.157", Volume: "1", TradeCount: 1,
		Sources: []string{"soroswap"},
	}); err != nil {
		t.Fatalf("insert 1h: %v", err)
	}
	if _, err := store.LatestRewrittenVWAPSnapshot(ctx, pair, 5*time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("LatestRewrittenVWAPSnapshot(5m) err=%v, want sql.ErrNoRows when only 1h is populated", err)
	}
	got, err := store.LatestRewrittenVWAPSnapshot(ctx, pair, 1*time.Hour)
	if err != nil {
		t.Fatalf("LatestRewrittenVWAPSnapshot(1h): %v", err)
	}
	if got.WindowSeconds != 3600 {
		t.Errorf("WindowSeconds=%d, want 3600", got.WindowSeconds)
	}
}

// TestRewrittenVWAPSnapshotsInRange_OldestFirst — confirms the SQL
// ORDER BY observed_at ASC. Insert out-of-order, read in-order.
func TestRewrittenVWAPSnapshotsInRange_OldestFirst(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)

	// Insert three rows out of order.
	for _, off := range []int{-30, -10, -20} {
		obs := now.Add(time.Duration(off) * time.Minute)
		if err := store.InsertRewrittenVWAPSnapshot(ctx, timescale.VwapSnapshotRow{
			ObservedAt: obs, BaseAsset: "native", QuoteAsset: "fiat:USD",
			WindowSeconds: 300, VWAP: "0.157", Volume: "1", TradeCount: 1,
			Sources: []string{"soroswap"},
		}); err != nil {
			t.Fatalf("insert off=%d: %v", off, err)
		}
	}

	got, err := store.RewrittenVWAPSnapshotsInRange(ctx, pair, 5*time.Minute, now.Add(-1*time.Hour), now, 100)
	if err != nil {
		t.Fatalf("RewrittenVWAPSnapshotsInRange: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got)=%d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].ObservedAt.After(got[i-1].ObservedAt) {
			t.Errorf("series not oldest-first at index %d: %v then %v",
				i, got[i-1].ObservedAt, got[i].ObservedAt)
		}
	}
}
