package comet

import (
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Comet. Single instance
// per indexer — Comet uses a shared ("POOL", <event_name>) topic
// namespace across every pool contract, so routing is by topic
// bytes rather than per-pool contract ID (same shape as
// Soroswap/Aquarius/Phoenix).
//
// No goroutines, no state, no polling. Claims any of the five
// Soroban-emitted POOL events: swap (→ TradeEvent), join_pool /
// exit_pool / deposit / withdraw (→ LiquidityEvent). Admin
// functions (set_controller, gulp, init) exist but do NOT publish
// events in the Soroban port; BPT transfers go through the SEP-41
// standard token-event surface (handled by sep41_supply when the
// pool is in scope), not the POOL namespace.
type Decoder struct{}

// NewDecoder constructs a stateless Comet Decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.Decoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Byte-equality on topic[0]
// + topic[1] — no SCVal parsing on the hot path. Claims any
// recognised Comet POOL event so the dispatcher routes them to
// Decode rather than counting them as orphans.
func (d *Decoder) Matches(ev events.Event) bool { return classify(&ev) != "" }

// Decode implements [dispatcher.Decoder]. Returns exactly one
// consumer.Event on success — TradeEvent for swap, LiquidityEvent
// for the other four kinds. A decode error is non-fatal per the
// dispatcher contract — counted by the source's orphan/malformed
// metrics and skipped.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := classify(&ev)
	if kind == "" {
		return nil, ErrNotCometEvent
	}

	closedAt, err := ev.EventClosedAt()
	if err != nil {
		// Comet events use ledger close time for the event timestamp
		// (unlike oracles, there's no contract-declared timestamp in
		// the body). Fail closed like the blend/phoenix/defindex
		// siblings rather than substituting time.Now() — during a
		// backfill replay that would stamp every event with the
		// wall-clock of the replay run, not the historical ledger.
		return nil, err
	}

	if kind == EventSwap {
		trade, err := decodeSwap(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{TradeEvent{Trade: trade}}, nil
	}

	// All other recognised kinds are liquidity events.
	liq, err := decodeLiquidityEvent(&ev, closedAt)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{liq}, nil
}
