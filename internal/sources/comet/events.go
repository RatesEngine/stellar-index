// Package comet decodes on-chain events from Comet — a Soroban
// implementation of Balancer v1's weighted-AMM design. Pools hold
// N ≥ 2 tokens with arbitrary weights; trading preserves the
// weighted-geometric-mean invariant.
//
// Wire shape, verified 2026-04-23 against the public contract source
// (.discovery-repos/comet-contracts/contracts/src/c_pool/event.rs
// and call_logic/pool.rs:21/184-191):
//
//	topic[0] = Symbol("POOL")
//	topic[1] = Symbol("<event_name>")  — "swap" / "join_pool" /
//	                                     "exit_pool" / "deposit" /
//	                                     "withdraw"
//	body     = Map { "caller": Address,
//	                 "token_in": Address,
//	                 "token_out": Address,
//	                 "token_amount_in": i128,
//	                 "token_amount_out": i128 }   (for swap)
//
// `POOL` is a shared topic namespace across every Comet pool contract
// — the decoder matches by topic bytes, not pool contract ID, same
// pattern as Soroswap/Aquarius/Phoenix. Operators may want to
// down-filter by pool address if Comet deploys broadly, but v1
// observes every pool on mainnet that emits these events.
//
// v1 decodes only the "swap" variant (→ canonical.Trade). Join /
// exit / deposit / withdraw are reserve-tracking events; we'll need
// them once the aggregator wants live pool state, tracked as a
// follow-up.
package comet

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName is the canonical string stamped on every Trade this
// package emits. Single source — Comet has no versioned variants.
const SourceName = "comet"

// Event-topic constants. Matches against these are byte-equality
// via the pre-encoded TopicSymbol* blobs below.
const (
	EventTopic0 = "POOL"
	EventSwap   = "swap"
)

// Pre-encoded base64 SCVal::Symbol blobs for topic[0] / topic[1].
// symbol_short!("POOL") and symbol_short!("swap") in the Rust source
// — the Soroban-SDK Symbol marshaller matches scval.MustEncodeSymbol
// byte-for-byte (pinned by internal/scval/scval_test.go).
var (
	TopicSymbolPool = scval.MustEncodeSymbol(EventTopic0)
	TopicSymbolSwap = scval.MustEncodeSymbol(EventSwap)
)

// Errors returned by the decode path.
var (
	// ErrNotCometSwap — topic[0..1] doesn't match (POOL, swap).
	// Skip: not a Comet swap, another Comet event we haven't
	// implemented yet, or an unrelated contract.
	ErrNotCometSwap = errors.New("comet: not a Comet POOL.swap event")

	// ErrMalformedPayload — body didn't decode to the expected
	// SwapEvent map shape.
	ErrMalformedPayload = errors.New("comet: malformed SwapEvent payload")

	// ErrNonPositiveAmounts — token_amount_in or token_amount_out
	// is zero / negative. A valid swap always has positive amounts
	// on both sides; zero-amount is either a contract bug or an
	// edge case we'd rather skip+count than emit.
	ErrNonPositiveAmounts = errors.New("comet: swap amounts must be positive")
)
