package phoenix

import (
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// TradeEvent is the [consumer.Event] Phoenix's Decoder emits on a
// completed 8-field swap assembly.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "phoenix.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time check that TradeEvent satisfies consumer.Event.
var _ consumer.Event = TradeEvent{}

// ─── 8-field correlation buffer ─────────────────────────────────
// Phoenix emits one swap as 8 separate events (one per field).
// An entry sits in the buffer until all 8 slots are populated —
// a missing field (pagination race, contract bug, malformed pool)
// otherwise leaves it hanging forever. Age-based eviction bounds
// memory usage.

// defaultOrphanMaxAge caps how long an incomplete entry waits for
// missing fields. 5 minutes is generous — all 8 events should land
// within the same transaction, seconds apart on-chain.
const defaultOrphanMaxAge = 5 * time.Minute

type buffer struct {
	m      map[groupKey]*RawSwap
	maxAge time.Duration
	nowFn  func() time.Time
}

func newBuffer() *buffer {
	return &buffer{
		m:      map[groupKey]*RawSwap{},
		maxAge: defaultOrphanMaxAge,
		nowFn:  time.Now,
	}
}

// absorb stores one field-event in the appropriate RawSwap slot.
// Returns:
//   - completed: non-nil *RawSwap when all 8 slots are populated.
//   - evicted:   entries whose ClosedAt is older than maxAge.
//   - err:       ErrUnknownField / decode errors for the current event.
func (b *buffer) absorb(e *events.Event, fieldTopic string, closedAt time.Time) (completed *RawSwap, evicted []RawSwap, err error) {
	// Reference time for orphan eviction is the incoming event's
	// ClosedAt, not wall-clock — so backfill of historical events
	// correctly compares against the timeline being replayed.
	evicted = b.sweepStale(closedAt)

	k := keyOf(e)
	r, ok := b.m[k]
	if !ok {
		r = &RawSwap{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pool: e.ContractID, ClosedAt: closedAt,
		}
		b.m[k] = r
	}
	if err := r.assign(e, fieldTopic); err != nil {
		return nil, evicted, err
	}
	if r.Complete() {
		delete(b.m, k)
		return r, evicted, nil
	}
	return nil, evicted, nil
}

// sweepStale removes entries older than maxAge relative to `ref`,
// returning them as orphans. A zero `ref` falls back to nowFn()
// for drain-at-shutdown calls.
func (b *buffer) sweepStale(ref time.Time) []RawSwap {
	if b.maxAge <= 0 {
		return nil
	}
	if ref.IsZero() {
		ref = b.nowFn()
	}
	cutoff := ref.Add(-b.maxAge)
	var evicted []RawSwap
	for k, r := range b.m {
		if r.ClosedAt.Before(cutoff) {
			evicted = append(evicted, *r)
			delete(b.m, k)
		}
	}
	return evicted
}

// orphans returns incomplete entries. Called after a bounded-range
// ingest ends; incompletes indicate contract or pagination anomaly.
func (b *buffer) orphans() []RawSwap {
	out := make([]RawSwap, 0, len(b.m))
	for _, r := range b.m {
		out = append(out, *r)
	}
	return out
}

// size returns the in-flight entry count. Used by tests.
func (b *buffer) size() int { return len(b.m) }
