//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	c "github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestSourceEntryCounts_AtomicIdempotentBump is the correctness core
// of the always-on entry tally (migration 0035): the writers bump
// source_entry_counts ATOMICALLY and IDEMPOTENTLY. A backfill
// re-walk that re-inserts already-stored rows (ON CONFLICT DO
// NOTHING → 0 rows) must NOT inflate the tally — otherwise every
// `-resume` / parallel-chunk replay would drift the count upward,
// re-creating exactly the "legacy data" class of bug this design
// exists to avoid.
func TestSourceEntryCounts_AtomicIdempotentBump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	xlm, err := c.NewCryptoAsset("XLM")
	if err != nil {
		t.Fatal(err)
	}
	usd, err := c.NewFiatAsset("USD")
	if err != nil {
		t.Fatal(err)
	}
	xlmUSD, _ := c.NewPair(xlm, usd)
	ts := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	count := func(source string) int64 {
		m, err := store.SourceEntryCounts(ctx)
		if err != nil {
			t.Fatalf("SourceEntryCounts: %v", err)
		}
		return m[source]
	}

	tr1 := mkIntegrationTrade("sdex", 1, ts, xlmUSD, 100_000_000, 12_000_000)
	tr2 := mkIntegrationTrade("sdex", 2, ts, xlmUSD, 100_000_000, 12_000_000)

	// First insert → tally 1.
	if err := store.InsertTrade(ctx, tr1); err != nil {
		t.Fatalf("InsertTrade tr1: %v", err)
	}
	if got := count("sdex"); got != 1 {
		t.Fatalf("after first insert: sdex entries = %d, want 1", got)
	}

	// Re-insert the SAME trade (backfill re-walk). PK conflict →
	// DO NOTHING → the HAVING-gated counter upsert must be a no-op.
	if err := store.InsertTrade(ctx, tr1); err != nil {
		t.Fatalf("InsertTrade tr1 (replay): %v", err)
	}
	if got := count("sdex"); got != 1 {
		t.Fatalf("after replay: sdex entries = %d, want 1 (idempotent)", got)
	}

	// A genuinely new trade for the same source → tally 2.
	if err := store.InsertTrade(ctx, tr2); err != nil {
		t.Fatalf("InsertTrade tr2: %v", err)
	}
	if got := count("sdex"); got != 2 {
		t.Fatalf("after second distinct insert: sdex entries = %d, want 2", got)
	}

	// Oracle updates feed the SAME tally (the whole point of the
	// rename: "entries", not "trades"). Same idempotency contract.
	ou := c.OracleUpdate{
		Source:    "reflector-dex",
		Ledger:    50_000_123,
		TxHash:    strings.Repeat("ab", 32),
		OpIndex:   0,
		Timestamp: ts,
		Asset:     xlm,
		Quote:     usd,
		Price:     c.NewAmount(big.NewInt(1_2345678901234)),
		Decimals:  14,
	}
	if err := store.InsertOracleUpdate(ctx, ou); err != nil {
		t.Fatalf("InsertOracleUpdate: %v", err)
	}
	if err := store.InsertOracleUpdate(ctx, ou); err != nil {
		t.Fatalf("InsertOracleUpdate (replay): %v", err)
	}
	if got := count("reflector-dex"); got != 1 {
		t.Fatalf("oracle entries = %d, want 1 (idempotent across oracle_updates)", got)
	}
	// Trade tally untouched by oracle ingest.
	if got := count("sdex"); got != 2 {
		t.Fatalf("sdex entries drifted to %d after oracle insert, want 2", got)
	}

	// SeedSourceEntryCounts is the authoritative reconcile: it must
	// CORRECT drift (SET, not ADD). Poison the tally, reseed, verify
	// it snaps back to the real table totals.
	if _, err := store.DB().ExecContext(ctx,
		`UPDATE source_entry_counts SET entry_count = 99999 WHERE source = 'sdex'`); err != nil {
		t.Fatalf("poison: %v", err)
	}
	if _, err := store.SeedSourceEntryCounts(ctx); err != nil {
		t.Fatalf("SeedSourceEntryCounts: %v", err)
	}
	if got := count("sdex"); got != 2 {
		t.Fatalf("after reseed: sdex entries = %d, want 2 (authoritative recount)", got)
	}
	if got := count("reflector-dex"); got != 1 {
		t.Fatalf("after reseed: reflector-dex entries = %d, want 1", got)
	}
}
