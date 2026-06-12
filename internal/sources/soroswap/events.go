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
	// MainnetFactory is the current/primary Soroswap factory — the only
	// one that has deployed pairs with swap activity.
	MainnetFactory = "CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2"
	MainnetRouter  = "CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH"

	// MainnetPairWASMHash lets us identify Soroswap pair contracts by
	// hashing their wasm rather than walking factory events.
	// Useful for backfill short-cuts.
	MainnetPairWASMHash = "18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e"
)

// MainnetFactories is the COMPLETE, empirically-verified set of Soroswap
// factories on mainnet (ADR-0035). Like Blend, Soroswap has more than one
// factory: the primary CA4HEQTL plus three early (launch-era) factories.
// Verified from the r1 lake (2026-06-12) by decoding every
// `SoroswapFactory:new_pair` event — the early factories created 21 pairs
// between them, but NONE of those pairs have any swap event (they're
// defunct launch-era pairs), so the primary-factory-only gate drops no
// real trades today. They are included so the `new_pair` gate honors every
// factory (directive: all factories + all factory-created contracts) and
// the reconcile self-seeds the early pairs; a future trade on one of them
// would then be captured rather than dropped. Re-run the enumeration if a
// new factory appears.
var MainnetFactories = []string{
	MainnetFactory,
	"CCIQM2O3YJQEKS7I77AS5IO3CU6UCBAUWHLWRBWVV336ZCSTKRNBKPHW", // early, 11 pairs
	"CDBRTEJMOUJQHFZCAW4JPXZ75HCRHZAQXG75ZMGQ2LMNXA5ID7RQIFSX", // early, 6 pairs
	"CCDATRT2EY6Y2KAZ7HM7BRZVZCB6RHL56PQUBWGBS2ML2JAK7VXFLCJY", // early, 4 pairs
}

// IsMainnetFactory reports whether contractID is one of the verified
// Soroswap factories — the trust-root set the new_pair gate consults.
func IsMainnetFactory(contractID string) bool {
	for _, f := range MainnetFactories {
		if f == contractID {
			return true
		}
	}
	return false
}

// Pre-encoded base64 SCVal blobs — byte-identical to what the
// contract emits on topic positions. Computed at package init via
// scval.MustEncodeString / MustEncodeSymbol. Used for byte-equality
// classification against dispatched events (classify()) — no SCVal
// parse on the hot path.
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

	// ErrSwapWithoutSync — a swap that didn't get its following
	// sync. The dispatcher feeds events in-order; the decoder's
	// swap+sync correlation buffer should pair them. Surfacing this
	// means a bug or a truncated event stream.
	ErrSwapWithoutSync = errors.New("soroswap: swap without sync")

	// ErrMalformedPayload — event fields don't match the expected
	// Soroswap schema (arity, types, contract).
	ErrMalformedPayload = errors.New("soroswap: malformed event payload")
)
