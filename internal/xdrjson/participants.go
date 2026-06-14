package xdrjson

import (
	"sort"

	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// ParticipantAccounts returns the non-source G-account strkeys an operation
// touches — the "incoming"/counterparty side the participant index (ADR-0038
// Phase B) needs so an account's RECEIVED activity (it's the payment
// destination, the trustor, the merge target, the clawback victim, …) is
// queryable, not just what it sourced.
//
// Implementation: decode the op body and collect every decoded field value
// that is a valid G-strkey. This is deliberately generic — it picks up
// destination / from / trustor / to_address / etc. across all field-decoded op
// types without a per-type participant list to drift. The operation's own
// source account is handled separately (it's a lake column), so it is NOT
// returned here. Deduplicated + sorted (deterministic → idempotent re-derive).
func ParticipantAccounts(bodyB64 string) ([]string, error) {
	d, err := DecodeOperationBody(bodyB64)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]struct{}{}
	for _, v := range d.Fields {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if !canonical.IsAccountID(s) {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}
