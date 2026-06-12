// Package comet decodes on-chain events from Comet — a Soroban
// implementation of Balancer v1's weighted-AMM design. Pools hold
// N ≥ 2 tokens with arbitrary weights; trading preserves the
// weighted-geometric-mean invariant.
//
// Wire shape, verified 2026-04-23 against the public contract source
// (comet-contracts-v1/contracts/src/c_pool/event.rs and
// call_logic/pool.rs:21,184-191; re-verified 2026-05-26 against
// upstream `main` for the join/exit/deposit/withdraw additions):
//
//	topic[0] = Symbol("POOL")
//	topic[1] = Symbol("<event_name>")
//	body     = Map { … }      (shape per event)
//
// The Soroban port emits exactly **five** events under the shared
// `POOL` namespace:
//
//   - "swap"       — caller, token_in, token_out,
//     token_amount_in, token_amount_out
//     → emits a canonical.Trade
//   - "join_pool"  — caller, token_in, token_amount_in
//     → emits a LiquidityEvent (multi-token LP add;
//     one event per token, so an N-token join
//     produces N rows)
//   - "exit_pool"  — caller, token_out, token_amount_out
//     → emits a LiquidityEvent (multi-token LP remove)
//   - "deposit"    — caller, token_in, token_amount_in
//     → emits a LiquidityEvent (single-asset LP add)
//   - "withdraw"   — caller, token_out, token_amount_out,
//     pool_amount_in
//     → emits a LiquidityEvent (single-asset LP remove;
//     pool_amount_in is the BPT-share count burned)
//
// What's **not** emitted by the Soroban port (despite being in
// Balancer-v1 on EVM):
//
//   - bind / rebind / unbind / finalize — these functions do not
//     exist in the Soroban port. The pool's token+weight set is
//     fixed at `init()` and there is no event published.
//   - set_swap_fee / set_public_swap — neither function exists.
//   - set_controller — exists, but does not publish an event in the
//     Soroban port (a contract upgrade that adds one would surface
//     as a new (POOL, set_controller) topic; the decoder rejects
//     it with ErrNotCometEvent until support is added).
//   - gulp — exists (absorbs tokens sent directly to the contract),
//     does not publish an event.
//
// BPT (Balancer Pool Token) transfers ARE emitted, but via the
// **SEP-41 standard token-event surface**, not the POOL namespace.
// They are claimed by the SEP-41 supply observer
// (internal/sources/sep41_supply) when the pool contract is in its
// registered scope; this package does not re-decode them.
//
// `POOL` is a shared topic namespace across every Comet pool contract
// — the decoder matches by topic bytes, not pool contract ID, same
// pattern as Soroswap/Aquarius/Phoenix. Operators who want narrow
// coverage (e.g. only Blend's backstop) filter downstream by
// `Trade.Source == "comet"` or `LiquidityEvent.ContractID`, not at
// dispatch time.
package comet

