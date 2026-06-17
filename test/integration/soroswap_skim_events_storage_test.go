//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// TestSoroswapSkimEvent_Insert exercises the InsertSoroswapSkimEvent
// path against real TimescaleDB. Migration 0042. Inserts the two
// shapes the decoder emits in production:
//
//  1. Initial shape: amounts present, `to_address` NULL.
//  2. Future-upgrade shape: amounts present, `to_address` populated.
//
// Asserts ON CONFLICT DO NOTHING idempotency (replay-safety) and
// that the NUMERIC columns round-trip an above-int64 i128 (ADR-0003).
func TestSoroswapSkimEvent_Insert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	pair := contractStrkeyFromSeed(t, 0xA0)
	to := contractStrkeyFromSeed(t, 0xA1)
	closedAt := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	txHash := make([]byte, 32)
	for i := range txHash {
		txHash[i] = 0xBE
	}

	// Initial shape — no `to_address`.
	row1 := timescale.SoroswapSkimEvent{
		ContractID:      pair,
		Ledger:          52_000_000,
		LedgerCloseTime: closedAt,
		TxHash:          txHash,
		OpIndex:         0,
		EventIndex:      0,
		To:              "",
		Amount0:         "7500",
		Amount1:         "1234567",
	}
	if err := store.InsertSoroswapSkimEvent(ctx, row1); err != nil {
		t.Fatalf("Insert row1: %v", err)
	}

	// Future-upgrade shape — body includes a `to` field.
	row2 := row1
	row2.LedgerCloseTime = closedAt.Add(time.Second)
	row2.Ledger = 52_000_001
	row2.EventIndex = 1
	row2.To = to
	if err := store.InsertSoroswapSkimEvent(ctx, row2); err != nil {
		t.Fatalf("Insert row2: %v", err)
	}

	// Above-int64 i128 — ADR-0003 boundary. amounts must round-trip
	// exactly through the NUMERIC column.
	row3 := row1
	row3.LedgerCloseTime = closedAt.Add(2 * time.Second)
	row3.Ledger = 52_000_002
	row3.EventIndex = 2
	row3.Amount0 = "123456789012345678901234567890" // > 2^96
	if err := store.InsertSoroswapSkimEvent(ctx, row3); err != nil {
		t.Fatalf("Insert row3 (big i128): %v", err)
	}

	// Idempotency: re-insert row1 — no-op via ON CONFLICT DO NOTHING.
	if err := store.InsertSoroswapSkimEvent(ctx, row1); err != nil {
		t.Fatalf("Re-insert row1: %v", err)
	}

	// Verify exactly 3 distinct rows landed, the amounts and
	// to_address columns survived the round-trip, and the
	// to_address NULL semantics work.
	var count int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM soroswap_skim_events WHERE contract_id = $1`, pair).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 3 {
		t.Errorf("row count = %d, want 3", count)
	}

	var nullCount, populatedCount int
	if err := store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM soroswap_skim_events WHERE to_address IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count null: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM soroswap_skim_events WHERE to_address = $1`, to).Scan(&populatedCount); err != nil {
		t.Fatalf("count populated: %v", err)
	}
	if nullCount != 2 || populatedCount != 1 {
		t.Errorf("to_address split = (null=%d, populated=%d), want (2, 1)", nullCount, populatedCount)
	}

	var amt0Big sql.NullString
	if err := store.DB().QueryRowContext(ctx,
		`SELECT amount_0::text FROM soroswap_skim_events WHERE ledger = $1`, 52_000_002).
		Scan(&amt0Big); err != nil {
		t.Fatalf("scan big amount: %v", err)
	}
	if !amt0Big.Valid || amt0Big.String != "123456789012345678901234567890" {
		t.Errorf("big i128 round-trip lost precision: got %q want %q",
			amt0Big.String, "123456789012345678901234567890")
	}
}

// TestSoroswapSkimEvent_RejectsBadInputs verifies the defensive
// pre-flight checks before InsertSoroswapSkimEvent ever hits the DB.
// These are caller bugs, not chain truths — the decoder never emits
// the rejected shapes.
func TestSoroswapSkimEvent_RejectsBadInputs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	good := timescale.SoroswapSkimEvent{
		ContractID:      contractStrkeyFromSeed(t, 0xB0),
		Ledger:          1,
		LedgerCloseTime: time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC),
		TxHash:          []byte("01234567890123456789012345678901"), // 32 bytes
		OpIndex:         0,
		EventIndex:      0,
		Amount0:         "1",
		Amount1:         "2",
	}

	bad := []struct {
		name string
		mut  func(*timescale.SoroswapSkimEvent)
	}{
		{"empty ContractID", func(e *timescale.SoroswapSkimEvent) { e.ContractID = "" }},
		{"empty TxHash", func(e *timescale.SoroswapSkimEvent) { e.TxHash = nil }},
		{"zero LedgerCloseTime", func(e *timescale.SoroswapSkimEvent) { e.LedgerCloseTime = time.Time{} }},
		{"empty Amount0", func(e *timescale.SoroswapSkimEvent) { e.Amount0 = "" }},
		{"empty Amount1", func(e *timescale.SoroswapSkimEvent) { e.Amount1 = "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			row := good
			tc.mut(&row)
			if err := store.InsertSoroswapSkimEvent(ctx, row); err == nil {
				t.Errorf("InsertSoroswapSkimEvent(%s) = nil, want error", tc.name)
			}
		})
	}
}

// contractStrkeyFromSeed mirrors the soroswap unit-test helper:
// builds a valid C-strkey from a single-byte seed for deterministic
// fixtures.
func contractStrkeyFromSeed(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}
