package supply

import (
	"context"
	"fmt"
	"math/big"
)

// ConfigReserveBalanceReader is a [ReserveBalanceReader] backed by a
// static operator-supplied balance map. The supply-snapshot writer
// uses it as the bootstrap fallback in the chained-reader pattern
// (see docs/architecture/supply-pipeline.md §"The chained-fallback
// reader pattern"): the live [LCMReserveBalanceReader] takes
// precedence when every watched account has an observation, and
// this reader fills the gap when the AccountEntry observer hasn't
// backfilled yet (or, transiently, on storage error).
//
// Operator usage: populate
// `[supply] reserve_balances_stroops = { "G..." = "12345..." }` in
// the operator config. The writer constructs one of these from that
// map and passes it into the chained reader; once the observer has
// covered every account in `sdf_reserve_accounts`, the static map
// is no longer consulted.
//
// Limitations (as a fallback):
//
//   - Static map — no automatic balance refresh. Stale entries
//     would yield a stale circulating-supply number rather than a
//     wrong-by-fabrication one. Mitigated by the writer's stale-
//     ledger guard: the snapshot is attributed to the ledger the
//     operator passes to the CLI, not to "now".
//   - No per-account ledger versioning. The reader returns whatever
//     the config says for the requested account regardless of the
//     `ledger` argument. The live [LCMReserveBalanceReader] is the
//     ledger-aware path; this fallback is intentionally
//     ledger-agnostic since its purpose is bring-up only.
type ConfigReserveBalanceReader struct {
	balances map[string]*big.Int
}

// NewConfigReserveBalanceReader constructs a reader from a balance
// map. Stroop values are decimal strings (NUMERIC-safe per
// ADR-0003) parsed at construction so a malformed entry fails fast
// at startup rather than mid-snapshot.
//
// Empty input is valid — yields a reader that returns zero for any
// account list (equivalent to "no reserves to exclude"). The
// XLMComputer treats a zero-account input as a configuration where
// the operator hasn't enumerated reserves yet; circulating equals
// total.
func NewConfigReserveBalanceReader(balancesStroops map[string]string) (*ConfigReserveBalanceReader, error) {
	parsed := make(map[string]*big.Int, len(balancesStroops))
	for acc, raw := range balancesStroops {
		if acc == "" {
			return nil, fmt.Errorf("supply: ConfigReserveBalanceReader: empty account key in balance map")
		}
		v, ok := new(big.Int).SetString(raw, 10)
		if !ok {
			return nil, fmt.Errorf("supply: ConfigReserveBalanceReader: parse balance for %s: %q is not a decimal integer", acc, raw)
		}
		if v.Sign() < 0 {
			return nil, fmt.Errorf("supply: ConfigReserveBalanceReader: negative balance for %s: %s", acc, v.String())
		}
		parsed[acc] = v
	}
	return &ConfigReserveBalanceReader{balances: parsed}, nil
}

// ReserveBalanceTotal sums the configured balances for the supplied
// account list. Missing accounts return an error — silently treating
// an unknown account as zero would yield an over-stated circulating
// supply, which is exactly the failure mode ADR-0011 says we don't
// publish.
//
// The `ledger` argument is currently unused; see type-level docstring
// for why. Future LCM-observer reader will consume it.
func (r *ConfigReserveBalanceReader) ReserveBalanceTotal(_ context.Context, accounts []string, _ uint32) (*big.Int, error) {
	total := big.NewInt(0)
	for _, acc := range accounts {
		v, ok := r.balances[acc]
		if !ok {
			return nil, fmt.Errorf("supply: ConfigReserveBalanceReader: no balance configured for account %s", acc)
		}
		total = new(big.Int).Add(total, v)
	}
	return total, nil
}
