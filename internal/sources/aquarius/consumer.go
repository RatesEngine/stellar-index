package aquarius

import (
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// TradeEvent is the [consumer.Event] Aquarius's Decoder emits on
// a successful decode. The indexer's event sink type-switches on
// this at its output channel.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "aquarius.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time check that TradeEvent satisfies consumer.Event.
var _ consumer.Event = TradeEvent{}
