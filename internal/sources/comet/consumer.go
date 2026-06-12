package comet

import (
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// TradeEvent is the [consumer.Event] Comet's Decoder emits on a
// successful (POOL, swap) decode. The indexer's event sink type-
// switches on this and calls store.InsertTrade — same shape as
// soroswap.TradeEvent / aquarius.TradeEvent / phoenix.TradeEvent.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "comet.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// LiquidityEvent is the [consumer.Event] Comet's Decoder emits on a
// successful (POOL, join_pool | exit_pool | deposit | withdraw)
// decode. One row per emitted event — a multi-token join_pool / exit_pool
// produces one LiquidityEvent per participating token (Comet emits one
// event per token in the loop, as documented in pool.rs).
//
// The indexer's event sink type-switches on this and calls
// store.InsertCometLiquidity to land it in the `comet_liquidity`
// hypertable (migration 0042). PoolAmountIn is populated only for
// withdraw events (the count of BPT-share tokens burned for the
// underlying withdrawn); the writer translates a zero / unset
// Amount to SQL NULL on the other three kinds.
type LiquidityEvent struct {
	ContractID string
	Ledger     uint32
	TxHash     string
	OpIndex    uint32
	// EventIndex is the contract event's index within its operation —
	// the per-event discriminator added to the comet_liquidity PK by
	// migration 0059 (F-1324). The swap path already fans op_index via
	// canonical.FanoutOpIndex; the liquidity path keys on event_index
	// directly so two same-(kind,token) events in one op don't collide.
	EventIndex   uint32
	ObservedAt   time.Time
	Kind         LiquidityKind
	Caller       string
	Token        string
	Amount       canonical.Amount
	PoolAmountIn canonical.Amount // withdraw-only; zero on join/exit/deposit
}

// EventKind implements [consumer.Event].
func (LiquidityEvent) EventKind() string { return "comet.liquidity" }

// Source implements [consumer.Event].
func (LiquidityEvent) Source() string { return SourceName }

// Compile-time checks.
var (
	_ consumer.Event = TradeEvent{}
	_ consumer.Event = LiquidityEvent{}
)
