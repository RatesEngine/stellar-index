// Package streampublish bridges the closed-bucket VWAP cache to the
// SSE streaming Hub backing /v1/price/stream.
//
// One [Publisher] runs in the API binary alongside the Hub. For
// each operator-configured (asset, quote) pair it polls the same
// PriceReader the /v1/price handler uses; when a new closed bucket
// arrives (detected by ObservedAt advancing past the last published
// timestamp) it serialises the snapshot envelope and calls
// [streaming.Hub.Publish] on the matching topic. Subscribers
// attached to the topic via /v1/price/stream receive byte-identical
// payloads — the same cross-region consistency property as
// /v1/price itself (ADR-0015).
//
// Static pair list: the operator declares which pairs to broadcast
// in the binary's `[api.streaming]` config section. Adding a pair
// requires a config + restart. Pairs without observations stay
// silent — no synthetic events.
package streampublish

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/StellarIndex/stellar-index/internal/api/streaming"
	v1 "github.com/StellarIndex/stellar-index/internal/api/v1"
	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/obs"
)

// PriceReader is the narrow read-side dependency the publisher
// needs. Satisfied by the same v1.PriceReader the /v1/price
// handler consumes — declared here to avoid an import cycle into
// the v1 package's `Server`. In practice the production wiring
// passes a shared adapter to both.
type PriceReader interface {
	LatestPrice(ctx context.Context, asset, quote canonical.Asset) (
		snapshot v1.PriceSnapshot, sources []string, stale bool, err error,
	)
}

// Publisher polls a fixed set of pairs and republishes every newly-
// closed bucket to a Hub. Construct via [New], then call [Run] in
// a long-lived goroutine.
//
// Goroutine-safe — Run starts one inner goroutine per pair, all
// driven off the supplied context.
type Publisher struct {
	hub      *streaming.Hub
	reader   PriceReader
	interval time.Duration
	logger   *slog.Logger

	// lastPublished tracks the most recent ObservedAt we've already
	// fanned out, keyed by topic. Each pair's poller goroutine has
	// exclusive access to its own key — no cross-goroutine writes —
	// but we still wrap in a mutex for race-detector cleanliness and
	// to keep the data structure honest if a future caller wants to
	// inspect it from elsewhere.
	mu            sync.Mutex
	lastPublished map[string]time.Time
}

// New constructs a Publisher. The reader is the same PriceReader
// the /v1/price handler uses; the hub is the same instance passed
// to v1.Options.Hub. Interval clamps to 1 s minimum (zero / negative
// uses [DefaultInterval]).
func New(hub *streaming.Hub, reader PriceReader, interval time.Duration, logger *slog.Logger) *Publisher {
	if hub == nil {
		panic("streampublish: hub must not be nil")
	}
	if reader == nil {
		panic("streampublish: reader must not be nil")
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	if interval < time.Second {
		interval = time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{
		hub:           hub,
		reader:        reader,
		interval:      interval,
		logger:        logger,
		lastPublished: map[string]time.Time{},
	}
}

// DefaultInterval is the per-pair poll cadence used when the
// caller passes 0 / negative to [New]. 5 s detects a new 1-minute
// closed bucket within 5 s of its end — well inside the 30 s
// freshness SLA target — without hammering the reader for
// pairs that update once per minute.
const DefaultInterval = 5 * time.Second

// Run starts one polling goroutine per pair and blocks until ctx
// is cancelled. Returns ctx.Err() on shutdown so callers can chain
// onto an errgroup without losing the cancel cause.
//
// Pairs is treated as immutable for the Publisher's lifetime —
// adding a pair requires a binary restart. The cost of that
// constraint is recovered by simpler bookkeeping (no
// add/remove/ref-count machinery).
func (p *Publisher) Run(ctx context.Context, pairs []canonical.Pair) error {
	if len(pairs) == 0 {
		// No pairs configured — nothing to do, but block until
		// shutdown so callers can wait on the same ctx without a
		// special-cased zero path.
		<-ctx.Done()
		return ctx.Err()
	}

	var wg sync.WaitGroup
	for _, pair := range pairs {
		wg.Add(1)
		go func(pair canonical.Pair) {
			defer wg.Done()
			p.pollLoop(ctx, pair)
		}(pair)
	}
	wg.Wait()
	return ctx.Err()
}

// pollLoop is the per-pair ticker. Returns when ctx is cancelled.
func (p *Publisher) pollLoop(ctx context.Context, pair canonical.Pair) {
	topic := v1.PriceStreamTopic(pair.Base, pair.Quote)
	t := time.NewTicker(p.interval)
	defer t.Stop()

	// Poll once immediately so a freshly-restarted API binary
	// publishes the most-recent closed bucket without waiting a full
	// interval. The detect-change machinery guards against
	// republishing the same bucket on the next tick.
	p.tickOnce(ctx, pair, topic)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tickOnce(ctx, pair, topic)
		}
	}
}

// tickOnce queries the latest price for one pair and publishes if
// the ObservedAt advanced past the last-published timestamp.
//
// Best-effort: a reader error logs at WARN and the loop continues —
// a Postgres outage shouldn't take the publisher down across other
// pairs. ErrPriceNotFound is silent (the pair has no closed bucket
// yet, or has fallen outside the freshness window).
func (p *Publisher) tickOnce(ctx context.Context, pair canonical.Pair, topic string) {
	pollCtx, cancel := context.WithTimeout(ctx, p.interval)
	defer cancel()

	snap, sources, stale, err := p.reader.LatestPrice(pollCtx, pair.Base, pair.Quote)
	if err != nil {
		if errors.Is(err, v1.ErrPriceNotFound) {
			return
		}
		// Suppress log noise on shutdown.
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		p.logger.Warn("streampublish: LatestPrice failed",
			"err", err, "pair", pair.String())
		return
	}
	if !p.shouldPublish(topic, snap.ObservedAt) {
		return
	}

	payload, err := json.Marshal(struct {
		Snapshot v1.PriceSnapshot `json:"snapshot"`
		Sources  []string         `json:"sources"`
		Stale    bool             `json:"stale,omitempty"`
	}{Snapshot: snap, Sources: sources, Stale: stale})
	if err != nil {
		// json.Marshal of a fixed shape that already round-trips
		// through /v1/price — only surfaces on a Go runtime defect.
		p.logger.Error("streampublish: marshal failed",
			"err", err, "pair", pair.String())
		return
	}

	p.hub.Publish(topic, "price_update", payload)
	obs.StreamPublishTotal.WithLabelValues("price_stream").Inc()
}

// shouldPublish records the latest observed timestamp for the
// topic and returns true on advance. False (no advance) is the
// common case between bucket closes — the reader returns the same
// bucket for every poll until a new one materialises.
func (p *Publisher) shouldPublish(topic string, observedAt time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	prev, seen := p.lastPublished[topic]
	if seen && !observedAt.After(prev) {
		return false
	}
	p.lastPublished[topic] = observedAt
	return true
}
