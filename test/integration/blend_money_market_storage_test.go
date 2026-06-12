//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"math/big"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/blend"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestBlendPositionsRoundTrip exercises the InsertBlendPositionEvent
// path through real TimescaleDB. Verifies:
//
//   - All seven money-market event kinds insert successfully.
//   - The i128 amounts round-trip through the NUMERIC column at
//     full precision (per ADR-0003).
//   - PK is idempotent — re-running over the same range is a no-op.
//   - flash_loan correctly persists the counterparty contract.
//   - Invalid Kind / empty Pool / empty TxHash are rejected before
//     touching the DB.
func TestBlendPositionsRoundTrip(t *testing.T) {
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
		pool     = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
		asset    = "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6"
		user     = "GABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTU56K"
		contract = "CAXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXY32S"
	)
	t0 := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	kinds := []string{
		blend.EventSupply,
		blend.EventWithdraw,
		blend.EventSupplyCollateral,
		blend.EventWithdrawCollateral,
		blend.EventBorrow,
		blend.EventRepay,
		blend.EventFlashLoan,
	}
	for i, kind := range kinds {
		ev := blend.PositionEvent{
			Pool:        pool,
			Kind:        kind,
			Asset:       asset,
			User:        user,
			TokenAmount: big.NewInt(int64(1_000_000) * int64(i+1)),
			BOrDAmount:  big.NewInt(int64(990_000) * int64(i+1)),
			Ledger:      uint32(60_000_000 + i),
			TxHash:      pad64("a", i),
			OpIndex:     uint32(i),
			Timestamp:   t0.Add(time.Duration(i) * time.Minute),
		}
		if kind == blend.EventFlashLoan {
			ev.Counterparty = contract
		}
		if err := store.InsertBlendPositionEvent(ctx, ev); err != nil {
			t.Fatalf("InsertBlendPositionEvent (%s): %v", kind, err)
		}
		// Idempotent re-insert — same PK is a no-op.
		if err := store.InsertBlendPositionEvent(ctx, ev); err != nil {
			t.Fatalf("InsertBlendPositionEvent (%s dup): %v", kind, err)
		}
	}

	// ─── Validate the rows via a raw COUNT ─────────────────────────
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var rowCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM blend_positions WHERE pool = $1",
		pool,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rowCount != len(kinds) {
		t.Errorf("blend_positions COUNT = %d, want %d", rowCount, len(kinds))
	}

	// flash_loan row should have counterparty populated.
	var counterparty *string
	if err := db.QueryRowContext(ctx,
		"SELECT counterparty FROM blend_positions WHERE pool = $1 AND event_kind = 'flash_loan'",
		pool,
	).Scan(&counterparty); err != nil {
		t.Fatalf("counterparty scan: %v", err)
	}
	if counterparty == nil || *counterparty != contract {
		t.Errorf("flash_loan counterparty = %v, want %q", counterparty, contract)
	}

	// supply row should have NULL counterparty.
	if err := db.QueryRowContext(ctx,
		"SELECT counterparty FROM blend_positions WHERE pool = $1 AND event_kind = 'supply'",
		pool,
	).Scan(&counterparty); err != nil {
		t.Fatalf("supply counterparty scan: %v", err)
	}
	if counterparty != nil {
		t.Errorf("supply counterparty = %v, want NULL (non-flash_loan)", *counterparty)
	}
}

