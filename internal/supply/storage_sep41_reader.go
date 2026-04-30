package supply

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// SEP41SupplyStore is the storage-side primitive set the
// [StorageSEP41SupplyReader] consumes. Production impl is
// *timescale.Store; tests pass in-memory fakes.
//
// SEP41KindTotalsAtOrBefore returns the per-kind running sums
// (mint / burn / clawback). SACBalanceForContractAtOrBefore is
// reused from ADR-0022's classic-supply storage — pure SEP-41
// contracts share the same `DataKey::Balance(Address) → i128`
// shape as the SAC wrapper, so once an operator adds the
// SEP-41 contract to `[supply.sac_wrappers]` the same observer
// populates locked-set lookups for it.
type SEP41SupplyStore interface {
	SEP41KindTotalsAtOrBefore(ctx context.Context, contractID string, asOfLedger uint32) (SEP41KindTotals, error)
	SACBalanceForContractAtOrBefore(ctx context.Context, contractHolder, assetKey string, asOfLedger uint32) (*big.Int, error)
}

// SEP41KindTotals mirrors the timescale.SEP41KindTotals shape
// without importing timescale (which would create a cycle —
// timescale already imports supply for InsertSupply). The
// aggregator-side adapter projects between the two types.
type SEP41KindTotals struct {
	Mint     *big.Int
	Burn     *big.Int
	Clawback *big.Int
}

// StorageSEP41SupplyReader satisfies [SEP41SupplyReader] by
// composing the SEP41 event-sum totals (#309) plus the
// SAC-balance per-contract lookup primitive (#303). Per ADR-0023
// PR 3/4 — closes the algorithm 3 reader path.
//
// AdminBalance handling: Algorithm 3 names AdminBalance as a
// separate field, but the SEP-41 admin is operator-policy (the
// admin is whoever the contract's `set_admin` event last named).
// At v1 we don't track set_admin; instead, operators put the
// admin's strkey in the per-asset LockedSet alongside other
// locked addresses. AdminBalance is therefore always 0 from this
// reader; the practical effect on circulating is identical
// (locked-set sums + admin balance both subtract).
//
// LockedAccount/LockedContract handling: per-entity SAC-balance
// lookups summed in Go. Per-asset LockedSet sizes are typically
// single-digit, so the round-trip cost is bounded.
type StorageSEP41SupplyReader struct {
	store SEP41SupplyStore
}

// NewStorageSEP41SupplyReader constructs the reader.
func NewStorageSEP41SupplyReader(store SEP41SupplyStore) *StorageSEP41SupplyReader {
	return &StorageSEP41SupplyReader{store: store}
}

// SEP41SupplyAt implements [SEP41SupplyReader]. Performs the
// kind-totals query plus per-(account, contract) SAC-balance
// lookups for the locked set. AdminBalance is returned as 0 —
// see type-level docstring for the rationale.
func (r *StorageSEP41SupplyReader) SEP41SupplyAt(ctx context.Context, asset canonical.Asset, locked LockedSet, ledger uint32) (SEP41SupplyComponents, error) {
	if asset.Type != canonical.AssetSoroban {
		return SEP41SupplyComponents{}, fmt.Errorf("supply: StorageSEP41SupplyReader: %w: got type %q", ErrNotSoroban, asset.Type)
	}
	contractID := asset.ContractID
	if contractID == "" {
		return SEP41SupplyComponents{}, errors.New("supply: StorageSEP41SupplyReader: empty ContractID")
	}

	totals, err := r.store.SEP41KindTotalsAtOrBefore(ctx, contractID, ledger)
	if err != nil {
		return SEP41SupplyComponents{}, fmt.Errorf("supply: SEP41 kind totals for %s: %w", contractID, err)
	}

	// SEP-41 holders' balances live in the contract's own
	// ContractData entries under DataKey::Balance(Address). The
	// SAC observer queries those by (contract_id, holder); for
	// pure SEP-41 contracts the asset_key is the contract_id
	// itself per supply.AssetKey.
	lockedAccounts, err := r.sumPerHolder(ctx, locked.Accounts, contractID, ledger)
	if err != nil {
		return SEP41SupplyComponents{}, fmt.Errorf("supply: locked-accounts sum for %s: %w", contractID, err)
	}
	lockedContracts, err := r.sumPerHolder(ctx, locked.Contracts, contractID, ledger)
	if err != nil {
		return SEP41SupplyComponents{}, fmt.Errorf("supply: locked-contracts sum for %s: %w", contractID, err)
	}

	return SEP41SupplyComponents{
		MintTotal:              totals.Mint,
		BurnTotal:              totals.Burn,
		ClawbackTotal:          totals.Clawback,
		AdminBalance:           big.NewInt(0),
		LockedAccountBalances:  lockedAccounts,
		LockedContractBalances: lockedContracts,
	}, nil
}

func (r *StorageSEP41SupplyReader) sumPerHolder(ctx context.Context, holders []string, assetKey string, ledger uint32) (*big.Int, error) {
	total := big.NewInt(0)
	for _, h := range holders {
		if h == "" {
			return nil, errors.New("supply: empty holder in LockedSet")
		}
		bal, err := r.store.SACBalanceForContractAtOrBefore(ctx, h, assetKey, ledger)
		if err != nil {
			return nil, fmt.Errorf("sac lookup for %s: %w", h, err)
		}
		total = new(big.Int).Add(total, bal)
	}
	return total, nil
}

// Compile-time check.
var _ SEP41SupplyReader = (*StorageSEP41SupplyReader)(nil)

// AssetBoundSEP41Computer adapts a [SEP41Computer] to the
// [SnapshotComputer] interface (the [Refresher]'s computer
// contract) by baking in a fixed [canonical.Asset]. Mirrors
// [AssetBoundClassicComputer] from #307 — the aggregator
// constructs one per watched SEP-41 contract for its dedicated
// Refresher goroutine.
type AssetBoundSEP41Computer struct {
	inner *SEP41Computer
	asset canonical.Asset
}

// NewAssetBoundSEP41Computer constructs the per-contract wrapper.
// Errors when the asset isn't a Soroban contract (the
// SEP41Computer also checks at Compute time; failing fast at
// construction is friendlier).
func NewAssetBoundSEP41Computer(inner *SEP41Computer, asset canonical.Asset) (*AssetBoundSEP41Computer, error) {
	if asset.Type != canonical.AssetSoroban {
		return nil, fmt.Errorf("%w: got type %q", ErrNotSoroban, asset.Type)
	}
	if inner == nil {
		return nil, errors.New("supply: NewAssetBoundSEP41Computer: inner is nil")
	}
	return &AssetBoundSEP41Computer{inner: inner, asset: asset}, nil
}

// Compute implements [SnapshotComputer] by delegating to the
// wrapped [SEP41Computer.Compute] with the bound asset.
func (a *AssetBoundSEP41Computer) Compute(ctx context.Context, ledger uint32, observedAt time.Time) (Supply, error) {
	return a.inner.Compute(ctx, a.asset, ledger, observedAt)
}

var _ SnapshotComputer = (*AssetBoundSEP41Computer)(nil)
