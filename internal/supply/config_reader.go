package supply

import (
	"context"
	"fmt"
	"math/big"
)

// ConfigReserveBalanceReader is a [ReserveBalanceReader] backed by a
// static operator-supplied balance map. It is the interim
// implementation used by the supply-snapshot writer until the
// LCM-based AccountEntry observer ships (see ADR-0011 + the deferred
// account-entry observer noted in internal/config/config.go's
// MetadataConfig comment).
//
// Operator usage: populate
// `[supply] reserve_balances_stroops = { "G..." = "12345..." }` in
// the operator config. The writer constructs one of these from that
// map and passes it to [XLMComputer]. When SDF announces a reserve
// move, the operator updates the config entry and re-runs the
// writer; the next snapshot reflects the new balance.
//
// Limitations:
//
//   - Static map — no automatic balance refresh. Stale entries
//     would yield a stale circulating-supply number rather than a
//     wrong-by-fabrication one. Mitigated by the writer's stale-
//     ledger guard: the snapshot is attributed to the ledger the
//     operator passes to the CLI, not to "now".
//   - No per-account ledger versioning. The reader returns whatever
//     the config says for the requested account regardless of the
//     `ledger` argument. Acceptable because the writer typically
//     attributes snapshots to the latest known ledger; future LCM-
//     observer reader will use the ledger arg to look up the
//     historical balance.
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
