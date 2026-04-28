//go:build integration

package integration_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/supply"
)

// TestSupplyStorageRoundTrip exercises the InsertSupply →
// LatestSupply → SupplyHistory paths through real TimescaleDB with
// the asset_supply_history hypertable migration applied. Together
// with the unit tests for assembleSupply this is the end-to-end
// proof of the storage layer for ADR-0011 supply data.
func TestSupplyStorageRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// ─── LatestSupply on empty table → ErrNotFound ──────────────
	if _, err := store.LatestSupply(ctx, "XLM"); !errors.Is(err, timescale.ErrNotFound) {
		t.Fatalf("LatestSupply on empty table: err = %v, want ErrNotFound", err)
	}

	// ─── Insert XLM snapshot at ledger 50_000_000 ───────────────
	xlmTotal, _ := new(big.Int).SetString("500018068120000000", 10) // 50.0018... B XLM in stroops
	xlmCirc, _ := new(big.Int).SetString("499000000000000000", 10)
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	snap := supply.Supply{
		AssetKey:          "XLM",
		TotalSupply:       xlmTotal,
		CirculatingSupply: xlmCirc,
		MaxSupply:         xlmTotal, // XLM is hard-capped at total
		Basis:             supply.BasisXLMSDFReserveExclusion,
		LedgerSequence:    50_000_000,
		ObservedAt:        t0,
	}
	if err := store.InsertSupply(ctx, snap); err != nil {
		t.Fatalf("InsertSupply: %v", err)
	}

	// Idempotent re-insert at the same ledger is a no-op.
	if err := store.InsertSupply(ctx, snap); err != nil {
		t.Fatalf("InsertSupply (duplicate): %v", err)
	}

	// ─── LatestSupply round-trips ───────────────────────────────
	got, err := store.LatestSupply(ctx, "XLM")
	if err != nil {
		t.Fatalf("LatestSupply: %v", err)
	}
	if got.TotalSupply.Cmp(xlmTotal) != 0 {
		t.Errorf("TotalSupply = %s, want %s", got.TotalSupply, xlmTotal)
	}
	if got.CirculatingSupply.Cmp(xlmCirc) != 0 {
		t.Errorf("CirculatingSupply = %s, want %s", got.CirculatingSupply, xlmCirc)
	}
	if got.MaxSupply == nil || got.MaxSupply.Cmp(xlmTotal) != 0 {
		t.Errorf("MaxSupply = %v, want %s", got.MaxSupply, xlmTotal)
	}
	if got.Basis != supply.BasisXLMSDFReserveExclusion {
		t.Errorf("Basis = %q", got.Basis)
	}
	if got.LedgerSequence != 50_000_000 {
		t.Errorf("LedgerSequence = %d", got.LedgerSequence)
	}

	// ─── Insert a later snapshot — Latest must advance ───────────
	t1 := t0.Add(1 * time.Hour)
	xlmCirc2, _ := new(big.Int).SetString("499010000000000000", 10) // 1M XLM less reserved
	snap2 := snap
	snap2.CirculatingSupply = xlmCirc2
	snap2.LedgerSequence = 50_001_000
	snap2.ObservedAt = t1
	if err := store.InsertSupply(ctx, snap2); err != nil {
		t.Fatalf("InsertSupply (advance): %v", err)
	}

	got, err = store.LatestSupply(ctx, "XLM")
	if err != nil {
		t.Fatalf("LatestSupply (after advance): %v", err)
	}
	if got.LedgerSequence != 50_001_000 {
		t.Errorf("Latest didn't advance; LedgerSequence = %d, want 50_001_000", got.LedgerSequence)
	}
	if got.CirculatingSupply.Cmp(xlmCirc2) != 0 {
		t.Errorf("Latest circulating = %s, want %s", got.CirculatingSupply, xlmCirc2)
	}

	// ─── Classic asset with NULL max_supply ─────────────────────
	usdcKey := "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	usdcSnap := supply.Supply{
		AssetKey:          usdcKey,
		TotalSupply:       big.NewInt(1_000_000_000),
		CirculatingSupply: big.NewInt(990_000_000),
		MaxSupply:         nil, // uncapped issuer + no override
		Basis:             supply.BasisIssuerExclusion,
		LedgerSequence:    50_000_000,
		ObservedAt:        t0,
	}
	if err := store.InsertSupply(ctx, usdcSnap); err != nil {
		t.Fatalf("InsertSupply USDC: %v", err)
	}
	gotUSDC, err := store.LatestSupply(ctx, usdcKey)
	if err != nil {
		t.Fatalf("LatestSupply USDC: %v", err)
	}
	if gotUSDC.MaxSupply != nil {
		t.Errorf("USDC MaxSupply = %v, want nil", gotUSDC.MaxSupply)
	}

	// ─── SupplyHistory returns both XLM rows in time order ──────
	hist, err := store.SupplyHistory(ctx, "XLM", t0.Add(-1*time.Hour), t1.Add(1*time.Hour), 0)
	if err != nil {
		t.Fatalf("SupplyHistory: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("SupplyHistory returned %d rows, want 2", len(hist))
	}
	if hist[0].LedgerSequence != 50_000_000 {
		t.Errorf("hist[0].LedgerSequence = %d, want 50_000_000 (ascending order)", hist[0].LedgerSequence)
	}
	if hist[1].LedgerSequence != 50_001_000 {
		t.Errorf("hist[1].LedgerSequence = %d, want 50_001_000", hist[1].LedgerSequence)
	}

	// SupplyHistory with limit caps results.
	histLimited, err := store.SupplyHistory(ctx, "XLM", t0.Add(-1*time.Hour), t1.Add(1*time.Hour), 1)
	if err != nil {
		t.Fatalf("SupplyHistory (limit=1): %v", err)
	}
	if len(histLimited) != 1 {
		t.Errorf("SupplyHistory(limit=1) returned %d rows", len(histLimited))
	}

	// SupplyHistory across an empty window returns []
	histEmpty, err := store.SupplyHistory(ctx, "XLM", t0.Add(2*time.Hour), t0.Add(3*time.Hour), 0)
	if err != nil {
		t.Fatalf("SupplyHistory (empty window): %v", err)
	}
	if len(histEmpty) != 0 {
		t.Errorf("SupplyHistory across empty window returned %d rows", len(histEmpty))
	}
}
