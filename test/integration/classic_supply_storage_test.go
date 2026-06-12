//go:build integration

package integration_test

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// TestClassicSupplyObservationsRoundTrip exercises the four
// classic-supply hypertables shipped in #303 through real
// TimescaleDB. Each Sum*AtOrBefore method uses the same
// DISTINCT ON pattern + WHERE NOT is_removal filter; a SQL
// regression in the DISTINCT ON ordering or the is_removal
// handling silently mis-reports Algorithm 2 components.
//
// Companion to #316's SEP-41 coverage. The Insert + DISTINCT-ON
// + last-writer-wins semantics ship untested at the SQL level
// without this; Go-layer defensive guards in #303 catch
// invalid inputs but can't detect a SQL regression.
func TestClassicSupplyObservationsRoundTrip(t *testing.T) {
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
		assetUSDC  = "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
		assetOther = "AQUA:GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA"
		holderA    = "GA1"
		holderB    = "GA2"
	)
	t0 := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)

	t.Run("Trustline", func(t *testing.T) {
		// Insert two trustlines for USDC; sum should be the post-state total.
		insertTrustline(t, ctx, store, holderA, assetUSDC, 1000, 100, t0, false)
		insertTrustline(t, ctx, store, holderB, assetUSDC, 2000, 500, t0.Add(time.Hour), false)

		got, err := store.SumTrustlineBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if err != nil {
			t.Fatalf("Sum: %v", err)
		}
		if got.Cmp(big.NewInt(600)) != 0 {
			t.Errorf("Sum = %s, want 600 (100 + 500)", got)
		}

		// Update holderA — last-writer-wins on the same (account, asset, ledger).
		insertTrustline(t, ctx, store, holderA, assetUSDC, 1000, 999, t0, false)
		got, _ = store.SumTrustlineBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(1499)) != 0 {
			t.Errorf("Sum after upsert = %s, want 1499 (999 + 500)", got)
		}

		// Insert at a later ledger — DISTINCT ON should pick the latest.
		insertTrustline(t, ctx, store, holderA, assetUSDC, 3000, 250, t0.Add(2*time.Hour), false)
		got, _ = store.SumTrustlineBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(750)) != 0 {
			t.Errorf("Sum at ledger 5000 = %s, want 750 (250 + 500)", got)
		}

		// At-or-before ledger 1500: only the ledger-1000 row counts for holderA.
		got, _ = store.SumTrustlineBalancesAtOrBefore(ctx, assetUSDC, 1500)
		if got.Cmp(big.NewInt(999)) != 0 {
			t.Errorf("Sum at ledger 1500 = %s, want 999 (only ledger-1000 holderA)", got)
		}

		// Removal: holderA's most-recent observation is is_removal=true.
		insertTrustline(t, ctx, store, holderA, assetUSDC, 4000, 0, t0.Add(3*time.Hour), true)
		got, _ = store.SumTrustlineBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(500)) != 0 {
			t.Errorf("Sum after removal = %s, want 500 (holderA removed; only holderB at 500 remains)", got)
		}

		// Per-account lookup returns 0 for the removed row.
		balA, _ := store.TrustlineBalanceForAccountAtOrBefore(ctx, holderA, assetUSDC, 5000)
		if balA.Sign() != 0 {
			t.Errorf("removed account balance = %s, want 0", balA)
		}

		// Other-asset isolation — AQUA stays at 0.
		got, _ = store.SumTrustlineBalancesAtOrBefore(ctx, assetOther, 5000)
		if got.Sign() != 0 {
			t.Errorf("isolated asset sum = %s, want 0 (asset_key WHERE filter broken)", got)
		}
	})

	t.Run("Claimable", func(t *testing.T) {
		insertClaimable(t, ctx, store, "claimable-1", assetUSDC, 1000, 5000, t0, false)
		insertClaimable(t, ctx, store, "claimable-2", assetUSDC, 1000, 7000, t0, false)
		insertClaimable(t, ctx, store, "claimable-3", assetOther, 1000, 99, t0, false) // isolation

		got, err := store.SumClaimableBalancesAtOrBefore(ctx, assetUSDC, 2000)
		if err != nil {
			t.Fatalf("Sum: %v", err)
		}
		if got.Cmp(big.NewInt(12_000)) != 0 {
			t.Errorf("Sum = %s, want 12000", got)
		}

		// Claim of one balance — most-recent observation for claimable-1 is removal.
		insertClaimable(t, ctx, store, "claimable-1", assetUSDC, 1500, 0, t0.Add(time.Hour), true)
		got, _ = store.SumClaimableBalancesAtOrBefore(ctx, assetUSDC, 2000)
		if got.Cmp(big.NewInt(7_000)) != 0 {
			t.Errorf("Sum after claim = %s, want 7000", got)
		}

		// Other asset still isolated.
		got, _ = store.SumClaimableBalancesAtOrBefore(ctx, assetOther, 2000)
		if got.Cmp(big.NewInt(99)) != 0 {
			t.Errorf("isolated asset sum = %s, want 99", got)
		}
	})

	t.Run("LPReserve", func(t *testing.T) {
		const pool1 = "pool-1"
		const pool2 = "pool-2"
		// Pool 1 holds USDC + AQUA; pool 2 holds USDC + native (XLM/native).
		// We index per asset side, so each pool emits 2 rows on a delta.
		insertLPReserve(t, ctx, store, pool1, assetUSDC, 1000, 1_000_000, t0, false)
		insertLPReserve(t, ctx, store, pool1, assetOther, 1000, 2_000_000, t0, false)
		insertLPReserve(t, ctx, store, pool2, assetUSDC, 1000, 500_000, t0, false)

		got, err := store.SumLPReservesAtOrBefore(ctx, assetUSDC, 2000)
		if err != nil {
			t.Fatalf("Sum: %v", err)
		}
		if got.Cmp(big.NewInt(1_500_000)) != 0 {
			t.Errorf("Sum USDC = %s, want 1500000 (pool1 + pool2)", got)
		}

		// Pool 1 gets a swap — USDC reserve drops, AQUA reserve rises.
		insertLPReserve(t, ctx, store, pool1, assetUSDC, 2000, 800_000, t0.Add(time.Hour), false)
		insertLPReserve(t, ctx, store, pool1, assetOther, 2000, 2_500_000, t0.Add(time.Hour), false)
		got, _ = store.SumLPReservesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(1_300_000)) != 0 {
			t.Errorf("Sum USDC after swap = %s, want 1300000 (800K + 500K)", got)
		}

		// At-or-before ledger 1500: only the original observations count.
		got, _ = store.SumLPReservesAtOrBefore(ctx, assetUSDC, 1500)
		if got.Cmp(big.NewInt(1_500_000)) != 0 {
			t.Errorf("Sum at ledger 1500 = %s, want 1500000", got)
		}
	})

	t.Run("SACBalance", func(t *testing.T) {
		const sacContract = "sac-contract-USDC"
		const otherSAC = "sac-contract-AQUA"
		insertSAC(t, ctx, store, sacContract, holderA, assetUSDC, 1000, 100_000, t0, false)
		insertSAC(t, ctx, store, sacContract, holderB, assetUSDC, 1000, 200_000, t0, false)
		insertSAC(t, ctx, store, otherSAC, holderA, assetOther, 1000, 999, t0, false)

		got, err := store.SumSACBalancesAtOrBefore(ctx, assetUSDC, 2000)
		if err != nil {
			t.Fatalf("Sum: %v", err)
		}
		if got.Cmp(big.NewInt(300_000)) != 0 {
			t.Errorf("Sum USDC = %s, want 300000", got)
		}

		// holderA transfers — balance updates at later ledger.
		insertSAC(t, ctx, store, sacContract, holderA, assetUSDC, 2000, 50_000, t0.Add(time.Hour), false)
		got, _ = store.SumSACBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(250_000)) != 0 {
			t.Errorf("Sum after transfer = %s, want 250000 (50K + 200K)", got)
		}

		// Per-contract lookup for holderA returns the latest balance.
		balA, _ := store.SACBalanceForContractAtOrBefore(ctx, holderA, assetUSDC, 5000)
		if balA.Cmp(big.NewInt(50_000)) != 0 {
			t.Errorf("per-contract holderA = %s, want 50000", balA)
		}

		// Removed entry → 0.
		insertSAC(t, ctx, store, sacContract, holderA, assetUSDC, 3000, 0, t0.Add(2*time.Hour), true)
		got, _ = store.SumSACBalancesAtOrBefore(ctx, assetUSDC, 5000)
		if got.Cmp(big.NewInt(200_000)) != 0 {
			t.Errorf("Sum after removal = %s, want 200000 (only holderB remains)", got)
		}
		balA, _ = store.SACBalanceForContractAtOrBefore(ctx, holderA, assetUSDC, 5000)
		if balA.Sign() != 0 {
			t.Errorf("removed balance lookup = %s, want 0", balA)
		}

		// Asset isolation.
		got, _ = store.SumSACBalancesAtOrBefore(ctx, assetOther, 5000)
		if got.Cmp(big.NewInt(999)) != 0 {
			t.Errorf("isolated asset sum = %s, want 999", got)
		}
	})
}