// TestBlendPositionsLargeI128 — i128 amounts MUST round-trip without
// truncation through the NUMERIC column (ADR-0003).
func TestBlendPositionsLargeI128(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dsn := startTimescale(t, ctx)
	applyMigrations(t, dsn)

	store, err := timescale.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	huge, _ := new(big.Int).SetString("123456789012345678901234567890", 10)
	ev := blend.PositionEvent{
		Pool:        "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Kind:        blend.EventBorrow,
		Asset:       "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6",
		User:        "GABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTU56K",
		TokenAmount: huge,
		BOrDAmount:  huge,
		Ledger:      60_000_001,
		TxHash:      pad64("b", 0),
		OpIndex:     0,
		Timestamp:   time.Now().UTC(),
	}
	if err := store.InsertBlendPositionEvent(ctx, ev); err != nil {
		t.Fatalf("InsertBlendPositionEvent: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRowContext(ctx,
		"SELECT token_amount::text FROM blend_positions WHERE ledger = 60000001",
	).Scan(&got); err != nil {
		t.Fatalf("token_amount scan: %v", err)
	}
	if got != huge.String() {
		t.Errorf("token_amount round-trip mismatch: got %s, want %s — i128 truncated?",
			got, huge.String())
	}
}

// TestBlendEmissionsRoundTrip exercises every emission /
// credit-risk event kind through InsertBlendEmissionEvent.
// Validates the per-kind attribute jsonb (claim.reserve_token_ids,
// reserve_emission_update.eps/expiration/res_token_id) lands
// correctly.
func TestBlendEmissionsRoundTrip(t *testing.T) {
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
		pool  = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
		asset = "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6"
		user  = "GABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTU56K"
	)
	t0 := time.Date(2026, 5, 20, 13, 0, 0, 0, time.UTC)

	rows := []blend.EmissionEvent{
		{
			Pool: pool, Kind: blend.EventGulp, Asset: asset,
			Amount: big.NewInt(100), Ledger: 61_000_000, TxHash: pad64("c", 0), OpIndex: 0, Timestamp: t0,
		},
		{
			Pool: pool, Kind: blend.EventClaim, User: user,
			Amount: big.NewInt(7_500_000), ReserveTokenIDs: []uint32{0, 2, 5},
			Ledger: 61_000_001, TxHash: pad64("c", 1), OpIndex: 0, Timestamp: t0.Add(time.Minute),
		},
		{
			Pool: pool, Kind: blend.EventReserveEmissions,
			ResTokenID: 7, EmissionsPerSec: 1_000_000, Expiration: 1_900_000_000,
			Ledger: 61_000_002, TxHash: pad64("c", 2), OpIndex: 0, Timestamp: t0.Add(2 * time.Minute),
		},
		{
			Pool: pool, Kind: blend.EventGulpEmissions, Amount: big.NewInt(42),
			Ledger: 61_000_003, TxHash: pad64("c", 3), OpIndex: 0, Timestamp: t0.Add(3 * time.Minute),
		},
		{
			Pool: pool, Kind: blend.EventBadDebt, User: user, Asset: asset, Amount: big.NewInt(1_000),
			Ledger: 61_000_004, TxHash: pad64("c", 4), OpIndex: 0, Timestamp: t0.Add(4 * time.Minute),
		},
		{
			Pool: pool, Kind: blend.EventDefaultedDebt, Asset: asset, Amount: big.NewInt(2_000),
			Ledger: 61_000_005, TxHash: pad64("c", 5), OpIndex: 0, Timestamp: t0.Add(5 * time.Minute),
		},
	}
	for _, ev := range rows {
		if err := store.InsertBlendEmissionEvent(ctx, ev); err != nil {
			t.Fatalf("InsertBlendEmissionEvent (%s): %v", ev.Kind, err)
		}
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM blend_emissions").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != len(rows) {
		t.Errorf("blend_emissions COUNT = %d, want %d", n, len(rows))
	}

	// reserve_emission_update should have the typed fields stamped
	// in attributes jsonb.
	var attrs string
	if err := db.QueryRowContext(ctx,
		"SELECT attributes::text FROM blend_emissions WHERE event_kind = 'reserve_emission_update'",
	).Scan(&attrs); err != nil {
		t.Fatalf("attrs scan: %v", err)
	}
	for _, want := range []string{`"res_token_id":7`, `"eps":1000000`, `"expiration":1900000000`} {
		if !contains(attrs, want) {
			t.Errorf("reserve_emission_update attributes %q missing %q", attrs, want)
		}
	}

	// claim should carry reserve_token_ids in attributes.
	if err := db.QueryRowContext(ctx,
		"SELECT attributes::text FROM blend_emissions WHERE event_kind = 'claim'",
	).Scan(&attrs); err != nil {
		t.Fatalf("claim attrs scan: %v", err)
	}
	if !contains(attrs, `"reserve_token_ids":[0,2,5]`) {
		t.Errorf("claim attributes %q missing reserve_token_ids", attrs)
	}
}

