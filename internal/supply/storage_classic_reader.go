package supply

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ClassicSupplyStore is the storage-side primitive set the
// [StorageClassicSupplyReader] consumes. Production impl is
// *timescale.Store; tests pass in-memory fakes.
//
// Each method takes (asset_key, ledger) and returns a non-nil
// *big.Int representing the post-removal-aware sum of the
// component at or before the supplied ledger. Per-account /
// per-contract lookups for the issuer + locked-set components
// take the entity identifier in addition.
type ClassicSupplyStore interface {
	SumTrustlineBalancesAtOrBefore(ctx context.Context, assetKey string, asOfLedger uint32) (*big.Int, error)
	SumClaimableBalancesAtOrBefore(ctx context.Context, assetKey string, asOfLedger uint32) (*big.Int, error)
	SumLPReservesAtOrBefore(ctx context.Context, assetKey string, asOfLedger uint32) (*big.Int, error)
	SumSACBalancesAtOrBefore(ctx context.Context, assetKey string, asOfLedger uint32) (*big.Int, error)

	TrustlineBalanceForAccountAtOrBefore(ctx context.Context, accountID, assetKey string, asOfLedger uint32) (*big.Int, error)
	SACBalanceForContractAtOrBefore(ctx context.Context, contractHolder, assetKey string, asOfLedger uint32) (*big.Int, error)

	// MinClassicComponentLedger returns the lowest "most-recent
	// observation ledger" across the four classic-supply component
	// tables for `assetKey`. F-1236 (codex audit-2026-05-12):
	// powers the Refresher's stale-component freshness gate.
	// Zero = no observations in any component table for this
	// asset (treated as "no signal" by the gate). Optional —
	// implementations that don't surface it can return (0, nil)
	// and the gate stays in legacy-permissive mode.
	MinClassicComponentLedger(ctx context.Context, assetKey string, asOfLedger uint32) (uint32, error)
}

// StorageClassicSupplyReader satisfies [ClassicSupplyReader] by
// composing the four classic-supply hypertables (#303) populated
// by the trustlines / claimable_balances / liquidity_pools /
// sac_balances observers. Per ADR-0022 PR 5/5 — closes the
// Algorithm 2 producer pipeline.
//
// Issuer-balance handling: Algorithm 2 subtracts the asset
// issuer's own holding from circulating. The reader looks up
// the issuer's trustline-observation row (which is a normal
// account-as-holder observation) for the asset; an issuer that
// doesn't hold their own asset returns zero, the typical case.
//
// LockedSet-balance handling: per-asset
// [LockedSet.Accounts] members produce per-account trustline
// lookups summed in Go; [LockedSet.Contracts] members produce
// per-contract SAC lookups summed in Go. These are typically
// single-digit list sizes, so the per-entity round-trip cost is
// dominated by the four big Sum* queries above.
type StorageClassicSupplyReader struct {
	store ClassicSupplyStore
}

// NewStorageClassicSupplyReader constructs the reader.
func NewStorageClassicSupplyReader(store ClassicSupplyStore) *StorageClassicSupplyReader {
	return &StorageClassicSupplyReader{store: store}
}