import (
	"errors"

	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// SourceName is the canonical string stamped on every event this
// package emits. Single source — Comet has no versioned variants.
const SourceName = "comet"

// cometTopicArity is the topic count on every Comet event:
// [Symbol("POOL"), Symbol("<event_name>")]. Anything other than 2 is
// a schema change we don't claim.
const cometTopicArity = 2

// LiquidityKind discriminates the four liquidity-mutating Comet
// events. String values are stamped onto LiquidityEvent.Kind and
// match the comet_liquidity.event_kind CHECK constraint
// (migration 0042).
type LiquidityKind string

// Liquidity kinds. `add` (join_pool / deposit) grows pool reserves
// for the named token; `remove` (exit_pool / withdraw) shrinks them.
// The four kinds map cleanly to two directions:
//
//	join_pool / deposit  → direction = "add"
//	exit_pool / withdraw → direction = "remove"
//
// We keep the per-kind distinction in the row so a follow-up
// reserve-tracker can tell "multi-token join (one event per token,
// must group by tx)" apart from "single-asset deposit (standalone)".
const (
	LiquidityJoinPool LiquidityKind = "join_pool"
	LiquidityExitPool LiquidityKind = "exit_pool"
	LiquidityDeposit  LiquidityKind = "deposit"
	LiquidityWithdraw LiquidityKind = "withdraw"
)

// IsValid reports whether k is one of the four known LiquidityKinds.
func (k LiquidityKind) IsValid() bool {
	switch k {
	case LiquidityJoinPool, LiquidityExitPool, LiquidityDeposit, LiquidityWithdraw:
		return true
	}
	return false
}

// Direction is the add/remove polarity for a liquidity event —
// `add` for join_pool/deposit (reserves grow), `remove` for
// exit_pool/withdraw (reserves shrink). Mirrors the
// comet_liquidity.direction CHECK constraint.
func (k LiquidityKind) Direction() string {
	switch k {
	case LiquidityJoinPool, LiquidityDeposit:
		return "add"
	case LiquidityExitPool, LiquidityWithdraw:
		return "remove"
	}
	return ""
}

// Event-topic constants. Matches against these are byte-equality
// via the pre-encoded TopicSymbol* blobs below.
const (
	EventTopic0   = "POOL"
	EventSwap     = "swap"
	EventJoinPool = "join_pool"
	EventExitPool = "exit_pool"
	EventDeposit  = "deposit"
	EventWithdraw = "withdraw"
)

// Pre-encoded base64 SCVal::Symbol blobs for topic[0] / topic[1].
// `symbol_short!("POOL")` and the per-event symbols in the Rust
// source — the Soroban-SDK Symbol marshaller matches
// scval.MustEncodeSymbol byte-for-byte (pinned by
// internal/scval/scval_test.go). All five event names are ≤ 9
// chars so they go through `symbol_short!` (compact wire form);
// the encoder picks the right form by length automatically.
var (
	TopicSymbolPool     = scval.MustEncodeSymbol(EventTopic0)
	TopicSymbolSwap     = scval.MustEncodeSymbol(EventSwap)
	TopicSymbolJoinPool = scval.MustEncodeSymbol(EventJoinPool)
	TopicSymbolExitPool = scval.MustEncodeSymbol(EventExitPool)
	TopicSymbolDeposit  = scval.MustEncodeSymbol(EventDeposit)
	TopicSymbolWithdraw = scval.MustEncodeSymbol(EventWithdraw)
)

// Errors returned by the decode path.
var (
	// ErrNotCometSwap — topic[0..1] doesn't match (POOL, swap).
	// Returned by the legacy `decodeSwap` entry point when invoked
	// against a non-swap event. New decoders should prefer
	// ErrNotCometEvent.
	ErrNotCometSwap = errors.New("comet: not a Comet POOL.swap event")

	// ErrNotCometEvent — topic[0..1] doesn't match any known Comet
	// (POOL, <kind>) tuple. Skip: another Comet variant added in a
	// future contract upgrade, or an unrelated contract entirely.
	// Operators see the rate via the dispatcher's
	// `ratesengine_source_orphan_events_total{source="comet"}`
	// counter; a sustained spike means a new event variant is in
	// the wild and decoder coverage is incomplete.
	ErrNotCometEvent = errors.New("comet: not a recognised Comet POOL event")

	// ErrMalformedPayload — body didn't decode to the expected
	// Map shape for the matched event kind.
	ErrMalformedPayload = errors.New("comet: malformed event payload")

	// ErrNonPositiveAmounts — token_amount_in or token_amount_out
	// is zero / negative. A valid swap always has positive amounts
	// on both sides; zero-amount is either a contract bug or an
	// edge case we'd rather skip+count than emit. Liquidity events
	// also reject non-positive amounts: a zero-amount join/exit
	// doesn't change pool state and shouldn't materialise as a row.
	ErrNonPositiveAmounts = errors.New("comet: amounts must be positive")
)
