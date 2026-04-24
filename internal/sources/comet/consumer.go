package comet

import (
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// TradeEvent is the [consumer.Event] Comet's Decoder emits on a
// successful swap decode. The indexer's event sink type-switches on
// this and calls store.InsertTrade — same shape as
// soroswap.TradeEvent / aquarius.TradeEvent / phoenix.TradeEvent.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "comet.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time check.
var _ consumer.Event = TradeEvent{}