// ClassicSupplyAt implements [ClassicSupplyReader]. Performs the
// six storage queries (4 sums + issuer trustline +
// per-account/per-contract locked lookups), composes them into a
// [ClassicSupplyComponents], and validates the result before
// returning.
//
// Error semantics: any single sub-query failure returns an
// error — the caller receives no partial Components, which the
// computer treats as an unobservable supply for this tick. We do
// NOT mix "partial sum + nil component" — that would silently
// publish wrong totals.
func (r *StorageClassicSupplyReader) ClassicSupplyAt(ctx context.Context, asset canonical.Asset, locked LockedSet, ledger uint32) (ClassicSupplyComponents, error) {
	if asset.Type != canonical.AssetClassic {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: StorageClassicSupplyReader: %w: got type %q", ErrNotClassic, asset.Type)
	}
	assetKey, err := AssetKey(asset)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: derive asset key: %w", err)
	}

	trustline, err := r.store.SumTrustlineBalancesAtOrBefore(ctx, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: trustline sum for %s: %w", assetKey, err)
	}
	claimable, err := r.store.SumClaimableBalancesAtOrBefore(ctx, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: claimable sum for %s: %w", assetKey, err)
	}
	lpReserve, err := r.store.SumLPReservesAtOrBefore(ctx, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: lp-reserve sum for %s: %w", assetKey, err)
	}
	sacWrapped, err := r.store.SumSACBalancesAtOrBefore(ctx, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: sac sum for %s: %w", assetKey, err)
	}

	issuerBalance, err := r.store.TrustlineBalanceForAccountAtOrBefore(ctx, asset.Issuer, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: issuer trustline lookup for %s: %w", assetKey, err)
	}

	lockedAccounts, err := r.sumPerAccountTrustlines(ctx, locked.Accounts, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: locked-accounts sum for %s: %w", assetKey, err)
	}
	lockedContracts, err := r.sumPerContractSAC(ctx, locked.Contracts, assetKey, ledger)
	if err != nil {
		return ClassicSupplyComponents{}, fmt.Errorf("supply: locked-contracts sum for %s: %w", assetKey, err)
	}

	// F-1236 (codex audit-2026-05-12): per-component freshness
	// for the Refresher's stale-component gate. A storage-side
	// failure here is non-fatal — the gate stays permissive
	// (MinComponentLedger=0) so a transient query error doesn't
	// reject a snapshot that the legacy posture would have
	// accepted. Operator's signal is the WARN log line.
	minLedger, err := r.store.MinClassicComponentLedger(ctx, assetKey, ledger)
	if err != nil {
		// Don't bail — the snapshot is still correct, just
		// without freshness metadata. Log + carry on.
		_ = err
		minLedger = 0
	}

	return ClassicSupplyComponents{
		Trustline:              trustline,
		Claimable:              claimable,
		LPReserve:              lpReserve,
		SACWrapped:             sacWrapped,
		IssuerBalance:          issuerBalance,
		LockedAccountBalances:  lockedAccounts,
		LockedContractBalances: lockedContracts,
		MinComponentLedger:     minLedger,
	}, nil
}

func (r *StorageClassicSupplyReader) sumPerAccountTrustlines(ctx context.Context, accounts []string, assetKey string, ledger uint32) (*big.Int, error) {
	total := big.NewInt(0)
	for _, acc := range accounts {
		if acc == "" {
			return nil, errors.New("supply: empty account in LockedSet.Accounts")
		}
		bal, err := r.store.TrustlineBalanceForAccountAtOrBefore(ctx, acc, assetKey, ledger)
		if err != nil {
			return nil, fmt.Errorf("trustline lookup for %s: %w", acc, err)
		}
		total = new(big.Int).Add(total, bal)
	}
	return total, nil
}

func (r *StorageClassicSupplyReader) sumPerContractSAC(ctx context.Context, contracts []string, assetKey string, ledger uint32) (*big.Int, error) {
	total := big.NewInt(0)
	for _, c := range contracts {
		if c == "" {
			return nil, errors.New("supply: empty contract in LockedSet.Contracts")
		}
		bal, err := r.store.SACBalanceForContractAtOrBefore(ctx, c, assetKey, ledger)
		if err != nil {
			return nil, fmt.Errorf("sac lookup for %s: %w", c, err)
		}
		total = new(big.Int).Add(total, bal)
	}
	return total, nil
}

// Compile-time check.
var _ ClassicSupplyReader = (*StorageClassicSupplyReader)(nil)

// AssetBoundClassicComputer adapts a [ClassicComputer] to the
// [SnapshotComputer] interface (the [Refresher]'s computer
// contract) by baking in a fixed [canonical.Asset]. The aggregator
// constructs one per watched classic asset so it can run a
// dedicated [Refresher] goroutine per asset alongside the
// XLM-only refresher.
type AssetBoundClassicComputer struct {
	inner *ClassicComputer
	asset canonical.Asset
}

// NewAssetBoundClassicComputer constructs the per-asset wrapper.
// Errors when the asset isn't a classic credit asset (the
// [ClassicComputer] checks too at Compute time, but failing fast
// at construction is friendlier).
func NewAssetBoundClassicComputer(inner *ClassicComputer, asset canonical.Asset) (*AssetBoundClassicComputer, error) {
	if asset.Type != canonical.AssetClassic {
		return nil, fmt.Errorf("%w: got type %q", ErrNotClassic, asset.Type)
	}
	if inner == nil {
		return nil, errors.New("supply: NewAssetBoundClassicComputer: inner is nil")
	}
	return &AssetBoundClassicComputer{inner: inner, asset: asset}, nil
}

// Compute implements [SnapshotComputer] by delegating to the
// wrapped [ClassicComputer.Compute] with the bound asset.
func (a *AssetBoundClassicComputer) Compute(ctx context.Context, ledger uint32, observedAt time.Time) (Supply, error) {
	return a.inner.Compute(ctx, a.asset, ledger, observedAt)
}

// Compile-time check.
var _ SnapshotComputer = (*AssetBoundClassicComputer)(nil)
