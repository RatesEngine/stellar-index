package soroswap_router

import (
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Decoder implements dispatcher.ContractCallDecoder. The Soroswap
// router emits no events itself (its swap functions call down to
// per-pair contracts that emit `SoroswapPair("swap")`); we hook
// the InvokeContract call directly so the router-level intent
// (path + amounts + deadline) is captured distinct from the per-
// pair leg-level swaps.
//
// Same wire pattern as Band's StandardReference decoder
// (internal/sources/band) — both protocols expose state-changing
// functions that don't publish events.
//
// Stateless. Matching is O(1) string compare on (contract_id,
// function_name); SCVal arg parsing only happens for matching
// calls.
type Decoder struct {
	routerContract string
}

// NewDecoder constructs a router decoder bound to the given
// contract address. Mainnet callers pass [MainnetRouter];
// testnet / dev callers pass the testnet address.
func NewDecoder(routerContract string) *Decoder {
	return &Decoder{routerContract: routerContract}
}

// Name implements [dispatcher.ContractCallDecoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.ContractCallDecoder]. Cheap
// predicate: contract ID == the configured router AND function
// is one of the two swap entry points. Admin functions
// (set_pair_fee, set_protocol_fee_recipient, init, …) don't move
// tokens and aren't useful for trade attribution.
func (d *Decoder) Matches(contractID, functionName string) bool {
	if contractID != d.routerContract {
		return false
	}
	return functionName == FnSwapExactTokensForTokens ||
		functionName == FnSwapTokensForExactTokens
}

// Decode implements [dispatcher.ContractCallDecoder]. Emits one
// Event per matched call. Returning an error is a "skip + count"
// signal per the dispatcher's contract — a malformed call doesn't
// abort the ledger, just gets dropped + counted.
func (d *Decoder) Decode(ctx dispatcher.ContractCallContext) ([]consumer.Event, error) {
	swap, err := decodeRouterArgs(
		ctx.FunctionName, ctx.Args,
		ctx.ContractID,
		ctx.Ledger, ctx.TxHash,
		ctx.OpIndex,
		ctx.OpSource, ctx.TxSource,
		ctx.ClosedAt,
	)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{Event{Swap: *swap}}, nil
}
