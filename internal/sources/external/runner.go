package external

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// StreamerSpec is a configured streamer + the pair list it should
// subscribe to. Returned by each venue package's builder so the
// indexer doesn't need to know how to wire pair maps per venue.
type StreamerSpec struct {
	Streamer Streamer
	Pairs    []canonical.Pair
}

// PollerSpec pairs a Poller with the list of pairs it should fetch
// on each tick. Pollers declare their own PollInterval; the runner
// ticks at that cadence and fans the PollOnce outputs into the
// shared sink.
type PollerSpec struct {
	Poller Poller
	Pairs  []canonical.Pair
}

// Run launches every streamer (and later, poller) in its own
// goroutine, fans trade output into the supplied consumer.Event
// channel wrapping each as a TradeEvent, and returns a cleanup
// function that blocks until all connectors have shut down. The
// cleanup is normally called from the indexer's shutdown sequence;
// ctx cancellation is the primary stop signal.
//
// On a streamer's own error channel close (unrecoverable error),
// Run logs and lets it drop — the other connectors continue. Fatal
// config errors at Start time are returned synchronously before any
// goroutine spawns.
//
// The signature intentionally mirrors the dispatcher's
// processAndPersist goroutine pattern so the indexer can wire them
// symmetrically.
func Run(
	ctx context.Context,
	streamers []StreamerSpec,
	pollers []PollerSpec,
	sink chan<- consumer.Event,
	logger *slog.Logger,
) (wait func(), err error) {
	if logger == nil {
		logger = slog.Default()
	}
	if len(streamers) == 0 && len(pollers) == 0 {
		// No external sources configured — still return a valid
		// wait() so the caller's shutdown path doesn't need
		// special-case logic.
		return func() {}, nil
	}

	// Pre-flight every Start. Fatal config errors (empty pair list,
	// bad endpoint URL) surface here before we spawn anything.
	type running struct {
		name string
		ch   <-chan canonical.Trade
	}
	launched := make([]running, 0, len(streamers))
	for _, s := range streamers {
		ch, err := s.Streamer.Start(ctx, s.Pairs)
		if err != nil {
			return nil, fmt.Errorf("external.Run: start %q: %w", s.Streamer.Name(), err)
		}
		launched = append(launched, running{name: s.Streamer.Name(), ch: ch})
	}

	var wg sync.WaitGroup
	for _, r := range launched {
		wg.Add(1)
		go func(name string, ch <-chan canonical.Trade) {
			defer wg.Done()
			forwardTrades(ctx, name, ch, sink, logger)
		}(r.name, r.ch)
	}

	// Pollers: one goroutine per poller running a ticker at the
	// declared PollInterval. Each tick calls PollOnce; returned
	// trades + updates are wrapped and fanned to the shared sink.
	for _, p := range pollers {
		if p.Poller == nil {
			return nil, errors.New("external.Run: nil Poller in spec")
		}
		interval := p.Poller.PollInterval()
		if interval <= 0 {
			return nil, fmt.Errorf("external.Run: %q declares non-positive PollInterval %v",
				p.Poller.Name(), interval)
		}
		wg.Add(1)
		go func(spec PollerSpec) {
			defer wg.Done()
			runPoller(ctx, spec, sink, logger)
		}(p)
	}

	wait = func() { wg.Wait() }
	return wait, nil
}

// forwardTrades drains one streamer's channel into the shared sink,
// wrapping each trade as a TradeEvent. Returns when the source
// channel closes (streamer shutdown) or ctx is cancelled.
func forwardTrades(
	ctx context.Context,
	source string,
	in <-chan canonical.Trade,
	sink chan<- consumer.Event,
	logger *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case trade, ok := <-in:
			if !ok {
				logger.Info("external streamer closed",
					"source", source)
				return
			}
			select {
			case <-ctx.Done():
				return
			case sink <- TradeEvent{Trade: trade}:
			}
		}
	}
}

// runPoller drives a single Poller at its declared cadence. On each
// tick, PollOnce is called; returned trades land as TradeEvents and
// updates as UpdateEvents on the shared sink. An error from PollOnce
// is logged + counted (future: per-source metrics hook) but doesn't
// stop the loop — poll-based sources regularly hit transient REST
// errors (network blip, venue rate-limit) and should just retry at
// the next tick.
//
// First poll fires immediately on startup rather than waiting a full
// interval — operators want fresh data visible within seconds of
// starting the indexer, not only after the ticker elapses.
func runPoller(
	ctx context.Context,
	spec PollerSpec,
	sink chan<- consumer.Event,
	logger *slog.Logger,
) {
	name := spec.Poller.Name()
	interval := spec.Poller.PollInterval()

	doPoll := func() {
		trades, updates, err := spec.Poller.PollOnce(ctx, spec.Pairs)
		if err != nil {
			obs.ExternalPollerPollsTotal.WithLabelValues(name, "error").Inc()
			logger.Warn("poller error",
				"source", name, "err", err)
			return
		}
		// (nil trades, nil updates, nil err) is the convention for
		// "poller skipped this tick" — used by per-poller cooldown
		// after rate-limit (e.g. coingecko backoff). Don't conflate
		// with success; operators alerting on `outcome="success"`
		// staleness need a true silence here.
		if trades == nil && updates == nil {
			obs.ExternalPollerPollsTotal.WithLabelValues(name, "skipped").Inc()
			return
		}
		obs.ExternalPollerPollsTotal.WithLabelValues(name, "success").Inc()
		obs.ExternalPollerLastSuccessUnix.WithLabelValues(name).Set(float64(time.Now().Unix()))
		for _, t := range trades {
			select {
			case <-ctx.Done():
				return
			case sink <- TradeEvent{Trade: t}:
			}
		}
		for _, u := range updates {
			select {
			case <-ctx.Done():
				return
			case sink <- UpdateEvent{Update: u}:
			}
		}
	}

	// Fire once on start, then on the ticker cadence.
	doPoll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("poller stopping", "source", name)
			return
		case <-ticker.C:
			doPoll()
		}
	}
}