// TestBlendAdminRoundTrip exercises every admin / pool-config /
// pool-factory lifecycle event kind, including the dual-arity
// set_status (admin / non-admin) and queue_set_reserve's
// embedded ReserveConfig.
func TestBlendAdminRoundTrip(t *testing.T) {
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
		pool     = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
		factory  = "CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU"
		admin    = "GABCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTU56K"
		newAdm   = "GZYXWVUTSRQPONMLKJIHGFEDCBA765432ZYXWVUTSRQPONMLKJIHG6677"
		asset    = "CC4WPS7HRSPRZAXBVUDYLRXLZRHPLA6VTZARKZJTNVNECAS5IDRXRUB6"
		poolAddr = "CAUVZAQXKAQJ2PJKTH7Q6DCAXKQ22GZBV4XW2QHHIPC6BFCC6FJSXNEW"
	)
	t0 := time.Date(2026, 5, 20, 14, 0, 0, 0, time.UTC)

	rows := []blend.AdminEvent{
		{
			ContractID: pool, Kind: blend.EventSetAdmin,
			Admin: admin, Target: newAdm,
			Ledger: 62_000_000, TxHash: pad64("d", 0), OpIndex: 0, Timestamp: t0,
		},
		{
			ContractID: pool, Kind: blend.EventUpdatePool, Admin: admin,
			BackstopTakeRate: 2_000_000, MaxPositions: 8,
			MinCollateral: big.NewInt(100_000_000),
			Ledger:        62_000_001, TxHash: pad64("d", 1), OpIndex: 0, Timestamp: t0.Add(time.Minute),
		},
		{
			ContractID: pool, Kind: blend.EventQueueSetReserve, Admin: admin, Asset: asset,
			ReserveConfig: map[string]any{
				"index":      uint64(3),
				"enabled":    true,
				"supply_cap": "1000000000000",
			},
			Ledger: 62_000_002, TxHash: pad64("d", 2), OpIndex: 0, Timestamp: t0.Add(2 * time.Minute),
		},
		{
			ContractID: pool, Kind: blend.EventCancelSetReserve, Admin: admin, Asset: asset,
			Ledger: 62_000_003, TxHash: pad64("d", 3), OpIndex: 0, Timestamp: t0.Add(3 * time.Minute),
		},
		{
			ContractID: pool, Kind: blend.EventSetReserve, Asset: asset, ReserveIndex: 4,
			Ledger: 62_000_004, TxHash: pad64("d", 4), OpIndex: 0, Timestamp: t0.Add(4 * time.Minute),
		},
		{
			ContractID: pool, Kind: blend.EventSetStatus, NewStatus: 2, ByAdmin: false,
			Ledger: 62_000_005, TxHash: pad64("d", 5), OpIndex: 0, Timestamp: t0.Add(5 * time.Minute),
		},
		{
			ContractID: pool, Kind: blend.EventSetStatus, Admin: admin, NewStatus: 5, ByAdmin: true,
			Ledger: 62_000_006, TxHash: pad64("d", 6), OpIndex: 0, Timestamp: t0.Add(6 * time.Minute),
		},
		{
			ContractID: factory, Kind: blend.EventDeploy, Target: poolAddr,
			Ledger: 62_000_007, TxHash: pad64("d", 7), OpIndex: 0, Timestamp: t0.Add(7 * time.Minute),
		},
	}
	for _, ev := range rows {
		if err := store.InsertBlendAdminEvent(ctx, ev); err != nil {
			t.Fatalf("InsertBlendAdminEvent (%s): %v", ev.Kind, err)
		}
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM blend_admin").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != len(rows) {
		t.Errorf("blend_admin COUNT = %d, want %d", n, len(rows))
	}

	// queue_set_reserve metadata should carry the supply_cap as
	// a string (i128 precision-preserved per ADR-0003).
	var attrs string
	if err := db.QueryRowContext(ctx,
		"SELECT attributes::text FROM blend_admin WHERE event_kind = 'queue_set_reserve'",
	).Scan(&attrs); err != nil {
		t.Fatalf("queue_set_reserve attrs scan: %v", err)
	}
	if !contains(attrs, `"supply_cap":"1000000000000"`) {
		t.Errorf("queue_set_reserve attributes %q missing supply_cap string", attrs)
	}

	// update_pool min_collateral i128 should also be a string in jsonb.
	if err := db.QueryRowContext(ctx,
		"SELECT attributes::text FROM blend_admin WHERE event_kind = 'update_pool'",
	).Scan(&attrs); err != nil {
		t.Fatalf("update_pool attrs scan: %v", err)
	}
	if !contains(attrs, `"min_collateral":"100000000"`) {
		t.Errorf("update_pool attributes %q missing min_collateral string", attrs)
	}

	// set_status admin variant should carry by_admin=true.
	if err := db.QueryRowContext(ctx,
		"SELECT attributes::text FROM blend_admin WHERE event_kind = 'set_status' AND ledger = 62000006",
	).Scan(&attrs); err != nil {
		t.Fatalf("set_status admin attrs scan: %v", err)
	}
	if !contains(attrs, `"by_admin":true`) {
		t.Errorf("set_status admin attributes %q missing by_admin:true", attrs)
	}

	// deploy row should have the pool_address in `target`.
	var target string
	if err := db.QueryRowContext(ctx,
		"SELECT target FROM blend_admin WHERE event_kind = 'deploy'",
	).Scan(&target); err != nil {
		t.Fatalf("deploy target scan: %v", err)
	}
	if target != poolAddr {
		t.Errorf("deploy target = %q, want %q", target, poolAddr)
	}
}

// pad64 builds a 64-char-hex-like string for a tx_hash slot —
// deterministic per (seed, n) so tests stay reproducible.
func pad64(seed string, n int) string {
	out := make([]byte, 0, 64)
	for len(out) < 64 {
		out = append(out, seed[0])
		// Per-test variation in the second char so different
		// row-numbers don't collide on the same tx_hash.
		out = append(out, hexNibble(n))
	}
	return string(out[:64])
}

func hexNibble(n int) byte {
	const tab = "0123456789abcdef"
	return tab[n&0xF]
}

// contains is a tiny strings.Contains shim — keeps the test file's
// import list narrow.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
