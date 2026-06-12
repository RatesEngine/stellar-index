package phoenix

import (
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
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

// Matches implements [dispatcher.Decoder]. Phoenix emits its
// per-action events with topic[0] = String(<action>). The second
// topic slot carries the field name; the buffer routes it
// internally. Five actions are claimed:
//
//   - swap (volatile + stableswap pools)
//   - provide_liquidity / withdraw_liquidity (volatile +
//     stableswap pools)
//   - bond / unbond (per-pool stake contracts)
//
// Each action's per-field correlation is independent.
func (*Decoder) Matches(ev events.Event) bool {
	a, _ := classifyAny(&ev)
	return a != actionUnknown
}

// Decode implements [dispatcher.Decoder]. Routes to the per-action
// reassembly buffer. Returns one consumer.Event when an action's
// required field count is met; (nil, nil) for the per-field events
// still buffering. For withdraw_liquidity the optional 5th
// `auto unbonded` event is recognised but discarded (the bond
// contract emits its own unbond which carries the same data).
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	a, fieldTopic := classifyAny(&ev)
	if a == actionUnknown {
		// Matches() already vetted this; defensive skip.
		return nil, nil
	}

	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	switch a {
	case actionSwap:
		return d.decodeSwapEvent(&ev, fieldTopic, closedAt)
	case actionProvideLiquidity:
		return d.decodeProvideLiquidityEvent(&ev, fieldTopic, closedAt)
	case actionWithdrawLiquidity:
		return d.decodeWithdrawLiquidityEvent(&ev, fieldTopic, closedAt)
	case actionBond:
		return d.decodeStakeEvent(&ev, fieldTopic, closedAt, true)
	case actionUnbond:
		return d.decodeStakeEvent(&ev, fieldTopic, closedAt, false)
	}
	return nil, nil
}

// decodeSwapEvent / decodeProvideLiquidityEvent /
// decodeWithdrawLiquidityEvent / decodeStakeEvent are the per-action
// helpers extracted from Decode to keep the switch's cognitive
// complexity under the gocognit ceiling. All assume d.mu is held by
// the caller — Decode owns the lock for the duration of one event.

func (d *Decoder) decodeSwapEvent(ev *events.Event, fieldTopic string, closedAt time.Time) ([]consumer.Event, error) {
	completed, evicted, err := d.buf.absorb(ev, fieldTopic, closedAt)
	d.evictedOrphans += len(evicted)
	if err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, nil
	}
	trade, err := decodeSwap(completed)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{TradeEvent{Trade: trade}}, nil
}

func (d *Decoder) decodeProvideLiquidityEvent(ev *events.Event, fieldTopic string, closedAt time.Time) ([]consumer.Event, error) {
	completed, evicted, err := d.buf.absorbProvideLiquidity(ev, fieldTopic, closedAt)
	d.evictedOrphans += evicted
	if err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, nil
	}
	change, err := decodeProvideLiquidity(completed)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{LiquidityEvent{Change: change}}, nil
}

func (d *Decoder) decodeWithdrawLiquidityEvent(ev *events.Event, fieldTopic string, closedAt time.Time) ([]consumer.Event, error) {
	completed, evicted, err := d.buf.absorbWithdrawLiquidity(ev, fieldTopic, closedAt)
	d.evictedOrphans += evicted
	if err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, nil
	}
	change, err := decodeWithdrawLiquidity(completed)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{LiquidityEvent{Change: change}}, nil
}

func (d *Decoder) decodeStakeEvent(ev *events.Event, fieldTopic string, closedAt time.Time, isBond bool) ([]consumer.Event, error) {
	completed, evicted, err := d.buf.absorbStake(ev, fieldTopic, closedAt, isBond)
	d.evictedOrphans += evicted
	if err != nil {
		return nil, err
	}
	if completed == nil {
		return nil, nil
	}
	change, err := decodeStake(completed)
	if err != nil {
		return nil, err
	}
	return []consumer.Event{StakeEvent{Change: change}}, nil
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
