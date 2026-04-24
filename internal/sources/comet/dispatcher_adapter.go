package comet

import (
	"time"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Comet. Single instance
// per indexer — Comet uses a shared ("POOL", <event_name>) topic
// namespace across every pool contract, so routing is by topic
// bytes rather than per-pool contract ID (same shape as
// Soroswap/Aquarius/Phoenix).
//
// No goroutines, no state, no polling. v1 decodes only (POOL,
// swap); join/exit/deposit/withdraw would be additional Matches
// predicates alongside when reserve tracking lands.
type Decoder struct{}

// NewDecoder constructs a stateless Comet Decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.Decoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Byte-equality on topic[0]
// + topic[1] — no SCVal parsing on the hot path.
func (d *Decoder) Matches(ev events.Event) bool { return classifySwap(&ev) }

// Decode implements [dispatcher.Decoder]. Returns exactly one
// TradeEvent on success, wrapping a canonical.Trade.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		// Swap events use ledger close time for the trade timestamp
		// (unlike oracles, there's no contract-declared timestamp in
		// the body). Fall back to now() rather than dropping the
		// entire trade.
		closedAt = time.Now().UTC()
	}
	trade, err := decodeSwap(&ev, closedAt)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{TradeEvent{Trade: trade}}, nil
}
