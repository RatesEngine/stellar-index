package aquarius

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

// Source implements [consumer.Source] for Aquarius events.
//
// Thread-safety: one Source instance is driven by a single
// orchestrator goroutine per method invocation. Health() is safe
// to call from any goroutine.
type Source struct {
	rpc *stellarrpc.Client

	poolCache  map[string]PoolInfo // pool contract addr → token/type
	poolCacheM sync.RWMutex

	pollInterval time.Duration

	mu     sync.RWMutex
	health consumer.HealthStatus
}

// New constructs an Aquarius source.
func New(rpc *stellarrpc.Client, opts ...Option) *Source {
	s := &Source{
		rpc:          rpc,
		poolCache:    map[string]PoolInfo{},
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

// WithSeededPools pre-loads the pool→info cache. Callers
// typically populate this at startup from the trades hypertable's
// distinct pool addresses.
func WithSeededPools(seed map[string]PoolInfo) Option {
	return func(s *Source) {
		for k, v := range seed {
			s.poolCache[k] = v
		}
	}
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
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, err := s.rpc.GetEvents(ctx, from, to, s.filters(), &stellarrpc.Pagination{
			Cursor: cursor, Limit: 200,
		})
		if err != nil {
			s.setError(err)
			return fmt.Errorf("aquarius backfill getEvents: %w", err)
		}
		s.setOK()

		if err := s.processPage(ctx, resp.Events, out); err != nil {
			return err
		}
		if resp.Cursor == "" || len(resp.Events) == 0 {
			break
		}
		cursor = resp.Cursor
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
	startLedger, err := s.rpc.LatestLedgerSequence(ctx)
	if err != nil {
		return fmt.Errorf("aquarius seed tip: %w", err)
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

		if err := s.processPage(ctx, resp.Events, out); err != nil {
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

// processPage is shared between backfill + live-stream.
func (s *Source) processPage(ctx context.Context, events []stellarrpc.Event, out chan<- consumer.Event) error {
	for i := range events {
		e := &events[i]
		kind := classify(e)
		if kind == "" {
			continue
		}
		// We only emit trades from `trade` events. Other event
		// kinds are tracked for metrics / diagnostics only.
		if kind != EventTrade {
			continue
		}

		pool, ok := s.lookupPool(e.ContractID)
		if !ok {
			// TODO(#0): fetch pool info via router read (async).
			// For now skip — these events are PERMANENTLY DROPPED
			// (StreamLive advances the cursor past them; no
			// re-visit on restart). Count as a decode error so a
			// sustained rate pages ops — operators will notice
			// before a phase-1 deploy misses weeks of trades.
			obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			continue
		}

		closedAt, err := e.EventClosedAt()
		if err != nil {
			obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			continue
		}
		trades, err := decodeTrade(e, pool, closedAt)
		if err != nil {
			// Per-event parse errors don't bubble up — bad data
			// shouldn't kill the stream. Counted so sustained rates
			// trigger alerts.
			obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
			continue
		}
		for _, t := range trades {
			s.mu.Lock()
			s.health.LastEvent = t.Timestamp
			if t.Ledger > s.health.LastLedger {
				s.health.LastLedger = t.Ledger
			}
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case out <- TradeEvent{Trade: t}:
			}
		}
	}
	return nil
}

func (s *Source) filters() []stellarrpc.EventFilter {
	// TODO(#0): when topic-symbol blobs become real, add them to
	// the first-position filter so stellar-rpc drops non-matching
	// events server-side.
	return []stellarrpc.EventFilter{{Type: "contract"}}
}

func (s *Source) lookupPool(contract string) (PoolInfo, bool) {
	s.poolCacheM.RLock()
	defer s.poolCacheM.RUnlock()
	p, ok := s.poolCache[contract]
	return p, ok
}

// SeedPool adds/updates a pool in the cache. Exposed for the
// orchestrator's start-up warmup code (future: pool-info seeding
// from the trades hypertable).
func (s *Source) SeedPool(contract string, info PoolInfo) {
	s.poolCacheM.Lock()
	defer s.poolCacheM.Unlock()
	s.poolCache[contract] = info
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

// TradeEvent is the [consumer.Event] this source emits. Shape
// intentionally matches soroswap.TradeEvent — the event-sink
// loop in cmd/ratesengine-indexer could in principle type-switch
// on either; currently it switches on each source's type
// explicitly to keep the wiring greppable.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "aquarius.trade" }

// Source implements [consumer.Event].
func (TradeEvent) Source() string { return SourceName }

// Compile-time conformance checks — see soroswap/consumer.go for
// rationale (catches interface drift at `go build` of this package).
var (
	_ consumer.Source = (*Source)(nil)
	_ consumer.Event  = TradeEvent{}
)
