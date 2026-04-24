// Package band decodes on-chain price updates from Band Protocol's
// Soroban StandardReference contract.
//
// Architectural note: Band's Stellar contract **emits zero events**
// (verified 2026-04-22 via grep across
// bandprotocol/band-std-reference-contracts-soroban; confirmed
// 2026-04-24 against the pinned source). A conventional
// dispatcher.Decoder running on emitted events would never fire. So
// this package plugs into dispatcher.ContractCallDecoder instead —
// it observes the InvokeContract op itself, decoding the relayer's
// call args as the authoritative payload.
//
// Wire shape (verified
// .discovery-repos/band-soroban/src/contract.rs:23-35):
//
//	relay(from: Address, symbol_rates: Vec<(Symbol, u64)>,
//	      resolve_time: u64, request_id: u64)
//	force_relay(symbol_rates: Vec<(Symbol, u64)>,
//	            resolve_time: u64, request_id: u64)
//
// `force_relay` drops the `from` arg — admin-only path, not gated
// by the relayer check. Both produce the same logical output: one
// (Symbol, rate) pair per entry written to Band's ref_data storage.
//
// Rates: u64 at E9 scale (adapter/config.rs). Single-symbol rates
// are USD-denominated per the Band convention — `get_ref_data(XYZ)`
// returns XYZ priced in USD. Pair rates (`get_reference_data`) are
// computed on-read at E18; we don't emit those from relay calls
// because they're a function of storage state, not the wire input.
//
// Timestamps: resolve_time is UNIX seconds (
// band-soroban/src/storage/ref_data.rs:56 compares against
// `env.ledger().timestamp()` which is seconds).
//
// See docs/discovery/oracles/band.md for the full analysis.
package band

import "errors"

// SourceName is stamped on every OracleUpdate this package emits.
// Single source — Band has one StandardReference contract per
// network.
const SourceName = "band"

// DefaultDecimals is the Band single-symbol rate scale —
// `E9 = 10^9` per band-soroban/src/constant.rs. Every relayed rate
// is u64 at this scale.
const DefaultDecimals uint8 = 9

// DefaultResolutionSeconds reflects Band's variable relayer
// cadence — secondary validation source, not real-time. 60s aligns
// with the poll-cadence recommendation in the discovery doc.
const DefaultResolutionSeconds = 60

// Relay function names on the StandardReference contract. Both
// produce symbol_rates updates; the decoder matches either.
const (
	FnRelay      = "relay"
	FnForceRelay = "force_relay"
)

// Errors returned by the decode path.
var (
	// ErrNotBandCall — the ContractCallContext's contract+function
	// pair doesn't identify a Band relay/force_relay call. Skip;
	// the decoder only owns these two functions.
	ErrNotBandCall = errors.New("band: not a StandardReference relay/force_relay call")

	// ErrMalformedArgs — the op args don't decode to the expected
	// shape for the claimed function. Either a contract upgrade
	// shifted the signature, or the envelope is broken.
	ErrMalformedArgs = errors.New("band: malformed InvokeContract args")

	// ErrEmptyRates — the symbol_rates vector was empty. Band
	// relayers don't normally submit empty batches; surface loudly.
	ErrEmptyRates = errors.New("band: empty symbol_rates vector")

	// ErrUnknownSymbol — a symbol in symbol_rates doesn't map to
	// any canonical asset (not on the fiat allow-list, not on the
	// crypto allow-list). Per-entry skip; other entries in the
	// same call still land.
	ErrUnknownSymbol = errors.New("band: symbol not in fiat or crypto allow-lists")
)
