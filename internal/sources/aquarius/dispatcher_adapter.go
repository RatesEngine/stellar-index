package aquarius

import (
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of the Aquarius AMM. Every
// Aquarius swap carries its asset identities in the event topics
// (see internal/sources/aquarius/decode.go + the contract-source
// citation there), so the Decoder needs no per-source state — it's
// a pure function from one events.Event to one canonical.Trade.
type Decoder struct{}

// NewDecoder constructs an Aquarius Decoder. Takes no arguments
// today; a future per-contract WASM-hash registry (per
// docs/architecture/contract-schema-evolution.md) might add a
// version selector but the core contract is fixed.
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Aquarius emits the
// `trade` event from every pool contract on the network — the
// dispatcher doesn't need a contract-address allowlist because
// the 4-topic shape plus the ADR-0010 / ADR-0014 asset-allow-list
// in the decoder already keep the output bounded.
func (*Decoder) Matches(ev events.Event) bool {
	return classify(&ev) == EventTrade
}

// Decode implements [dispatcher.Decoder]. Returns one TradeEvent
// per successful decode (Aquarius trades are always single-pair).
func (*Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}
	trade, err := decodeTrade(&ev, closedAt)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{TradeEvent{Trade: trade}}, nil
}
