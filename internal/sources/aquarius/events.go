// Package aquarius ingests trade events from Aquarius's Soroban AMM
// (volatile + stableswap + concentrated pool types).
//
// Design reference: internal/sources/aquarius/README.md and
// docs/discovery/dexes-amms/aquarius.md. Read the quirks Q1–Q4
// before modifying the decoder.
package aquarius

import "errors"

// SourceName constant — appears in metrics labels,
// canonical.Trade.Source, and config.IngestionConfig.EnabledSources.
// Stable.
const SourceName = "aquarius"

// Event names — topic[0] of every Aquarius event.
const (
	EventTrade            = "trade"
	EventDepositLiquidity = "deposit_liquidity"
	EventWithdrawLiquidity = "withdraw_liquidity"
	EventUpdateReserves   = "update_reserves"
	EventReservesSync     = "reserves_sync" // older pool variant
)

// Mainnet contract addresses — verified during Phase-1 audit against
// stellar.expert + Aquarius docs.
const (
	MainnetRouter = "CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK"
	// XLM SAC (network-wide, not Aquarius-specific, but Aquarius
	// docs + internal addressing use it).
	MainnetXLMSAC = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
)

// PoolType classifies the Aquarius pool emitting an event. Decoded
// from pool metadata via a router read, cached on the Source.
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

// Pre-encoded base64 SCVal::Symbol blobs. Same placeholder pattern
// as Soroswap — real values land with the XDR codec.
// Uniqueness enforced by the Go compiler at switch-on time.
const (
	TopicSymbolTrade              = "PLACEHOLDER_AQUARIUS_TOPIC_TRADE"
	TopicSymbolDepositLiquidity   = "PLACEHOLDER_AQUARIUS_TOPIC_DEPOSIT_LIQUIDITY"
	TopicSymbolWithdrawLiquidity  = "PLACEHOLDER_AQUARIUS_TOPIC_WITHDRAW_LIQUIDITY"
	TopicSymbolUpdateReserves     = "PLACEHOLDER_AQUARIUS_TOPIC_UPDATE_RESERVES"
	TopicSymbolReservesSync       = "PLACEHOLDER_AQUARIUS_TOPIC_RESERVES_SYNC"
)

// Errors returned by the decode path.
var (
	ErrUnknownEvent      = errors.New("aquarius: unknown event topic")
	ErrMalformedPayload  = errors.New("aquarius: malformed event payload")
	ErrUnknownPool       = errors.New("aquarius: pool not in token cache (router read pending)")
	ErrConcentratedWIP   = errors.New("aquarius: concentrated-liquidity pools not decoded yet (Phase-1 WIP)")
)
