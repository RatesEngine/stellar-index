package band

import (
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
)

// Decoder implements dispatcher.ContractCallDecoder. Unlike the
// event-based Decoders in sibling packages, Band plugs in here
// because its Soroban contract emits zero events — the relayer's
// `relay()` / `force_relay()` InvokeContract call carries the full
// update payload. See docs/discovery/oracles/band.md + events.go.
//
// No goroutines, no state. Matching is by (contract_id, function
// name) — O(1) string compare, no SCVal parsing on the hot path.
type Decoder struct {
	standardReferenceContract string
}

// NewDecoder constructs a Band Decoder bound to the mainnet
// StandardReference contract. Testnet callers pass the testnet
// address instead; the decoder has no knowledge of which is which.
func NewDecoder(standardReferenceContract string) *Decoder {
	return &Decoder{standardReferenceContract: standardReferenceContract}
}

// Name implements [dispatcher.ContractCallDecoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.ContractCallDecoder]. Cheap
// predicate — checks contract ID and one of the two relay entry
// points. Any other function on the same contract (get_ref_data,
// add_relayers, init, …) is read-only or admin and doesn't affect
// our price view.
func (d *Decoder) Matches(contractID, functionName string) bool {
	if contractID != d.standardReferenceContract {
		return false
	}
	return functionName == FnRelay || functionName == FnForceRelay
}

// Decode implements [dispatcher.ContractCallDecoder]. Emits zero
// or more UpdateEvent wrappers — one per (symbol, rate) entry in
// symbol_rates after USD special-casing and unknown-symbol skips.
func (d *Decoder) Decode(ctx dispatcher.ContractCallContext) ([]consumer.Event, error) {
	updates, err := decodeRelayArgs(
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
	out := make([]consumer.Event, 0, len(updates))
	for _, u := range updates {
		out = append(out, UpdateEvent{Update: u})
	}
	return out, nil
}
