//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/anomaly"
	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/freeze"
	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestFreezeEventSink_LKGVWAPLandsOnRow exercises the F-1228 path:
// `RecordFreeze` writes the frozen_value column with the
// orchestrator-supplied LKG VWAP rather than the hardcoded 0 the
// pre-F-1228 implementation used.
func TestFreezeEventSink_LKGVWAPLandsOnRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink := timescale.NewFreezeEventSink(store)

	asset, _ := c.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	quote, _ := c.NewFiatAsset("USD")

	const lkg = "0.999450000000"
	decision := anomaly.Decision{
		Action:       anomaly.ActionFreeze,
		Class:        anomaly.ClassStablecoin,
		DeviationPct: 7.5,
		Reason:       "deviation 7.5% exceeds 5% threshold for stablecoin",
	}

	if err := sink.RecordFreeze(ctx, asset, quote, lkg, decision); err != nil {
		t.Fatalf("RecordFreeze: %v", err)
	}

	// Read it back via raw SQL — easier than threading a Lookup API
	// just for this assertion.
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var (
		frozenValueRaw string
		reason         string
	)
	err = db.QueryRowContext(ctx, `
		SELECT frozen_value::text, reason
		  FROM freeze_events
		 WHERE asset_id = $1 AND quote_id = $2
		 ORDER BY frozen_at DESC
		 LIMIT 1
	`, asset.String(), quote.String()).Scan(&frozenValueRaw, &reason)
	if err != nil {
		t.Fatalf("read freeze_events row: %v", err)
	}
	// Postgres NUMERIC formats trailing zeros: 0.999450000000.
	if frozenValueRaw != lkg {
		t.Errorf("frozen_value = %q, want %q", frozenValueRaw, lkg)
	}
	// Reason classification from mapFreezeReason: deviation-driven
	// Phase 1 freezes map to outlier_storm.
	if reason != "outlier_storm" {
		t.Errorf("reason = %q, want outlier_storm", reason)
	}
}

// TestFreezeEventSink_RecoveryRoundTrip exercises the F-1229 path:
// ListOpen → MarkRecovered → ListOpen returns one fewer row. Pins
// the worker's contract against the postgres schema.
func TestFreezeEventSink_RecoveryRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink := timescale.NewFreezeEventSink(store)
	asset, _ := c.NewClassicAsset("USDT", "GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V")
	quote, _ := c.NewFiatAsset("USD")

	if err := sink.RecordFreeze(ctx, asset, quote, "1.000000000000", anomaly.Decision{
		Action: anomaly.ActionFreeze,
		Class:  anomaly.ClassStablecoin,
	}); err != nil {
		t.Fatalf("RecordFreeze: %v", err)
	}

	open, err := sink.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("expected 1 open freeze, got %d: %+v", len(open), open)
	}
	if open[0].Asset.String() != asset.String() {
		t.Errorf("open[0].Asset = %s, want %s", open[0].Asset.String(), asset.String())
	}
	// Conform to the freeze.OpenFreezePair shape that the recovery
	// worker consumes (compile-time check via type assertion).
	var _ freeze.OpenFreezePair = open[0]

	if err := sink.MarkRecovered(ctx, asset, quote); err != nil {
		t.Fatalf("MarkRecovered: %v", err)
	}

	open, err = sink.ListOpen(ctx)
	if err != nil {
		t.Fatalf("ListOpen after recovery: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("expected 0 open freezes after MarkRecovered, got %d: %+v", len(open), open)
	}

	// Re-running MarkRecovered on an already-closed pair returns
	// ErrNotFound — operators relying on the idempotent-by-skip
	// semantics catch this loudly.
	err = sink.MarkRecovered(ctx, asset, quote)
	if err == nil {
		t.Error("expected ErrNotFound on second MarkRecovered, got nil")
	}
}

// TestFreezeEventSink_IdempotentOpenRow — two RecordFreeze calls
// for the same (asset, quote) while the first is still firing
// (recovered_at NULL) MUST NOT insert a second row. Pre-F-1228 we
// relied on the (asset, quote, frozen_at) PK with microsecond
// resolution; the explicit `WHERE NOT EXISTS` clause is what
// guarantees idempotency.
func TestFreezeEventSink_IdempotentOpenRow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink := timescale.NewFreezeEventSink(store)
	asset, _ := c.NewClassicAsset("PYUSD", "GBGQNZAZ54NZWZA7KGOTOZYCXEYIQGOUJK7L6EM7EJD7AQRBKO7VSXJP")
	quote, _ := c.NewFiatAsset("USD")

	dec := anomaly.Decision{Action: anomaly.ActionFreeze, Class: anomaly.ClassStablecoin}
	for i := 0; i < 3; i++ {
		if err := sink.RecordFreeze(ctx, asset, quote, "1.000000000000", dec); err != nil {
			t.Fatalf("RecordFreeze #%d: %v", i, err)
		}
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var count int
	err = db.QueryRowContext(ctx, `
		SELECT count(*) FROM freeze_events
		 WHERE asset_id = $1 AND quote_id = $2
	`, asset.String(), quote.String()).Scan(&count)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 freeze_events row across 3 RecordFreeze calls, got %d", count)
	}
}
