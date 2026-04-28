package supply

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// xlmAssetKey is the single canonical key for native XLM in
// asset_supply_history + on the API surface. Every XLM-related
// computation produces this constant, never "native" / "XLM:" /
// other variants.
const xlmAssetKey = "XLM"

// XLMTotalSupplyStroops is the frozen-2019 native XLM total supply
// in stroops: 50_001_806_812 XLM × 10^7 stroops/XLM. The figure
// comprises the 50 B genesis lumens plus the inflation pool that
// was frozen by network vote in October 2019. Per ADR-0011 it does
// not move; only circulating changes (when SDF reserve account
// balances change).
//
// Constructed once at package init via init(); exposed as a function
// rather than a `var` to prevent accidental mutation by callers.
func XLMTotalSupplyStroops() *big.Int {
	return new(big.Int).Set(xlmTotalSupplyStroops)
}

// xlmTotalSupplyStroops is the underlying immutable value. Returned
// only via the [XLMTotalSupplyStroops] copy-constructor so callers
// can't mutate the package-level constant.
var xlmTotalSupplyStroops = new(big.Int).Mul(
	big.NewInt(50_001_806_812),
	big.NewInt(10_000_000), // 10^7 stroops per XLM
)

// ReserveBalanceReader is the read-side interface the [XLMComputer]
// needs: given a list of G-strkey account addresses, return their
// summed XLM balance in stroops as observed at LedgerSequence.
//
// Production implementation: a Postgres-backed reader against the
// trustline-delta indexer's per-account running totals (the same
// hypertable Algorithm 2 will read for classic asset supply). The
// reader is responsible for its own caching; the computer makes one
// call per Compute() invocation.
//
// Returns the summed balance as a non-nil *big.Int (zero is a valid
// answer — the SDF reserves could be empty in a hypothetical
// future). Returns an error when the storage layer can't satisfy
// the request; the computer surfaces that error rather than
// fabricating a partial answer.
type ReserveBalanceReader interface {
	ReserveBalanceTotal(ctx context.Context, accounts []string, ledger uint32) (*big.Int, error)
}

// XLMComputer derives Algorithm 1 supply for native XLM. Wraps a
// configured reserve-account list (from [Policy.SDFReserveAccounts])
// + a [ReserveBalanceReader] that resolves balances on demand.
//
// Safe for concurrent Compute() calls — fields are read-only after
// construction; the underlying ReserveBalanceReader is required to
// be concurrent-safe by contract.
type XLMComputer struct {
	reserveAccounts []string
	reader          ReserveBalanceReader
}

// NewXLMComputer constructs an Algorithm 1 computer.
//
// reader MAY be nil when reserveAccounts is empty — Compute() then
// short-circuits the lookup. A nil reader with a non-empty
// reserveAccounts list is a configuration error and returns ErrNilReader.
func NewXLMComputer(reserveAccounts []string, reader ReserveBalanceReader) (*XLMComputer, error) {
	if len(reserveAccounts) > 0 && reader == nil {
		return nil, ErrNilReader
	}
	// Defensive copy so a caller mutating their input slice can't
	// silently change the configured reserve list.
	reserved := append([]string(nil), reserveAccounts...)
	return &XLMComputer{
		reserveAccounts: reserved,
		reader:          reader,
	}, nil
}

// ErrNilReader is returned by [NewXLMComputer] when the caller
// supplied reserve accounts but no reader. Operator misconfig that
// would silently produce an over-stated circulating supply (no
// exclusion applied) — fail loudly at construction instead.
var ErrNilReader = errors.New("supply: reserve-balance reader is nil but reserve accounts are configured")

// Compute returns the [Supply] for native XLM at the supplied
// ledger. Per Algorithm 1:
//
//   - total_supply = XLMTotalSupplyStroops (constant).
//   - max_supply = total_supply (XLM is hard-capped).
//   - circulating_supply = total_supply − Σ(SDF reserve balances).
//
// observedAt should be the close time of `ledger` in UTC; callers
// pass the ledger-meta timestamp directly. Compute does NOT consult
// wall-clock time.
//
// Returns the underlying error (wrapped) when the
// [ReserveBalanceReader] fails. The computer does NOT fall back to
// "publish total as circulating" — the partial answer would be
// indistinguishable on the wire from a healthy zero-reserve state,
// so we surface the error and let the caller decide whether to skip
// this snapshot or retry.
func (c *XLMComputer) Compute(ctx context.Context, ledger uint32, observedAt time.Time) (Supply, error) {
	total := XLMTotalSupplyStroops()

	reserved := big.NewInt(0)
	if len(c.reserveAccounts) > 0 {
		var err error
		reserved, err = c.reader.ReserveBalanceTotal(ctx, c.reserveAccounts, ledger)
		if err != nil {
			return Supply{}, fmt.Errorf("supply: read SDF reserve balances at ledger %d: %w", ledger, err)
		}
		if reserved == nil {
			// Defence-in-depth: a misbehaving reader returning
			// (nil, nil) would otherwise nil-pointer in the Sub call
			// below. Treat as zero with a wrapped error so the
			// operator gets a clear signal.
			return Supply{}, fmt.Errorf("supply: reserve-balance reader returned nil at ledger %d", ledger)
		}
	}

	circulating := new(big.Int).Sub(total, reserved)

	return Supply{
		AssetKey:          xlmAssetKey,
		TotalSupply:       total,
		CirculatingSupply: circulating,
		MaxSupply:         new(big.Int).Set(total),
		Basis:             BasisXLMSDFReserveExclusion,
		LedgerSequence:    ledger,
		ObservedAt:        observedAt.UTC(),
	}, nil
}
