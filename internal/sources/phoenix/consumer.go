package phoenix

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// Source implements [consumer.Source] for Phoenix swaps.
//
// Uses an 8-event correlation buffer (Q1). Events for one swap
// arrive across one or more RPC pages; the buffer persists across
// page boundaries until all 8 fields land or the source shuts down.
type Source struct {
	rpc *stellarrpc.Client

	pollInterval time.Duration

	mu     sync.RWMutex
	health consumer.HealthStatus
}

// New constructs a Phoenix source.
func New(rpc *stellarrpc.Client, opts ...Option) *Source {
	s := &Source{
		rpc:          rpc,
		pollInterval: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Option configures a [Source] at construction time.
type Option func(*Source)

// WithPollInterval overrides the default 2s live-stream poll.
func WithPollInterval(d time.Duration) Option {
	return func(s *Source) { s.pollInterval = d }
}

// Name implements [consumer.Source].
func (s *Source) Name() string { return SourceName }

// Health implements [consumer.Source].
func (s *Source) Health() consumer.HealthStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.health
}

// BackfillRange implements [consumer.Source].
func (s *Source) BackfillRange(ctx context.Context, from, to uint32, out chan<- consumer.Event) error {
	cursor := ""
	buf := newBuffer()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := s.rpc.GetEvents(ctx, from, to, s.filters(), &stellarrpc.Pagination{
			Cursor: cursor, Limit: 200,
		})
		if err != nil {
			s.setError(err)
			return fmt.Errorf("phoenix backfill getEvents: %w", err)
		}
		s.setOK()

		if err := s.processPage(ctx, resp.Events, buf, out); err != nil {
			return err
		}

		if resp.Cursor == "" || len(resp.Events) == 0 {
			break
		}
		cursor = resp.Cursor
	}

	for _, orphan := range buf.orphans() {
		_ = orphan // TODO(#0): metric counter phoenix_orphan_swaps_total{fields=N}
	}
	return nil
}

// StreamLive implements [consumer.Source]. First-poll bootstrap:
// see reflector.StreamLive for why startLedger MUST be a concrete
// ledger (we seed from the tip) instead of the 0 that stellar-rpc
// would reject.
func (s *Source) StreamLive(ctx context.Context, out chan<- consumer.Event) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	var cursor string
	buf := newBuffer()
	startLedger, err := s.rpc.LatestLedgerSequence(ctx)
	if err != nil {
		return fmt.Errorf("phoenix seed tip: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		resp, err := s.rpc.GetEvents(ctx, startLedger, 0, s.filters(), &stellarrpc.Pagination{
			Cursor: cursor, Limit: 200,
		})
		if err != nil {
			s.setError(err)
			continue
		}
		s.setOK()

		if err := s.processPage(ctx, resp.Events, buf, out); err != nil {
			s.setError(err)
			continue
		}

		if resp.Cursor != "" {
			cursor = resp.Cursor
		}
		if resp.LatestLedger > 0 {
			startLedger = resp.LatestLedger
			s.mu.Lock()
			if s.health.LastLedger > 0 && resp.LatestLedger > s.health.LastLedger {
				s.health.LagLedgers = resp.LatestLedger - s.health.LastLedger
			} else {
				s.health.LagLedgers = 0
			}
			s.mu.Unlock()
		}
	}
}

// processPage is shared between backfill + live.
func (s *Source) processPage(ctx context.Context, events []stellarrpc.Event, buf *buffer, out chan<- consumer.Event) error {
	for i := range events {
		e := &events[i]
		fieldTopic, isSwap := classify(e)
		if !isSwap {
			continue
		}

		completed, evicted, err := buf.absorb(e, fieldTopic)
		if len(evicted) > 0 {
			// Stale incompletes aged out of the buffer — one "decode"
			// that didn't produce a trade, per evicted entry.
			for range evicted {
				obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			}
		}
		if err != nil {
			// Unknown field — event didn't match any of the 8 slots.
			obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			continue
		}
		if completed == nil {
			continue
		}

		trade, err := decodeSwap(completed)
		if err != nil {
			// Per-event decode failures don't bubble up — bad data
			// shouldn't kill the stream. Counted so sustained rates
			// trigger alerts.
			obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			continue
		}
		s.mu.Lock()
		s.health.LastEvent = trade.Timestamp
		if trade.Ledger > s.health.LastLedger {
			s.health.LastLedger = trade.Ledger
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- TradeEvent{Trade: trade}:
		}
	}
	return nil
}

func (s *Source) filters() []stellarrpc.EventFilter {
	return []stellarrpc.EventFilter{{Type: "contract"}}
}

// ─── Health mutators ─────────────────────────────────────────────

func (s *Source) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.Connected = false
	s.health.LastError = err
}

func (s *Source) setOK() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health.Connected = true
	s.health.LastError = nil
}

// ─── Event envelope ─────────────────────────────────────────────

// TradeEvent is the [consumer.Event] this source emits.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "phoenix.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// ─── 8-field correlation buffer ─────────────────────────────────
//
// Phoenix emits one swap as 8 separate events (one per field, per
// README.md). An entry sits in the buffer until all 8 slots are
// populated — but a missing field (pagination race, contract bug,
// malformed pool) leaves it hanging forever. Age-based eviction
// bounds the memory usage so StreamLive doesn't leak.

// defaultOrphanMaxAge caps how long an incomplete entry waits for
// its missing fields. 5 minutes is generous — all 8 events should
// land within a single transaction and therefore within one or two
// RPC pages.
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
//   - evicted:   entries whose ClosedAt is older than maxAge — the
//     caller emits orphan metrics and drops them.
//   - err:       ErrUnknownField / decode errors for the current event.
func (b *buffer) absorb(e *stellarrpc.Event, fieldTopic string) (completed *RawSwap, evicted []RawSwap, err error) {
	evicted = b.sweepStale()

	k := keyOf(e)
	r, ok := b.m[k]
	if !ok {
		t, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)
		r = &RawSwap{
			Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex),
			Pool: e.ContractID, ClosedAt: t,
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

// sweepStale removes entries older than maxAge, returning them as
// orphans. Called automatically from absorb.
func (b *buffer) sweepStale() []RawSwap {
	if b.maxAge <= 0 {
		return nil
	}
	cutoff := b.nowFn().Add(-b.maxAge)
	var evicted []RawSwap
	for k, r := range b.m {
		if r.ClosedAt.Before(cutoff) {
			evicted = append(evicted, *r)
			delete(b.m, k)
		}
	}
	return evicted
}

// orphans returns incomplete entries. Called after a backfill range
// ends; incompletes indicate either a contract bug or an RPC pagination
// anomaly.
func (b *buffer) orphans() []RawSwap {
	out := make([]RawSwap, 0, len(b.m))
	for _, r := range b.m {
		out = append(out, *r)
	}
	return out
}

// size returns the in-flight entry count. Used by tests to assert
// the buffer stays bounded.
func (b *buffer) size() int { return len(b.m) }
