//go:build integration

package integration_test

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/sorobanevents"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestSorobanEventsBatchInsert exercises the
// InsertSorobanEventsBatch → CountSorobanEventsInRange paths against
// real TimescaleDB. ADR-0029. Inserts 100 synthetic rows across the
// full topic+body+op_args shape coverage and asserts they all land.
// Also verifies idempotency (re-inserting the same batch is a no-op
// via ON CONFLICT DO NOTHING).
func TestSorobanEventsBatchInsert(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	t0 := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)

	rows := make([]sorobanevents.Row, 0, 100)
	for i := 0; i < 100; i++ {
		rows = append(rows, mkSyntheticRow(t, uint32(50_000_000+i), t0.Add(time.Duration(i)*time.Second), byte(i)))
	}

	if err := store.InsertSorobanEventsBatch(ctx, rows); err != nil {
		t.Fatalf("InsertSorobanEventsBatch: %v", err)
	}

	got, err := store.CountSorobanEventsInRange(ctx, 50_000_000, 50_000_099)
	if err != nil {
		t.Fatalf("CountSorobanEventsInRange: %v", err)
	}
	if got != 100 {
		t.Errorf("CountSorobanEventsInRange = %d, want 100", got)
	}

	// Idempotency: re-insert the same batch — count unchanged.
	if err := store.InsertSorobanEventsBatch(ctx, rows); err != nil {
		t.Fatalf("re-insert: %v", err)
	}
	got2, err := store.CountSorobanEventsInRange(ctx, 50_000_000, 50_000_099)
	if err != nil {
		t.Fatalf("CountSorobanEventsInRange (after re-insert): %v", err)
	}
	if got2 != 100 {
		t.Errorf("after re-insert count = %d, want 100 (idempotent)", got2)
	}

	// Empty batch is a no-op.
	if err := store.InsertSorobanEventsBatch(ctx, nil); err != nil {
		t.Errorf("InsertSorobanEventsBatch(nil) = %v, want nil", err)
	}
	if err := store.InsertSorobanEventsBatch(ctx, []sorobanevents.Row{}); err != nil {
		t.Errorf("InsertSorobanEventsBatch(empty slice) = %v, want nil", err)
	}
}

// mkSyntheticRow constructs one well-formed sorobanevents.Row with
// distinct (ledger, tx_hash) so the batch insert exercises the PK
// + index paths.
func mkSyntheticRow(t *testing.T, ledger uint32, ts time.Time, seed byte) sorobanevents.Row {
	t.Helper()
	var cid [32]byte
	cid[0] = seed
	cid[1] = 0xAA
	cstrk, err := strkey.Encode(strkey.VersionByteContract, cid[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	txh := make([]byte, 32)
	for i := range txh {
		txh[i] = seed
	}
	// Topic 0 — encode a plausible Symbol so topic_0_sym exercises
	// the not-NULL index path on some rows. For variety: alternate
	// rows have a NULL topic_0_sym (we use empty string for those).
	topic0 := mkBytes(seed, 16)
	var sym string
	if seed%2 == 0 {
		sym = "synthetic_event"
	}
	return sorobanevents.Row{
		Ledger:          ledger,
		LedgerCloseTime: ts,
		TxHash:          txh,
		OpIndex:         int16(seed % 4),
		EventIndex:      0,
		ContractID:      cstrk,
		ContractIDHex:   cid[:],
		TopicCount:      2,
		Topic0Sym:       sym,
		Topic0XDR:       topic0,
		Topic1XDR:       mkBytes(seed, 12),
		Topic2XDR:       nil,
		Topic3XDR:       nil,
		BodyXDR:         mkBytes(seed, 64),
		OpArgsXDR:       mkBytes(seed, 24),
	}
}

func mkBytes(seed byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed ^ byte(i)
	}
	return b
}

// _ keeps hex imported in case follow-up tests want to inspect hex.
var _ = hex.EncodeToString
