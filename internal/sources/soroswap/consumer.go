package soroswap

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

// Source implements [consumer.Source] for Soroswap events.
//
// Thread-safety: one Source instance is driven by a single
// orchestrator goroutine per method invocation. [Health] is safe
// to call from any goroutine.
type Source struct {
	rpc *stellarrpc.Client

	// PairTokens maps a pair contract address → (token0, token1).
	// Populated by walking `new_pair` factory events on backfill +
	// live-stream. Seeded from the DB cache on start.
	pairTokens  map[string]pairTokens
	pairTokensM sync.RWMutex

	// PollInterval is the live-stream heartbeat. Smaller = fresher
	// + more RPC load; larger = more lag. Default 2 s.
	pollInterval time.Duration

	// Health status (written under mu).
	mu     sync.RWMutex
	health consumer.HealthStatus
}

type pairTokens struct {
	Token0 canonical.Asset
	Token1 canonical.Asset
}

// New constructs a Soroswap source. The rpc client is expected to
// target a healthy stellar-rpc endpoint; this constructor does not
// verify it (let the orchestrator decide how to handle an unhealthy
// RPC — usually log + retry).
func New(rpc *stellarrpc.Client, opts ...Option) *Source {
	s := &Source{
		rpc:          rpc,
		pairTokens:   map[string]pairTokens{},
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

// WithSeededPairTokens pre-loads the pair→tokens cache. Callers
// typically read this from the trades hypertable on startup so we
// don't re-walk factory events from genesis every boot.
func WithSeededPairTokens(seed map[string]pairTokens) Option {
	return func(s *Source) {
		for k, v := range seed {
			s.pairTokens[k] = v
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

// BackfillRange implements [consumer.Source]. Processes the closed
// ledger range [from, to] in RPC-pagination-friendly chunks,
// emitting completed trades to out. Returns on range exhaustion or
// ctx cancel.
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
			return fmt.Errorf("soroswap backfill getEvents: %w", err)
		}
		s.setOK()

		if err := s.processPage(resp.Events, buf, out); err != nil {
			return err
		}

		if resp.Cursor == "" || len(resp.Events) == 0 {
			break
		}
		cursor = resp.Cursor
	}

	// Any buffered swap that never got its sync is a bug or a page
	// boundary we didn't handle. Surface + drop.
	for _, orphan := range buf.orphans() {
		_ = orphan // TODO(#0): metric counter soroswap_orphan_swaps_total
	}
	return nil
}

// StreamLive implements [consumer.Source]. Polls stellar-rpc's
// getEvents starting from the configured cursor, emits trades, and
// persists progress via cursor updates at the caller (the
// orchestrator owns cursor persistence, not this method).
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
			continue // backoff is the ticker's job
		}
		s.setOK()

		if err := s.processPage(resp.Events, buf, out); err != nil {
			s.setError(err)
			continue
		}

		if resp.Cursor != "" {
			cursor = resp.Cursor
		}
		if resp.LatestLedger > 0 {
			lastSeenLedger = resp.LatestLedger
			s.mu.Lock()
			// Lag = network tip - our last-processed ledger. Zero at
			// tip or when we haven't observed any events yet.
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
func (s *Source) processPage(events []stellarrpc.Event, buf *buffer, out chan<- consumer.Event) error {
	for i := range events {
		e := &events[i]
		kind := classify(e)
		if kind == "" {
			continue
		}

		switch kind {
		case EventNewPair:
			s.recordNewPair(e)
		case EventSwap, EventSync:
			completed, evicted := buf.absorb(e, kind)
			if len(evicted) > 0 {
				// Stale entries that timed out waiting for their
				// partner. TODO(#0): increment soroswap_orphan_pairs_total
				// metric with a `reason=age` label. For now they
				// drop silently — the memory bound is the priority.
				_ = evicted
			}
			for _, r := range completed {
				tokens, ok := s.lookupPair(r.Pair)
				if !ok {
					// We don't know the token mapping yet — usually
					// because the factory event for this pair hasn't
					// been seen. Re-buffer for a future pass via
					// orphan accounting (future work).
					continue
				}
				trade, derr := decodeSwap(r, tokens.Token0, tokens.Token1)
				if derr != nil {
					// Per-event parse failures don't bubble up —
					// bad data shouldn't kill the stream. Counted so
					// sustained rates trigger alerts.
					obs.SourceDecodeErrorsTotal.WithLabelValues(SourceName).Inc()
					continue
				}
				s.mu.Lock()
				s.health.LastEvent = trade.Timestamp
				if trade.Ledger > s.health.LastLedger {
					s.health.LastLedger = trade.Ledger
				}
				s.mu.Unlock()
				out <- TradeEvent{Trade: trade}
			}
		}
	}
	return nil
}

// filters returns the EventFilter list the RPC subscription uses.
// Covers the three events we actually care about; we rely on
// topic-based matching to avoid pulling every contract event on
// the network.
func (s *Source) filters() []stellarrpc.EventFilter {
	// TODO(#0): when TopicSymbol* blobs are verified against real RPC
	// traffic, add them as the first-position topic filter so the
	// server drops non-matching events server-side (saves our
	// bandwidth).
	return []stellarrpc.EventFilter{
		{Type: "contract"},
	}
}

// recordNewPair learns token mappings from factory events. The new_pair
// event's topic carries the two token contract addresses.
func (s *Source) recordNewPair(e *stellarrpc.Event) {
	// TODO(#0): real decode — topic[1] = token0, topic[2] = token1,
	// event body = pair contract address. Stub for now.
	_ = e
}

func (s *Source) lookupPair(pair string) (pairTokens, bool) {
	s.pairTokensM.RLock()
	defer s.pairTokensM.RUnlock()
	t, ok := s.pairTokens[pair]
	return t, ok
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

// TradeEvent is the [consumer.Event] shape we emit.
// The orchestrator type-switches on this at the output channel.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "soroswap.trade" }

// Source implements [consumer.Event] — matches [SourceName].
func (TradeEvent) Source() string { return SourceName }

// ─── Correlation buffer ─────────────────────────────────────────
// Groups swap + sync by (ledger, tx_hash, op_index). Emits complete
// pairs back to the caller; holds incompletes until either their
// partner event arrives, or their ClosedAt is older than maxAge
// (at which point they're returned as orphans and dropped).
//
// Bounded memory: without age-based eviction, StreamLive's buffer
// would grow unbounded whenever a swap arrives without its matching
// sync (page boundary races, malformed pair contracts, etc.).

// defaultOrphanMaxAge is how long we hold an incomplete entry
// waiting for its partner before treating it as an orphan.
//
// Soroswap swap+sync are emitted in the same transaction — they
// SHOULD always arrive within a single RPC page. Cross-page splits
// happen at the page boundary but resolve within seconds on the
// next poll. Five minutes is a generous ceiling that tolerates
// worst-case pagination lag without holding references to events
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
func (b *buffer) absorb(e *stellarrpc.Event, kind string) (completed []RawPair, evicted []RawPair) {
	// Evict stale orphans first — keeps the map bounded in size
	// regardless of how long the process runs.
	evicted = b.sweepStale()

	k := keyOf(e)
	p, ok := b.m[k]
	if !ok {
		t, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)
		p = &RawPair{Ledger: e.Ledger, TxHash: e.TxHash, OpIndex: uint32(e.OperationIndex), Pair: e.ContractID, ClosedAt: t}
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
// maxAge relative to nowFn(), returning them as orphans.
func (b *buffer) sweepStale() []RawPair {
	if b.maxAge <= 0 {
		return nil
	}
	cutoff := b.nowFn().Add(-b.maxAge)
	var evicted []RawPair
	for k, p := range b.m {
		if p.ClosedAt.Before(cutoff) {
			evicted = append(evicted, *p)
			delete(b.m, k)
		}
	}
	return evicted
}

// orphans returns every incomplete entry; called after a backfill
// range finishes so metrics can attribute the leak. Does not
// mutate the buffer.
func (b *buffer) orphans() []RawPair {
	out := make([]RawPair, 0, len(b.m))
	for _, p := range b.m {
		out = append(out, *p)
	}
	return out
}

// size returns the number of in-flight (incomplete) pairs. Used by
// tests to assert the buffer stays bounded.
func (b *buffer) size() int { return len(b.m) }
