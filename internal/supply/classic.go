package supply

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ClassicSupplyComponents is the per-asset breakdown the
// [ClassicSupplyReader] returns at one ledger boundary. Per ADR-0011
// Algorithm 2, total_supply is the sum of the four downstream-
// holdings components; circulating is total minus the locked-set.
//
// All four are non-nil *big.Int (zero is a valid component value —
// e.g. an asset with no LP exposure has LPReserve == 0). The
// IssuerBalance is broken out separately so the computer can
// subtract it as the default locked-set member without re-querying.
type ClassicSupplyComponents struct {
	// Trustline is Σ trustline_balance for the asset across every
	// classic account that holds it (excluding the issuer's own
	// trustline — issuers don't hold their own asset via trustline,
	// they emit it).
	Trustline *big.Int

	// Claimable is Σ claimable_balance amount for the asset across
	// every open claimable balance entry.
	Claimable *big.Int

	// LPReserve is Σ liquidity_pool reserve for the asset across
	// every classic LP. The pool's pro-rata share for this asset
	// (NOT the asset's full LP-token holding side).
	LPReserve *big.Int

	// SACWrapped is Σ contract_data balance for the asset's SAC
	// wrapper, when the asset has a Stellar-Asset-Contract
	// deployment. Zero when no SAC is deployed.
	SACWrapped *big.Int

	// IssuerBalance is the amount the issuer is currently holding
	// (typically zero — the canonical "I'm not on my own
	// trustline" pattern — but pre-net or freezer-asset issuers
	// may hold a non-trivial amount). Subtracted from circulating
	// per the default locked-set policy.
	IssuerBalance *big.Int

	// LockedAccountBalances is Σ trustline_balance across the
	// per-asset locked-set Accounts configured in [Policy.PerAsset].
	// Returned by the reader so the computer doesn't need a
	// separate per-account query path. Zero when the locked-set
	// is empty or matches no holders.
	LockedAccountBalances *big.Int

	// LockedContractBalances is Σ contract_data balance across the
	// per-asset locked-set Contracts configured in
	// [Policy.PerAsset]. Same single-query rationale as
	// LockedAccountBalances.
	LockedContractBalances *big.Int
}

// ClassicSupplyReader is the read-side interface the
// [ClassicComputer] needs. Production implementation: a Postgres-
// backed reader against the trustline / claimable / LP / SAC
// hypertables maintained by the indexer.
//
// The reader is responsible for asset-key derivation; it accepts a
// [canonical.Asset] directly so a caller can't accidentally pass an
// asset_key string mismatching the indexer's storage shape. Returns
// non-nil components on success; storage errors propagate as-is.
type ClassicSupplyReader interface {
	ClassicSupplyAt(ctx context.Context, asset canonical.Asset, locked LockedSet, ledger uint32) (ClassicSupplyComponents, error)
}

// ClassicComputer derives Algorithm 2 supply for classic credit
// assets. Wraps a [Policy] (for per-asset locked-set + max_supply
// overrides) and a [ClassicSupplyReader] for component lookups.
//
// Safe for concurrent Compute() calls — fields are read-only after
// construction.
type ClassicComputer struct {
	policy Policy
	reader ClassicSupplyReader
}

// NewClassicComputer constructs an Algorithm 2 computer. Returns
// ErrNilReader when reader is nil — there's no meaningful work the
// computer can do without one.
func NewClassicComputer(policy Policy, reader ClassicSupplyReader) (*ClassicComputer, error) {
	if reader == nil {
		return nil, ErrNilReader
	}
	return &ClassicComputer{policy: policy, reader: reader}, nil
}

// ErrNotClassic is returned by [ClassicComputer.Compute] when the
// supplied asset isn't a [canonical.AssetClassic]. Callers route
// non-classic assets to the appropriate computer rather than letting
// this one silently produce garbage.
var ErrNotClassic = errors.New("supply: asset is not a classic credit asset")