// ─── Insert helpers ─────────────────────────────────────────────

func insertTrustline(t *testing.T, ctx context.Context, store *timescale.Store, account, assetKey string, ledger uint32, balance int64, observedAt time.Time, removal bool) {
	t.Helper()
	if err := store.InsertTrustlineObservation(ctx, timescale.TrustlineObservation{
		AccountID:  account,
		AssetKey:   assetKey,
		Ledger:     ledger,
		ObservedAt: observedAt,
		Balance:    big.NewInt(balance),
		IsRemoval:  removal,
	}); err != nil {
		t.Fatalf("InsertTrustline %s/%s@%d: %v", account, assetKey, ledger, err)
	}
}

func insertClaimable(t *testing.T, ctx context.Context, store *timescale.Store, claimableID, assetKey string, ledger uint32, balance int64, observedAt time.Time, removal bool) {
	t.Helper()
	if err := store.InsertClaimableObservation(ctx, timescale.ClaimableObservation{
		ClaimableID: claimableID,
		AssetKey:    assetKey,
		Ledger:      ledger,
		ObservedAt:  observedAt,
		Balance:     big.NewInt(balance),
		IsRemoval:   removal,
	}); err != nil {
		t.Fatalf("InsertClaimable %s@%d: %v", claimableID, ledger, err)
	}
}

