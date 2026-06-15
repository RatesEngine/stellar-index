package blend_backstop

import (
	"fmt"

	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/events"
)

// decodeOne classifies and decodes a single backstop event into the
// canonical [Event] row. It is the join point between decode.go's
// per-kind helpers and the universal identity fields carried on
// events.Event. Returns ("", err) only for a genuinely malformed
// event (the dispatcher counts + skips it); a promoted field whose
// shape doesn't match degrades into Attributes inside the per-kind
// helper rather than erroring here.
func decodeOne(ev *events.Event) (Event, error) { //nolint:gocyclo // dispatch table; one arm per backstop event kind
	kind := Classify(ev)
	if kind == "" {
		return Event{}, fmt.Errorf("%w: %q", ErrUnknownEvent, topic0(ev))
	}

	observedAt, err := ev.EventClosedAt()
	if err != nil {
		return Event{}, fmt.Errorf("blend_backstop: %s: %w", kind, err)
	}

	var (
		d    decoded
		derr error
	)
	switch kind {
	case EventDeposit:
		d, derr = decodeDeposit(ev)
	case EventClaim:
		d, derr = decodeClaim(ev)
	case EventDonate:
		d, derr = decodeDonate(ev)
	case EventQueueWithdrawal:
		d, derr = decodeQueueWithdrawal(ev)
	case EventWithdraw:
		d, derr = decodeWithdraw(ev)
	case EventDistribute:
		d, derr = decodeDistribute(ev)
	case EventGulpEmissions:
		d, derr = decodeGulpEmissions(ev)
	case EventDequeueWithdrawal:
		d, derr = decodeDequeueWithdrawal(ev)
	case EventDraw:
		d, derr = decodeDraw(ev)
	case EventRwZoneAdd:
		d, derr = decodeRwZoneAdd(ev)
	default:
		// Unreachable while Classify and this switch stay in lockstep.
		return Event{}, fmt.Errorf("%w: %s", ErrUnknownEvent, kind)
	}
	if derr != nil {
		return Event{}, derr
	}

	attrs := d.Attributes
	if attrs == nil {
		attrs = map[string]any{}
	}
	return Event{
		ContractID:  ev.ContractID,
		Ledger:      ev.Ledger,
		TxHash:      ev.TxHash,
		OpIndex:     ev.OperationIndex,
		EventIndex:  ev.EventIndex,
		ObservedAt:  observedAt,
		EventType:   kind,
		Pool:        d.Pool,
		UserAddress: d.UserAddress,
		Amount:      d.Amount,
		Amount2:     d.Amount2,
		Attributes:  attrs,
	}, nil
}

// topic0 returns the base64 topic[0] for error context, or "" when
// the event has no topics.
func topic0(ev *events.Event) string {
	if len(ev.Topic) == 0 {
		return ""
	}
	return ev.Topic[0]
}

// project wraps decodeOne and adapts to the dispatcher's
// []consumer.Event return shape — exactly one Event per recognised
// backstop event.
func project(ev *events.Event) ([]consumer.Event, error) {
	out, err := decodeOne(ev)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{out}, nil
}
