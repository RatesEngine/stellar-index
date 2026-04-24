// Package redstone decodes on-chain events from the RedStone
// Adapter contract (one contract that owns price storage for every
// feed + thin per-feed proxies that delegate reads).
//
// Wire shape, verified 2026-04-23 against the public adapter source
// (.discovery-repos/redstone-public-contracts/packages/
// stellar-connector/deployments/stellarMultiFeed/contracts/
// redstone-adapter/src/event.rs):
//
//	topic[0] = Symbol("REDSTONE")
//	body     = Map {
//	              "updater":       Address,
//	              "updated_feeds": Vec<PriceData>,
//	           }
//	PriceData = Map {
//	              "price":             U256,
//	              "package_timestamp": u64,
//	              "write_timestamp":   u64,
//	           }
//
// The event carries prices + timestamps but NOT feed identifiers.
// Feed IDs live in the InvokeContract op args — the relayer calls
// `adapter.write_prices(updater, feed_ids: Vec<String>, payload)`.
// Our dispatcher surfaces those args via events.Event.OpArgs; the
// decoder zips `feed_ids` against `updated_feeds` one-to-one.
//
// Caveat: when the adapter's freshness verifier rejects a feed, it
// skips that entry in `updated_feeds` without skipping in
// `feed_ids`. We guard against this with a strict length check and
// surface ErrFeedIDCountMismatch if they disagree — a rare on-chain
// state we'd rather skip than attribute prices to the wrong assets.
// See docs/discovery/oracles/redstone.md for the full analysis.
package redstone

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName is the canonical string stamped on every OracleUpdate
// this package emits. Single source — unlike Reflector (3 variants),
// Redstone has one adapter contract covering all feeds.
const SourceName = "redstone"

// DefaultDecimals is the RedStone-wide price scale
// (adapter/config.rs:1 — `pub const DECIMALS: u64 = 8`). Every feed
// publishes at 8 decimals regardless of the underlying asset class.
const DefaultDecimals uint8 = 8

// DefaultResolutionSeconds reflects the on-chain update cadence:
// `0.2% deviation OR 24h heartbeat`. Emitted as the
// `ratesengine_oracle_resolution_seconds` gauge so the oracle-stale
// alert has a threshold. Set to 24h (the lower bound on assumed
// freshness — per docs/discovery/oracles/redstone.md a feed may go
// quiet for up to 24h if no price movement exceeds the 0.2%
// deviation threshold).
const DefaultResolutionSeconds = 24 * 60 * 60

// WriteFnName is the adapter contract's update entry point. The
// decoder only trusts OpArgs from calls to this function — anything
// else and it treats the args as unrelated (e.g. a composed tx that
// also calls a different Redstone method).
const WriteFnName = "write_prices"

// Event-topic constants.
const (
	EventTopic0 = "REDSTONE"
)

// TopicSymbolRedstone is the pre-encoded base64 SCVal::Symbol blob
// for topic[0]. Produced at init via scval.MustEncodeSymbol and
// used for byte-equality matching against Event.Topic entries.
var TopicSymbolRedstone = scval.MustEncodeSymbol(EventTopic0)

// Errors returned by the decode path.
var (
	// ErrNotRedstoneEvent — topic[0] doesn't match "REDSTONE".
	// Skip: this decoder owns only one topic.
	ErrNotRedstoneEvent = errors.New("redstone: not a REDSTONE event")

	// ErrMalformedPayload — event body doesn't decode to the
	// expected WritePrices map shape.
	ErrMalformedPayload = errors.New("redstone: malformed event payload")

	// ErrEmptyUpdates — the updated_feeds vector was empty. The
	// adapter only emits an event when at least one feed passes
	// the freshness check, so an empty vec is anomalous. Surface
	// loudly; caller decides whether to skip or alert.
	ErrEmptyUpdates = errors.New("redstone: empty updated_feeds vector")

	// ErrMissingOpArgs — the event arrived without InvokeContract
	// args attached. Either the producing tx wasn't an
	// InvokeContract (unexpected — write_prices is the only emit
	// path), or the dispatcher failed to populate them. Without
	// args we have no feed IDs to zip.
	ErrMissingOpArgs = errors.New("redstone: InvokeContract args unavailable")

	// ErrWrongFunctionCall — the InvokeContract call targeted a
	// function other than write_prices. Guard against decoding an
	// unrelated composed call's args as feed IDs.
	ErrWrongFunctionCall = errors.New("redstone: event not produced by a write_prices call")

	// ErrFeedIDCountMismatch — len(feed_ids from args) != len(
	// updated_feeds from event body). Happens when the adapter's
	// freshness verifier rejects one or more submitted feeds; in
	// that case we can't safely attribute the remaining prices to
	// specific feeds. Skip the whole event rather than risk
	// assigning a BTC price to ETH.
	ErrFeedIDCountMismatch = errors.New("redstone: feed_ids arity doesn't match updated_feeds; cannot safely zip")

	// ErrUnknownFeedID — a feed ID from the op args isn't on our
	// known-feeds allow-list. The 19 mainnet feeds are enumerated
	// in docs/discovery/oracles/redstone.md; extending the list is
	// a one-line change. Per-entry skip (other feeds in the same
	// event still land), same pattern as Reflector's
	// ErrUnknownSymbol.
	ErrUnknownFeedID = errors.New("redstone: feed_id not in known-feeds allow-list")
)
