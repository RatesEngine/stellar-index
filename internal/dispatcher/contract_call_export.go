package dispatcher

import "github.com/stellar/go-stellar-sdk/xdr"

// ContractCall is the public projection of one extracted InvokeContract
// call: the invoked contract (C-strkey), the function symbol, the base64
// SCVal args, and the auth-tree CallPath ([] for the top-level call).
//
// It exists so off-line tooling — specifically the completeness
// projection re-derive (ratesengine-ops compute-completeness) — can replay
// exactly what the live dispatcher routes to ContractCallDecoders over the
// certified ClickHouse lake, without reaching into the dispatcher's
// unexported call-tree internals. Event-less ContractCall sources (band,
// soroswap-router) have no soroban_events landing zone, so their projection
// census is re-derived by streaming InvokeContract ops from the lake and
// running this extraction + the source's ContractCallDecoder over each.
type ContractCall struct {
	ContractID   string
	FunctionName string
	Args         []string
	CallPath     []int
}

// ExtractContractCallTree returns every InvokeContract call reachable from a
// single operation — the Soroban auth-tree roots (the canonical source per
// task #48), or the top-level call as the fallback when the op carries no
// auth array. This is byte-identical to what the live dispatcher feeds its
// ContractCallDecoders (see extractInvokeContractCallTrees), so a census
// re-derived through it reconciles against the served tier by the same
// routing logic. Returns nil for non-InvokeContract ops.
func ExtractContractCallTree(op xdr.Operation) []ContractCall {
	trees := extractInvokeContractCallTrees([]xdr.Operation{op})
	if len(trees) == 0 || trees[0] == nil {
		return nil
	}
	out := make([]ContractCall, 0, len(trees[0]))
	for _, c := range trees[0] {
		out = append(out, ContractCall{
			ContractID:   c.ContractID,
			FunctionName: c.FunctionName,
			Args:         c.Args,
			CallPath:     c.CallPath,
		})
	}
	return out
}
