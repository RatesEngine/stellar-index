package soroswap

import (
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── Event envelope ─────────────────────────────────────────────

// TradeEvent is the [consumer.Event] shape Soroswap's Decoder
// emits on a completed swap+sync pair. The indexer's event-sink
// type-switches on this at its output channel.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "soroswap.trade" }

// Source implements [consumer.Event] — matches [SourceName].
func (TradeEvent) Source() string { return SourceName }

// Compile-time check that TradeEvent satisfies consumer.Event.
var _ consumer.Event = TradeEvent{}

// ─── Correlation buffer ─────────────────────────────────────────
// Groups swap + sync by (ledger, tx_hash, op_index). Emits complete
// pairs back to the caller; holds incompletes until either their
// partner event arrives, or their ClosedAt is older than maxAge
// (at which point they're returned as orphans and dropped).
//
// Bounded memory: without age-based eviction, the buffer would
// grow unbounded whenever a swap arrives without its matching sync
// (page-boundary races, malformed pair contracts, etc.).

// defaultOrphanMaxAge is how long the buffer holds an incomplete
// entry waiting for its partner before evicting as an orphan.
//
// Soroswap swap+sync are emitted in the same transaction — they
// SHOULD always arrive within seconds of each other in the
// dispatcher's in-order stream. Five minutes is a generous ceiling
// that tolerates worst-case pagination lag (from an RPC-based
// fixture-capture replay) without holding references to events
// that will never resolve.
const defaultOrphanMaxAge = 5 * time.Minute

type buffer struct {
	m      map[groupKey]*RawPair
	maxAge time.Duration
	nowFn  func() time.Time
}

func newBuffer() *buffer {
	return &buffer{
		m:      map[groupKey]*RawPair{},
		maxAge: defaultOrphanMaxAge,
		nowFn:  time.Now,
	}
}

// absorb records an event; returns any pairs that just completed.
// Also sweeps the buffer for entries older than maxAge; evicted
// orphans are RETURNED so the caller can emit metrics — they're
// NOT returned as completed pairs (they have no Sync to finalise).
func (b *buffer) absorb(e *events.Event, kind string, closedAt time.Time) (completed []RawPair, evicted []RawPair) {
	// Evict stale orphans first — keeps the map bounded in size
	// regardless of how long the process runs. The reference time
	// is the incoming event's ClosedAt, not wall-clock — so
	// backfill of historical events correctly compares against the
	// timeline being replayed.
	evicted = b.sweepStale(closedAt)

	k := keyOf(e)
	p, ok := b.m[k]
	if !ok {
		p = &RawPair{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pair: e.ContractID, ClosedAt: closedAt,
		}
		b.m[k] = p
	}
	switch kind {
	case EventSwap:
		p.Swap = e
	case EventSync:
		p.Sync = e
	}
	if p.Complete() {
		delete(b.m, k)
		completed = []RawPair{*p}
	}
	return completed, evicted
}

// sweepStale removes every entry whose ClosedAt is older than
// maxAge relative to `ref`, returning them as orphans.
//
// `ref` is normally the most-recent event's ClosedAt. Never use
// nowFn() during backfill: historical events have ClosedAt far
// behind wall-clock, and every entry would evict on the next
// absorb before its partner arrived.
func (b *buffer) sweepStale(ref time.Time) []RawPair {
	if b.maxAge <= 0 {
		return nil
	}
	if ref.IsZero() {
		ref = b.nowFn()
	}
	cutoff := ref.Add(-b.maxAge)
	var evicted []RawPair
	for k, p := range b.m {
		if p.ClosedAt.Before(cutoff) {
			evicted = append(evicted, *p)
			delete(b.m, k)
		}
	}
	return evicted
}

// orphans returns every incomplete entry; called after a bounded-
// range ingest finishes so metrics can attribute the leak. Does
// not mutate the buffer.
func (b *buffer) orphans() []RawPair {
	out := make([]RawPair, 0, len(b.m))
	for _, p := range b.m {
		out = append(out, *p)
	}
	return out
}

// size returns the number of in-flight (incomplete) pairs.
func (b *buffer) size() int { return len(b.m) }
