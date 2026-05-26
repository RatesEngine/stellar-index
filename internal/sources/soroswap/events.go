// Package soroswap ingests trade events from the Soroswap Soroban DEX.
//
// Design reference: internal/sources/soroswap/README.md and
// docs/discovery/dexes-amms/soroswap.md. See especially the Q1–Q4
// quirk notes in the README before modifying the correlation logic.
package soroswap

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// Source name constant — appears in metrics labels, canonical.Trade.Source,
// and config.IngestionConfig.EnabledSources. Must be stable.
const SourceName = "soroswap"

// Event names — topic[1] of every Soroswap pair/factory event is a
// Symbol SCVal with one of these literal values. (topic[0] is the
// contract-prefix String, see EventPrefix* below.)
//
// Verified 2026-04-23 against soroswap-core/contracts/pair/src/event.rs
// + contracts/factory/src/event.rs — each e.events().publish takes a
// 2-tuple `(prefix_literal, symbol_short!(event_name))`. The prefix
// serializes as ScvString; the event-name as ScvSymbol.
const (
	EventSwap     = "swap"
	EventSync     = "sync"
	EventDeposit  = "deposit"
	EventWithdraw = "withdraw"
	EventSkim     = "skim"

	// Emitted by the factory contract.
	EventNewPair = "new_pair"
)

// Topic-prefix string values (topic[0]). Soroswap uses String-typed
// SCVals for the contract-prefix slot, NOT Symbol — because the Rust
// contracts write `("SoroswapPair", symbol_short!("swap"))` where the
// first element is a string literal (→ ScvString on-wire).
const (
	PrefixPair    = "SoroswapPair"
	PrefixFactory = "SoroswapFactory"
	PrefixRouter  = "SoroswapRouter"
)

// Mainnet contract addresses — verified during Phase-1 audit against
// public/mainnet.contracts.json in soroswap-core.
const (
	MainnetFactory = "CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2"
	MainnetRouter  = "CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH"

	// MainnetPairWASMHash lets us identify Soroswap pair contracts by
	// hashing their wasm rather than walking factory events.
	// Useful for backfill short-cuts.
	MainnetPairWASMHash = "18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e"
)

// Pre-encoded base64 SCVal blobs — byte-identical to what the
// contract emits on topic positions. Computed at package init via
// scval.MustEncodeString / MustEncodeSymbol. Used both for:
//   - Byte-equality classification against live events (classify()).
//   - The EventFilter.Topics slice passed to stellar-rpc, so the
//     server drops non-matching events before streaming to us.
//
// Golden wire-format regression lives in internal/scval/scval_test.go
// (TestGolden_symbolBytes). If the SDK encoder shifts, that test
// fires before this package ships.
var (
	TopicPrefixPair    = scval.MustEncodeString(PrefixPair)    // topic[0] for pair events
	TopicPrefixFactory = scval.MustEncodeString(PrefixFactory) // topic[0] for factory events

	TopicSymbolSwap     = scval.MustEncodeSymbol(EventSwap)     // topic[1]
	TopicSymbolSync     = scval.MustEncodeSymbol(EventSync)     // topic[1]
	TopicSymbolDeposit  = scval.MustEncodeSymbol(EventDeposit)  // topic[1]
	TopicSymbolWithdraw = scval.MustEncodeSymbol(EventWithdraw) // topic[1]
	TopicSymbolSkim     = scval.MustEncodeSymbol(EventSkim)     // topic[1]
	TopicSymbolNewPair  = scval.MustEncodeSymbol(EventNewPair)  // topic[1] on factory
)

// Errors returned by the decode path. Callers classify via
// errors.Is.
var (
	// ErrUnknownEvent — topic[1] didn't match any of the event
	// names we care about. Most events fall into this class
	// (trades/sync we care about; others we ignore).
	ErrUnknownEvent = errors.New("soroswap: unknown event topic")

	// ErrOrphanSync — a sync event with no preceding swap in the
	// same (ledger, tx_hash, op_index). Not a trade; drop.
	ErrOrphanSync = errors.New("soroswap: orphan sync (no matching swap)")

	// ErrSwapWithoutSync — a swap that didn't get its following
	// sync. Could happen if the sync is in a later RPC page; the
	// consumer's buffer should have caught this. Bug or truncation.
	ErrSwapWithoutSync = errors.New("soroswap: swap without sync")

	// ErrMalformedPayload — event fields don't match the expected
	// Soroswap schema (arity, types, contract).
	ErrMalformedPayload = errors.New("soroswap: malformed event payload")
)
