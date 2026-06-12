//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical/discovery"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestDiscoveryRoundTrip exercises the SEP-41 auto-discovery
// storage layer end-to-end: RecordDiscovered → IsKnownDiscovered →
// ListDiscovered, including the ON CONFLICT update path that
// preserves first_seen_* and bumps event_count.
func TestDiscoveryRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const contractA = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	const contractB = "CBCZGGNOEUZG4CAAE7TGTQQHETZMKUT4OIPFHHPKEUX46U4KXBBZ3GLH"

	// Empty table: IsKnownDiscovered=false, ListDiscovered returns []
	known, err := store.IsKnownDiscovered(ctx, contractA)
	if err != nil {
		t.Fatalf("IsKnownDiscovered (empty): %v", err)
	}
	if known {
		t.Error("IsKnownDiscovered=true for empty table")
	}

	if rows, err := store.ListDiscovered(ctx, 0); err != nil || len(rows) != 0 {
		t.Errorf("ListDiscovered (empty): %d rows, err=%v", len(rows), err)
	}

	// First Record for contract A — mint event at ledger 50_000_000.
	hitA1 := discovery.Hit{
		ContractID:        contractA,
		EventType:         discovery.EventMint,
		Ledger:            50_000_000,
		ObservedAtRFC3339: "2026-04-01T12:00:00Z",
	}
	if err := store.RecordDiscovered(ctx, hitA1); err != nil {
		t.Fatalf("RecordDiscovered (first): %v", err)
	}

	// Second Record for SAME contract — transfer event at later
	// ledger. Must update last_seen_*, increment event_count, NOT
	// overwrite first_seen_event.
	hitA2 := hitA1
	hitA2.EventType = discovery.EventTransfer
	hitA2.Ledger = 50_000_500
	hitA2.ObservedAtRFC3339 = "2026-04-02T12:00:00Z"
	if err := store.RecordDiscovered(ctx, hitA2); err != nil {
		t.Fatalf("RecordDiscovered (second): %v", err)
	}

	// IsKnownDiscovered now true for A.
	known, _ = store.IsKnownDiscovered(ctx, contractA)
	if !known {
		t.Error("IsKnownDiscovered=false after Record")
	}

	// Record contract B — different contract.
	hitB := discovery.Hit{
		ContractID:        contractB,
		EventType:         discovery.EventBurn,
		Ledger:            50_001_000,
		ObservedAtRFC3339: "2026-04-03T12:00:00Z",
	}
	if err := store.RecordDiscovered(ctx, hitB); err != nil {
		t.Fatalf("RecordDiscovered (B): %v", err)
	}

	// ListDiscovered: 2 rows, B before A (newest first_seen_at first).
	rows, err := store.ListDiscovered(ctx, 0)
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListDiscovered returned %d rows, want 2", len(rows))
	}
	if rows[0].ContractID != contractB {
		t.Errorf("rows[0] = %q, want contract B (newest first)", rows[0].ContractID)
	}

	// Find the row for A and verify ON CONFLICT semantics.
	var rowA *timescale.DiscoveredAsset
	for i := range rows {
		if rows[i].ContractID == contractA {
			rowA = &rows[i]
		}
	}
	if rowA == nil {
		t.Fatal("contract A missing from ListDiscovered")
	}
	// First-write-wins on first_seen_*.
	if rowA.FirstSeenEvent != discovery.EventMint {
		t.Errorf("FirstSeenEvent = %q, want %q (first-write-wins)", rowA.FirstSeenEvent, discovery.EventMint)
	}
	if rowA.FirstSeenLedger != 50_000_000 {
		t.Errorf("FirstSeenLedger = %d, want 50_000_000", rowA.FirstSeenLedger)
	}
	// Last-write-wins on last_seen_*.
	if rowA.LastSeenLedger != 50_000_500 {
		t.Errorf("LastSeenLedger = %d, want 50_000_500", rowA.LastSeenLedger)
	}
	// event_count incremented to 2 (two Records).
	if rowA.EventCount != 2 {
		t.Errorf("EventCount = %d, want 2", rowA.EventCount)
	}

	// Limit clamps the result.
	rows, err = store.ListDiscovered(ctx, 1)
	if err != nil || len(rows) != 1 {
		t.Errorf("ListDiscovered(limit=1): %d rows, err=%v", len(rows), err)
	}
}
