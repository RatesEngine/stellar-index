//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestCometLiquidityRoundTrip exercises the
// InsertCometLiquidity path through real TimescaleDB. Validates
// the migration-0042 schema (PK shape, NUMERIC i128 preservation,
// per-kind/direction CHECK constraints, withdraw-only
// pool_amount_in) by writing one row per kind and reading them
// back.
func TestCometLiquidityRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const (
		// Blend's backstop Comet pool (per docs/operations/wasm-audits/comet.md).
		pool   = "CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM"
		caller = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALI4"
		token1 = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC" // synthetic
		token2 = "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6"
	)
	t0 := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	// ─── join_pool ───────────────────────────────────────────────
	join := timescale.CometLiquidityEvent{
		ContractID:      pool,
		Ledger:          1000,
		LedgerCloseTime: t0,
		TxHash:          "1100000000000000000000000000000000000000000000000000000000000001",
		OpIndex:         0,
		Kind:            timescale.CometLiquidityJoinPool,
		Caller:          caller,
		Token:           token1,
		Amount:          canonical.NewAmount(big.NewInt(1_500_000_000)),
	}
	if err := store.InsertCometLiquidity(ctx, join); err != nil {
		t.Fatalf("InsertCometLiquidity (join_pool): %v", err)
	}
	// Idempotent re-insert — ON CONFLICT DO NOTHING.
	if err := store.InsertCometLiquidity(ctx, join); err != nil {
		t.Fatalf("InsertCometLiquidity (join_pool dup): %v", err)
	}

	// Multi-token join: same (ledger, tx_hash, op_index) but
	// different `token` — must NOT collide thanks to `token` in PK.
	join2 := join
	join2.Token = token2
	join2.Amount = canonical.NewAmount(big.NewInt(600_000))
	if err := store.InsertCometLiquidity(ctx, join2); err != nil {
		t.Fatalf("InsertCometLiquidity (join_pool token2): %v", err)
	}

	// ─── exit_pool ───────────────────────────────────────────────
	if err := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
		ContractID:      pool,
		Ledger:          1100,
		LedgerCloseTime: t0.Add(5 * time.Minute),
		TxHash:          "1100000000000000000000000000000000000000000000000000000000000002",
		OpIndex:         0,
		Kind:            timescale.CometLiquidityExitPool,
		Caller:          caller,
		Token:           token1,
		Amount:          canonical.NewAmount(big.NewInt(250_000_000)),
	}); err != nil {
		t.Fatalf("InsertCometLiquidity (exit_pool): %v", err)
	}

	// ─── deposit (single-asset) ──────────────────────────────────
	if err := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
		ContractID:      pool,
		Ledger:          1200,
		LedgerCloseTime: t0.Add(10 * time.Minute),
		TxHash:          "1100000000000000000000000000000000000000000000000000000000000003",
		OpIndex:         0,
		Kind:            timescale.CometLiquidityDeposit,
		Caller:          caller,
		Token:           token1,
		Amount:          canonical.NewAmount(big.NewInt(100_000)),
	}); err != nil {
		t.Fatalf("InsertCometLiquidity (deposit): %v", err)
	}

	// ─── withdraw (carries pool_amount_in) ───────────────────────
	if err := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
		ContractID:      pool,
		Ledger:          1300,
		LedgerCloseTime: t0.Add(15 * time.Minute),
		TxHash:          "1100000000000000000000000000000000000000000000000000000000000004",
		OpIndex:         0,
		Kind:            timescale.CometLiquidityWithdraw,
		Caller:          caller,
		Token:           token1,
		Amount:          canonical.NewAmount(big.NewInt(50_000)),
		PoolAmountIn:    canonical.NewAmount(big.NewInt(12_345)),
	}); err != nil {
		t.Fatalf("InsertCometLiquidity (withdraw): %v", err)
	}

	// ─── Read-back: per-kind row count + direction mapping ────────
	type rowCount struct {
		kind      string
		direction string
		n         int
	}
	rows, err := store.DB().QueryContext(ctx, `
		SELECT event_kind, direction, COUNT(*) AS n
		FROM comet_liquidity
		WHERE contract_id = $1
		GROUP BY event_kind, direction
		ORDER BY event_kind`, pool)
	if err != nil {
		t.Fatalf("read-back query: %v", err)
	}
	defer rows.Close()
	got := map[string]rowCount{}
	for rows.Next() {
		var rc rowCount
		if err := rows.Scan(&rc.kind, &rc.direction, &rc.n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[rc.kind] = rc
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	want := map[string]rowCount{
		"join_pool": {kind: "join_pool", direction: "add", n: 2}, // two tokens on same join
		"exit_pool": {kind: "exit_pool", direction: "remove", n: 1},
		"deposit":   {kind: "deposit", direction: "add", n: 1},
		"withdraw":  {kind: "withdraw", direction: "remove", n: 1},
	}
	for kind, w := range want {
		g, ok := got[kind]
		if !ok {
			t.Errorf("missing kind=%q in result", kind)
			continue
		}
		if g.direction != w.direction {
			t.Errorf("kind=%q direction = %q, want %q", kind, g.direction, w.direction)
		}
		if g.n != w.n {
			t.Errorf("kind=%q count = %d, want %d", kind, g.n, w.n)
		}
	}

	// ─── pool_amount_in is NULL on non-withdraw rows ─────────────
	var nonWithdrawWithPoolAmt int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM comet_liquidity
		WHERE contract_id = $1
		  AND event_kind <> 'withdraw'
		  AND pool_amount_in IS NOT NULL`, pool).Scan(&nonWithdrawWithPoolAmt); err != nil {
		t.Fatalf("pool_amount_in NULL check: %v", err)
	}
	if nonWithdrawWithPoolAmt != 0 {
		t.Errorf("found %d non-withdraw rows with non-NULL pool_amount_in; want 0", nonWithdrawWithPoolAmt)
	}

	// ─── pool_amount_in round-trips correctly on withdraw ────────
	var poolAmt canonical.Amount
	if err := store.DB().QueryRowContext(ctx, `
		SELECT pool_amount_in::text FROM comet_liquidity
		WHERE contract_id = $1 AND event_kind = 'withdraw'
		LIMIT 1`, pool).Scan(&poolAmt); err != nil {
		t.Fatalf("read pool_amount_in: %v", err)
	}
	if poolAmt.BigInt().Int64() != 12_345 {
		t.Errorf("pool_amount_in = %s, want 12345", poolAmt)
	}
}

// TestCometLiquidity_LargeI128 verifies the NUMERIC column preserves
// 128-bit values — per ADR-0003 a Soroban i128 must never be silently
// truncated. A multi-billion-share LP join is the realistic worst
// case (single XLM unit at the contract's own precision can already
// exceed int64).
func TestCometLiquidity_LargeI128(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const (
		pool   = "CAS3FL6TLZKDGGSISDBWGGPXT3NRR4DYTZD7YOD3HMYO6LTJUVGRVEAM"
		caller = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAALI4"
		token  = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	)
	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)

	if err := store.InsertCometLiquidity(ctx, timescale.CometLiquidityEvent{
		ContractID:      pool,
		Ledger:          5000,
		LedgerCloseTime: time.Now().UTC(),
		TxHash:          "2200000000000000000000000000000000000000000000000000000000000001",
		OpIndex:         0,
		Kind:            timescale.CometLiquidityJoinPool,
		Caller:          caller,
		Token:           token,
		Amount:          canonical.NewAmount(huge),
	}); err != nil {
		t.Fatalf("InsertCometLiquidity (huge): %v", err)
	}

	var amt canonical.Amount
	if err := store.DB().QueryRowContext(ctx, `
		SELECT amount::text FROM comet_liquidity
		WHERE contract_id = $1 LIMIT 1`, pool).Scan(&amt); err != nil {
		t.Fatalf("read amount: %v", err)
	}
	if amt.BigInt().Cmp(huge) != 0 {
		t.Errorf("got %s, want %s — i128 / NUMERIC round-trip lost precision", amt, huge)
	}
}
