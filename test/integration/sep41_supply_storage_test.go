//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// TestSEP41SupplyEventsRoundTrip exercises the
// InsertSEP41SupplyEvent → SEP41NetMintAtOrBefore →
// SEP41KindTotalsAtOrBefore paths through real TimescaleDB.
// Per ADR-0023 + ADR-0011 Algorithm 3, the running net mint
// (mint - burn - clawback) IS the SEP-41 total supply; if the
// SQL CASE-WHEN sign-flip or DISTINCT ON / FILTER aggregations
// regress, total supply silently goes wrong. The unit tests
// (#309) cover defensive guards but can't validate the SQL —
// this test does.
func TestSEP41SupplyEventsRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const contractID = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC" // synthetic
	const otherContract = "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6"
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	// ─── Empty state: net mint = 0; kind totals all zero ─────────
	got, err := store.SEP41NetMintAtOrBefore(ctx, contractID, 1)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore (empty): %v", err)
	}
	if got.Sign() != 0 {
		t.Errorf("empty net mint = %s, want 0", got)
	}
	totals, err := store.SEP41KindTotalsAtOrBefore(ctx, contractID, 1)
	if err != nil {
		t.Fatalf("SEP41KindTotalsAtOrBefore (empty): %v", err)
	}
	if totals.Mint.Sign() != 0 || totals.Burn.Sign() != 0 || totals.Clawback.Sign() != 0 {
		t.Errorf("empty totals: mint=%s burn=%s clawback=%s, want all 0",
			totals.Mint, totals.Burn, totals.Clawback)
	}

	// ─── Insert a mint event at ledger 1000 ──────────────────────
	mintEvent := timescale.SEP41SupplyEvent{
		ContractID:   contractID,
		Ledger:       1000,
		TxHash:       "1100000000000000000000000000000000000000000000000000000000000001",
		OpIndex:      0,
		ObservedAt:   t0,
		Kind:         timescale.SEP41EventMint,
		Amount:       big.NewInt(1_000_000),
		Counterparty: "GA1",
	}
	if err := store.InsertSEP41SupplyEvent(ctx, mintEvent); err != nil {
		t.Fatalf("InsertSEP41SupplyEvent (mint): %v", err)
	}

	// Idempotent re-insert — same PK is a no-op.
	if err := store.InsertSEP41SupplyEvent(ctx, mintEvent); err != nil {
		t.Fatalf("InsertSEP41SupplyEvent (mint dup): %v", err)
	}

	// ─── Insert a burn at ledger 2000 ────────────────────────────
	if err := store.InsertSEP41SupplyEvent(ctx, timescale.SEP41SupplyEvent{
		ContractID:   contractID,
		Ledger:       2000,
		TxHash:       "1100000000000000000000000000000000000000000000000000000000000002",
		OpIndex:      0,
		ObservedAt:   t0.Add(time.Hour),
		Kind:         timescale.SEP41EventBurn,
		Amount:       big.NewInt(300_000),
		Counterparty: "GA1",
	}); err != nil {
		t.Fatalf("InsertSEP41SupplyEvent (burn): %v", err)
	}

	// ─── Insert a clawback at ledger 2500 ────────────────────────
	if err := store.InsertSEP41SupplyEvent(ctx, timescale.SEP41SupplyEvent{
		ContractID:   contractID,
		Ledger:       2500,
		TxHash:       "1100000000000000000000000000000000000000000000000000000000000003",
		OpIndex:      0,
		ObservedAt:   t0.Add(2 * time.Hour),
		Kind:         timescale.SEP41EventClawback,
		Amount:       big.NewInt(100_000),
		Counterparty: "GA2",
	}); err != nil {
		t.Fatalf("InsertSEP41SupplyEvent (clawback): %v", err)
	}

	// ─── Net mint = 1_000_000 − 300_000 − 100_000 = 600_000 ──────
	got, err = store.SEP41NetMintAtOrBefore(ctx, contractID, 3000)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore: %v", err)
	}
	if got.Cmp(big.NewInt(600_000)) != 0 {
		t.Errorf("net mint at ledger 3000 = %s, want 600000", got)
	}

	// ─── Kind totals split out cleanly ───────────────────────────
	totals, err = store.SEP41KindTotalsAtOrBefore(ctx, contractID, 3000)
	if err != nil {
		t.Fatalf("SEP41KindTotalsAtOrBefore: %v", err)
	}
	if totals.Mint.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Errorf("Mint = %s, want 1000000", totals.Mint)
	}
	if totals.Burn.Cmp(big.NewInt(300_000)) != 0 {
		t.Errorf("Burn = %s, want 300000", totals.Burn)
	}
	if totals.Clawback.Cmp(big.NewInt(100_000)) != 0 {
		t.Errorf("Clawback = %s, want 100000", totals.Clawback)
	}

	// ─── At-or-before ledger 1500: only the mint counts ──────────
	got, err = store.SEP41NetMintAtOrBefore(ctx, contractID, 1500)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore (1500): %v", err)
	}
	if got.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Errorf("net mint at ledger 1500 = %s, want 1000000 (burn+clawback excluded)", got)
	}

	// ─── At-or-before ledger 2000: mint + burn ───────────────────
	got, err = store.SEP41NetMintAtOrBefore(ctx, contractID, 2000)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore (2000): %v", err)
	}
	if got.Cmp(big.NewInt(700_000)) != 0 {
		t.Errorf("net mint at ledger 2000 = %s, want 700000 (1M − 300K, clawback at 2500 excluded)", got)
	}

	// ─── Other contract is isolated — its totals stay 0 ──────────
	got, err = store.SEP41NetMintAtOrBefore(ctx, otherContract, 5000)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore (otherContract): %v", err)
	}
	if got.Sign() != 0 {
		t.Errorf("isolated contract net mint = %s, want 0 — contract_id filter is broken",
			got)
	}
}

// TestSEP41SupplyEvents_LargeI128 verifies the SQL preserves
// values that exceed int64. SEP-41 amounts are i128 in the wire
// protocol; Algorithm 3's running sum must not silently truncate.
func TestSEP41SupplyEvents_LargeI128(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const contractID = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)

	if err := store.InsertSEP41SupplyEvent(ctx, timescale.SEP41SupplyEvent{
		ContractID: contractID,
		Ledger:     1,
		TxHash:     "2200000000000000000000000000000000000000000000000000000000000001",
		OpIndex:    0,
		ObservedAt: time.Now().UTC(),
		Kind:       timescale.SEP41EventMint,
		Amount:     huge,
	}); err != nil {
		t.Fatalf("InsertSEP41SupplyEvent (huge): %v", err)
	}

	got, err := store.SEP41NetMintAtOrBefore(ctx, contractID, 100)
	if err != nil {
		t.Fatalf("SEP41NetMintAtOrBefore: %v", err)
	}
	if got.Cmp(huge) != 0 {
		t.Errorf("got %s, want %s — i128 / NUMERIC round-trip lost precision", got, huge)
	}
}