// Compute returns the [Supply] for a classic credit asset at the
// supplied ledger. Per Algorithm 2:
//
//   - total_supply = Σ trustline + Σ claimable + Σ LP-reserve +
//     Σ SAC-wrapped (the four holding-domain components — see
//     [ClassicSupplyComponents]).
//   - max_supply = Policy.MaxSupplyOverrides[key] if set, else nil
//     (uncapped issuer; SEP-1 declaration overlay is a future PR).
//   - circulating_supply = total − (issuer balance + locked-account
//     balances + locked-contract balances). The issuer's own balance
//     is always excluded; per-asset locked-set extends that.
//
// Basis is BasisOverride when MaxSupplyOverrides supplied a value,
// BasisIssuerExclusion otherwise.
func (c *ClassicComputer) Compute(ctx context.Context, asset canonical.Asset, ledger uint32, observedAt time.Time) (Supply, error) {
	if asset.Type != canonical.AssetClassic {
		return Supply{}, fmt.Errorf("%w: got type %q", ErrNotClassic, asset.Type)
	}

	key, err := AssetKey(asset)
	if err != nil {
		return Supply{}, fmt.Errorf("supply: derive asset key: %w", err)
	}

	locked := c.policy.PerAsset[key] // zero value when missing — that's OK, falls back to issuer-only

	comps, err := c.reader.ClassicSupplyAt(ctx, asset, locked, ledger)
	if err != nil {
		return Supply{}, fmt.Errorf("supply: classic component read for %s at ledger %d: %w", key, ledger, err)
	}
	if err := validateClassicComponents(comps); err != nil {
		return Supply{}, fmt.Errorf("supply: classic reader returned invalid components for %s at ledger %d: %w", key, ledger, err)
	}

	total := new(big.Int)
	total.Add(total, comps.Trustline)
	total.Add(total, comps.Claimable)
	total.Add(total, comps.LPReserve)
	total.Add(total, comps.SACWrapped)

	// circulating = total − issuer_balance − locked_account_balances − locked_contract_balances
	circulating := new(big.Int).Set(total)
	circulating.Sub(circulating, comps.IssuerBalance)
	circulating.Sub(circulating, comps.LockedAccountBalances)
	circulating.Sub(circulating, comps.LockedContractBalances)

	// max_supply: operator override beats the default nil. SEP-1
	// declaration overlay is a future PR.
	var maxSupply *big.Int
	basis := BasisIssuerExclusion
	if override, ok, err := c.policy.MaxSupplyOverride(key); err != nil {
		return Supply{}, fmt.Errorf("supply: max_supply override for %s: %w", key, err)
	} else if ok {
		maxSupply = override
		basis = BasisOverride
	}

	// If the per-asset locked-set is non-empty AND no max-supply
	// override fired, basis upgrades to Override (the operator
	// expressed extra-than-default circulating policy).
	if !locked.IsEmpty() && basis == BasisIssuerExclusion {
		basis = BasisOverride
	}

	return Supply{
		AssetKey:          key,
		TotalSupply:       total,
		CirculatingSupply: circulating,
		MaxSupply:         maxSupply,
		Basis:             basis,
		LedgerSequence:    ledger,
		ObservedAt:        observedAt.UTC(),
	}, nil
}

// validateClassicComponents catches a misbehaving reader returning
// nil component pointers — would otherwise nil-pointer the Add/Sub
// chain. Cheap; runs once per Compute. Negative values are also
// rejected: classic-asset balances are non-negative by definition,
// and a negative reading means the indexer mis-summed somewhere.
func validateClassicComponents(c ClassicSupplyComponents) error {
	type field struct {
		name string
		val  *big.Int
	}
	for _, f := range []field{
		{"Trustline", c.Trustline},
		{"Claimable", c.Claimable},
		{"LPReserve", c.LPReserve},
		{"SACWrapped", c.SACWrapped},
		{"IssuerBalance", c.IssuerBalance},
		{"LockedAccountBalances", c.LockedAccountBalances},
		{"LockedContractBalances", c.LockedContractBalances},
	} {
		if f.val == nil {
			return fmt.Errorf("%s is nil", f.name)
		}
		if f.val.Sign() < 0 {
			return fmt.Errorf("%s is negative (%s)", f.name, f.val.String())
		}
	}
	return nil
}
