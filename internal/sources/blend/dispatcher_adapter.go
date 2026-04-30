package blend

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Blend. Per
// docs/discovery/dexes-amms/blend.md: every Blend pool emits all
// its events under the same per-pool contract, so topic[0] is the
// only classifier we need. There's no per-source state.
//
// The pool-factory's `deploy` event (which announces new pool
// instances) is decoded by a separate factory adapter — kept apart
// from this Decoder because it has a different downstream
// consumer (pool registry, not the auction store). That landing
// happens in Task #45 (factory walk + audit).
type Decoder struct{}

// NewDecoder constructs a Blend Decoder. Stateless; takes no
// arguments. Future per-WASM-hash dispatch (per
// docs/architecture/contract-schema-evolution.md) would add a
// version selector but the auction event surface is currently
// covered by a single contract version (V2).
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Returns true for any
// auction-event topic[0] symbol — `new_auction`, `fill_auction`,
// `delete_auction`. Money-market / admin / credit-risk events are
// classified separately (their topic constants exist in events.go
// but aren't matched here yet — they land in follow-up PRs).
func (*Decoder) Matches(ev events.Event) bool {
	switch classify(&ev) {
	case EventNewAuction, EventFillAuction, EventDeleteAuction:
		return true
	default:
		return false
	}
}

// Decode implements [dispatcher.Decoder]. Returns one consumer.Event
// per successful decode. Body shape varies per event; the kind is
// preserved in the returned struct's [Event.EventKind] string so
// the sink can demultiplex.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}
	switch classify(&ev) {
	case EventNewAuction:
		out, err := decodeNewAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventFillAuction:
		out, err := decodeFillAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventDeleteAuction:
		out, err := decodeDeleteAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	default:
		return nil, fmt.Errorf("%w: topic[0]=%q", ErrNotBlendEvent, firstTopicHex(&ev))
	}
}

// firstTopicHex returns a short identifying string for the event's
// topic[0] when no decoder branch matched — used in error messages
// only. Empty topic / non-symbol topic fall through to a
// placeholder rather than failing the error format itself.
func firstTopicHex(e *events.Event) string {
	if len(e.Topic) == 0 {
		return "<empty>"
	}
	return e.Topic[0]
}