func insertLPReserve(t *testing.T, ctx context.Context, store *timescale.Store, poolID, assetKey string, ledger uint32, balance int64, observedAt time.Time, removal bool) {
	t.Helper()
	if err := store.InsertLPReserveObservation(ctx, timescale.LPReserveObservation{
		PoolID:     poolID,
		AssetKey:   assetKey,
		Ledger:     ledger,
		ObservedAt: observedAt,
		Balance:    big.NewInt(balance),
		IsRemoval:  removal,
	}); err != nil {
		t.Fatalf("InsertLPReserve %s/%s@%d: %v", poolID, assetKey, ledger, err)
	}
}

func insertSAC(t *testing.T, ctx context.Context, store *timescale.Store, contractID, holder, assetKey string, ledger uint32, balance int64, observedAt time.Time, removal bool) {
	t.Helper()
	if err := store.InsertSACBalanceObservation(ctx, timescale.SACBalanceObservation{
		ContractID: contractID,
		AssetKey:   assetKey,
		Holder:     holder,
		Ledger:     ledger,
		ObservedAt: observedAt,
		Balance:    big.NewInt(balance),
		IsRemoval:  removal,
	}); err != nil {
		t.Fatalf("InsertSACBalance %s/%s@%d: %v", contractID, holder, ledger, err)
	}
}
