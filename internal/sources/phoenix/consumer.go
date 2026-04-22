package phoenix

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
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

// StreamLive implements [consumer.Source].
func (s *Source) StreamLive(ctx context.Context, out chan<- consumer.Event) error {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	var cursor string
	buf := newBuffer()
	var lastSeenLedger uint32

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		resp, err := s.rpc.GetEvents(ctx, lastSeenLedger, 0, s.filters(), &stellarrpc.Pagination{
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
			lastSeenLedger = resp.LatestLedger
			s.mu.Lock()
			s.health.LagLedgers = 0
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

		completed, err := buf.absorb(e, fieldTopic)
		if err != nil {
			// Unknown field: skip silently (future: metric).
			continue
		}
		if completed == nil {
			continue
		}

		trade, err := decodeSwap(completed)
		if err != nil {
			// Per-event decode failures don't bubble up.
			continue
		}
		s.mu.Lock()
		s.health.LastEvent = trade.Timestamp
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

// ─── 8-field correlation buffer ─────────────────────────────────

type buffer struct {
	m map[groupKey]*RawSwap
}

func newBuffer() *buffer { return &buffer{m: map[groupKey]*RawSwap{}} }

// absorb stores one field-event in the appropriate RawSwap slot.
// Returns a non-nil *RawSwap when all 8 slots are populated; the
// caller emits + decodes. Returns ErrUnknownField if fieldTopic
// doesn't match any of the 8 (caller typically skips).
func (b *buffer) absorb(e *stellarrpc.Event, fieldTopic string) (*RawSwap, error) {
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
		return nil, err
	}
	if r.Complete() {
		delete(b.m, k)
		return r, nil
	}
	return nil, nil
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
