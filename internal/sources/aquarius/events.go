// Package aquarius ingests trade events from Aquarius's Soroban AMM
// (volatile + stableswap + concentrated pool types).
//
// Design reference: internal/sources/aquarius/README.md and
// docs/discovery/dexes-amms/aquarius.md. Read the quirks Q1–Q4
// before modifying the decoder.
package aquarius

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName constant — appears in metrics labels,
// canonical.Trade.Source, and config.IngestionConfig.EnabledSources.
// Stable.
const SourceName = "aquarius"

// Event names — topic[0] of every Aquarius event, as a Symbol SCVal.
//
// Verified 2026-04-23 against
// aquarius-amm/liquidity_pool_events/src/lib.rs — every
// `e.events().publish(...)` call uses a tuple whose first element
// is a `Symbol::new(e, "<name>")`, which serializes as ScvSymbol
// on-wire.
const (
	EventTrade             = "trade"
	EventDepositLiquidity  = "deposit_liquidity"
	EventWithdrawLiquidity = "withdraw_liquidity"
	EventUpdateReserves    = "update_reserves"
)

// Mainnet contract addresses — verified during Phase-1 audit against
// stellar.expert + Aquarius docs.
const (
	MainnetRouter = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK"
	// XLM SAC (network-wide, not Aquarius-specific, but Aquarius
	// docs + internal addressing use it).
	MainnetXLMSAC = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
)

// PoolType classifies the Aquarius pool emitting an event. Currently
// only used by operators inspecting pool metadata — the trade
// decoder itself doesn't branch on pool type because the `trade`
// event already carries the sold/bought token addresses in its
// topics, so every trade decodes the same way regardless of whether
// the pool is 2-token volatile, 3-token stableswap, or 4-token
// stableswap. See discovery doc for the matrix of pool types.
type PoolType uint8

const (
	PoolUnknown      PoolType = 0
	PoolVolatile     PoolType = 1 // x*y=k
	PoolStableswap   PoolType = 2 // Curve-style invariant (N assets)
	PoolConcentrated PoolType = 3 // v3-style; WIP at Phase-1 audit
)

func (p PoolType) String() string {
	switch p {
	case PoolVolatile:
		return "volatile"
	case PoolStableswap:
		return "stableswap"
	case PoolConcentrated:
		return "concentrated"
	default:
		return "unknown"
	}
}

// Pre-encoded base64 SCVal::Symbol blobs, computed at init via
// scval.MustEncodeSymbol. Used for byte-equality classification
// and for the server-side EventFilter.Topics subscription.
//
// Uniqueness holds because each maps from a distinct EventTrade/
// EventDepositLiquidity/... string constant above; a duplicated
// SCVal would trace back to a duplicated source string.
var (
	TopicSymbolTrade             = scval.MustEncodeSymbol(EventTrade)
	TopicSymbolDepositLiquidity  = scval.MustEncodeSymbol(EventDepositLiquidity)
	TopicSymbolWithdrawLiquidity = scval.MustEncodeSymbol(EventWithdrawLiquidity)
	TopicSymbolUpdateReserves    = scval.MustEncodeSymbol(EventUpdateReserves)
)

// Errors returned by the decode path.
var (
	ErrUnknownEvent     = errors.New("aquarius: unknown event topic")
	ErrMalformedPayload = errors.New("aquarius: malformed event payload")
	// ErrConcentratedWIP is reserved for concentrated-pool trade
	// events, which use a different body schema. Current mainnet
	// has no concentrated pools live (feature-branch WIP at
	// Phase-1 audit); if we encounter one we'll surface this error
	// and skip until the dedicated decoder lands.
	ErrConcentratedWIP = errors.New("aquarius: concentrated-liquidity pools not decoded yet (Phase-1 WIP)")
)
