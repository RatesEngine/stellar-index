package supply

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// SEP41SupplyComponents is the per-token breakdown the
// [SEP41SupplyReader] returns at one ledger boundary. Per ADR-0011
// Algorithm 3, total_supply = MintTotal − BurnTotal − ClawbackTotal
// over the contract's lifetime; circulating = total − admin
// balance − locked-set balances.
//
// All fields are non-nil *big.Int. Zero is a valid value (a token
// with no clawbacks reports ClawbackTotal=0). The three event-sum
// fields are running totals over the contract's entire lifetime up
// to the supplied ledger; the indexer maintains these in
// asset_supply_history.
type SEP41SupplyComponents struct {
	// MintTotal is Σ mint.amount over the contract's lifetime.
	MintTotal *big.Int

	// BurnTotal is Σ burn.amount over the contract's lifetime.
	BurnTotal *big.Int

	// ClawbackTotal is Σ clawback.amount over the contract's
	// lifetime. Distinct from BurnTotal even though both reduce
	// total — operators want to see them separately for
	// compliance / forensic dashboards.
	ClawbackTotal *big.Int

	// AdminBalance is the token's admin account/contract balance
	// as observed at this ledger. The default locked-set member
	// per Algorithm 3; subtracted from circulating without operator
	// configuration.
	AdminBalance *big.Int

	// LockedAccountBalances is Σ token-balance across the
	// per-asset locked-set Accounts configured in
	// [Policy.PerAsset]. Returned by the reader so the computer
	// doesn't need a separate per-account query path.
	LockedAccountBalances *big.Int

	// LockedContractBalances is Σ token-balance across the
	// per-asset locked-set Contracts configured in
	// [Policy.PerAsset]. Same single-query rationale as
	// LockedAccountBalances.
	LockedContractBalances *big.Int
}

// SEP41SupplyReader is the read-side interface the [SEP41Computer]
// needs. Production implementation: a Postgres-backed reader against
// the SEP-41 event-sum running totals + the contract_data store the
// indexer maintains for admin / locked-balance lookups.
//
// Returns non-nil components on success; storage errors propagate
// as-is. The reader receives the contract id (asset.ContractID) and
// the operator-configured locked-set so it can produce the
// LockedAccount/LockedContract sums in a single query.
type SEP41SupplyReader interface {
	SEP41SupplyAt(ctx context.Context, asset canonical.Asset, locked LockedSet, ledger uint32) (SEP41SupplyComponents, error)
}

// SEP41Computer derives Algorithm 3 supply for SEP-41 Soroban tokens.
// Wraps a [Policy] (for per-asset locked-set + max_supply overrides)
// and a [SEP41SupplyReader] for component lookups.
//
// Safe for concurrent Compute() calls — fields are read-only after
// construction.
type SEP41Computer struct {
	policy Policy
	reader SEP41SupplyReader
}

// NewSEP41Computer constructs an Algorithm 3 computer. Returns
// ErrNilReader when reader is nil.
func NewSEP41Computer(policy Policy, reader SEP41SupplyReader) (*SEP41Computer, error) {
	if reader == nil {
		return nil, ErrNilReader
	}
	return &SEP41Computer{policy: policy, reader: reader}, nil
}

// ErrNotSoroban is returned by [SEP41Computer.Compute] when the
// supplied asset isn't a [canonical.AssetSoroban]. Routing bug
// safety net.
var ErrNotSoroban = errors.New("supply: asset is not a SEP-41 Soroban token")

// ErrNegativeTotalSupply is returned when MintTotal − BurnTotal −
// ClawbackTotal goes negative. Per SEP-41 semantics this is
// physically impossible (you can't burn more than was minted); a
// negative reading means the indexer's running totals mis-summed
// somewhere upstream. Surface the error rather than emit a
// negative-supply reading downstream.
var ErrNegativeTotalSupply = errors.New("supply: SEP-41 mint − burn − clawback went negative")

// Compute returns the [Supply] for a SEP-41 Soroban token at the
// supplied ledger. Per Algorithm 3:
//
//   - total_supply = MintTotal − BurnTotal − ClawbackTotal.
//   - max_supply = Policy.MaxSupplyOverrides[key] if set, else nil
//     (SEP-1 declaration overlay is a future PR).
//   - circulating_supply = total − AdminBalance − locked-set
//     balances. Admin balance is always excluded; per-asset
//     locked-set extends that.
//
// Basis is BasisOverride when MaxSupplyOverrides supplied a value
// OR the per-asset locked-set is non-empty; BasisAdminExclusion
// otherwise.
func (c *SEP41Computer) Compute(ctx context.Context, asset canonical.Asset, ledger uint32, observedAt time.Time) (Supply, error) {
	if asset.Type != canonical.AssetSoroban {
		return Supply{}, fmt.Errorf("%w: got type %q", ErrNotSoroban, asset.Type)
	}

	key, err := AssetKey(asset)
	if err != nil {
		return Supply{}, fmt.Errorf("supply: derive asset key: %w", err)
	}

	locked := c.policy.PerAsset[key]

	comps, err := c.reader.SEP41SupplyAt(ctx, asset, locked, ledger)
	if err != nil {
		return Supply{}, fmt.Errorf("supply: SEP-41 component read for %s at ledger %d: %w", key, ledger, err)
	}
	if err := validateSEP41Components(comps); err != nil {
		return Supply{}, fmt.Errorf("supply: SEP-41 reader returned invalid components for %s at ledger %d: %w", key, ledger, err)
	}

	// total = mint − burn − clawback
	total := new(big.Int).Set(comps.MintTotal)
	total.Sub(total, comps.BurnTotal)
	total.Sub(total, comps.ClawbackTotal)

	if total.Sign() < 0 {
		return Supply{}, fmt.Errorf("%w: mint=%s burn=%s clawback=%s for %s at ledger %d",
			ErrNegativeTotalSupply,
			comps.MintTotal, comps.BurnTotal, comps.ClawbackTotal,
			key, ledger)
	}

	// circulating = total − admin_balance − locked_account_balances − locked_contract_balances
	circulating := new(big.Int).Set(total)
	circulating.Sub(circulating, comps.AdminBalance)
	circulating.Sub(circulating, comps.LockedAccountBalances)
	circulating.Sub(circulating, comps.LockedContractBalances)

	// max_supply: operator override beats nil (SEP-1 overlay future PR).
	var maxSupply *big.Int
	basis := BasisAdminExclusion
	if override, ok, err := c.policy.MaxSupplyOverride(key); err != nil {
		return Supply{}, fmt.Errorf("supply: max_supply override for %s: %w", key, err)
	} else if ok {
		maxSupply = override
		basis = BasisOverride
	}

	if !locked.IsEmpty() && basis == BasisAdminExclusion {
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

// validateSEP41Components rejects nil pointers (would nil-pointer
// the arithmetic chain) and negative individual sums (event-sum
// running totals are non-negative by definition; a negative reading
// indicates indexer corruption upstream — refuse to publish rather
// than emit a physically-impossible reading).
func validateSEP41Components(c SEP41SupplyComponents) error {
	type field struct {
		name string
		val  *big.Int
	}
	for _, f := range []field{
		{"MintTotal", c.MintTotal},
		{"BurnTotal", c.BurnTotal},
		{"ClawbackTotal", c.ClawbackTotal},
		{"AdminBalance", c.AdminBalance},
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
