package phoenix

import (
	"sync"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Phoenix. Unlike
// Reflector or Aquarius, Phoenix is stateful: one swap produces 8
// separate events that must be correlated by (ledger, tx_hash,
// op_index) before a canonical.Trade can be emitted. The Decoder
// owns the correlation buffer.
//
// Serial-call assumption: per docs/architecture/ingest-pipeline.md
// the dispatcher processes events in order. Decode is not
// re-entrant. The mutex below is belt-and-braces for the rare case
// an operator runs parallel ledger replay (not a current feature
// but cheap insurance).
type Decoder struct {
	mu  sync.Mutex
	buf *buffer

	// evictedOrphans is incremented every time the buffer drops an
	// incomplete RawSwap (aged past defaultOrphanMaxAge). The
	// dispatcher reads this via the optional `EvictedOrphans() int`
	// interface (see internal/dispatcher/dispatcher.go::Stats) and
	// the indexer reports the running counts as
	// obs.SourceOrphanEventsTotal in the per-ledger stats path
	// (internal/pipeline/processor.go).
	evictedOrphans int
}

// NewDecoder constructs a Phoenix Decoder with a fresh buffer.
func NewDecoder() *Decoder {
	return &Decoder{buf: newBuffer()}
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Phoenix emits its swap
// events from every pool contract on the network with
// topic[0] = String("swap"). The second topic slot carries the
// field name; the buffer routes it internally.
func (*Decoder) Matches(ev events.Event) bool {
	_, ok := classify(&ev)
	return ok
}

// Decode implements [dispatcher.Decoder]. Buffers the field-event
// until all 8 slots are populated, then emits one TradeEvent. For
// the 7 intermediate events Decode returns (nil, nil) — zero
// outputs, no error.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	fieldTopic, ok := classify(&ev)
	if !ok {
		// Matches() already vetted this; defensive skip.
		return nil, nil
	}

	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	completed, evicted, err := d.buf.absorb(&ev, fieldTopic, closedAt)
	d.evictedOrphans += len(evicted)
	d.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, nil // still buffering
	}

	trade, err := decodeSwap(completed)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{TradeEvent{Trade: trade}}, nil
}

// EvictedOrphans is the count of incomplete RawSwaps dropped by
// buffer age-out since this Decoder was constructed. Production
// callers will read this via obs.SourceOrphanEventsTotal once the
// indexer binary is rewritten in PR 165d.
func (d *Decoder) EvictedOrphans() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.evictedOrphans
}
