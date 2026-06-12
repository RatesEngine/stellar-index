package external

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
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
	//
	// G10-11: each Streamer.Start spawns a reconnect-forever goroutine
	// bound to the context we hand it. If a LATER Start fails, the
	// earlier streamers' goroutines would otherwise leak — Run returns
	// an error, the caller never gets a wait()/cancel handle, and those
	// goroutines run until the parent ctx is cancelled (often never, on
	// a startup-config error). We start every streamer under a DERIVED,
	// cancellable context and cancel it on the error path so the
	// already-launched streamers shut down before Run returns. On the
	// happy path the derived context lives as long as the parent (it's
	// cancelled when ctx is, via context.WithCancel propagation).
	streamerCtx, cancelStreamers := context.WithCancel(ctx)
	type running struct {
		name string
		ch   <-chan canonical.Trade
	}
	launched := make([]running, 0, len(streamers))
	for _, s := range streamers {
		ch, err := s.Streamer.Start(streamerCtx, s.Pairs)
		if err != nil {
			// Tear down every streamer we already launched, then wait
			// for their goroutines to observe the cancellation and
			// close their channels so we don't return with goroutines
			// still draining.
			cancelStreamers()
			for _, r := range launched {
				drainUntilClosed(r.ch)
			}
			return nil, fmt.Errorf("external.Run: start %q: %w", s.Streamer.Name(), err)
		}
		launched = append(launched, running{name: s.Streamer.Name(), ch: ch})
	}

	var wg sync.WaitGroup
	for _, r := range launched {
		wg.Add(1)
		go func(name string, ch <-chan canonical.Trade) {
			defer wg.Done()
			forwardTrades(streamerCtx, name, ch, sink, logger)
		}(r.name, r.ch)
	}

	// Pollers: one goroutine per poller running a ticker at the
	// declared PollInterval. Each tick calls PollOnce; returned
	// trades + updates are wrapped and fanned to the shared sink.
	for _, p := range pollers {
		if p.Poller == nil {
			return nil, teardown(cancelStreamers, &wg, errors.New("external.Run: nil Poller in spec"))
		}
		interval := p.Poller.PollInterval()
		if interval <= 0 {
			return nil, teardown(cancelStreamers, &wg, fmt.Errorf(
				"external.Run: %q declares non-positive PollInterval %v",
				p.Poller.Name(), interval))
		}
		wg.Add(1)
		go func(spec PollerSpec) {
			defer wg.Done()
			runPoller(ctx, spec, sink, logger)
		}(p)
	}

	// wait() blocks until every connector goroutine has shut down, then
	// releases the derived streamer context. cancelStreamers is also
	// the safety valve the error paths above use to avoid leaking the
	// already-launched streamer goroutines (G10-11).
	wait = func() {
		wg.Wait()
		cancelStreamers()
	}
	return wait, nil
}

// teardown cancels the derived streamer context and blocks until the
// already-launched streamer goroutines have observed the cancellation
// and exited, then returns the original error. Used by Run's poller-
// validation error paths so a late config error doesn't leak the
// streamer goroutines started earlier in the same call (G10-11).
func teardown(cancel context.CancelFunc, wg *sync.WaitGroup, err error) error {
	cancel()
	wg.Wait()
	return err
}

// drainUntilClosed reads and discards from a streamer's trade channel
// until it closes. Used on the streamer-Start error path (G10-11):
// the earlier streamers were started with the now-cancelled derived
// context, so their run loops will close their channels promptly; we
// drain so the close — and thus the goroutine exit — is not blocked on
// a pending send, then return.
func drainUntilClosed(ch <-chan canonical.Trade) {
	for range ch {
	}
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
		// after rate-limit (e.g. coingecko backoff) AND by sources
		// like chainlink that frequently see "no new round" between
		// 1-hour feed updates. Pre-2026-06-01 this branch returned
		// without updating LastSuccessUnix, so a healthy chainlink
		// poller (polling every 30s, but feeds updating hourly)
		// looked stale to `ratesengine_external_poller_stale`
		// within ~10-15 min. The outcome counter still bumps
		// "skipped" so operators can tell skip from success; but
		// the timestamp bumps too because the poller is alive +
		// reaching upstream — a skip means "we polled and there
		// was nothing new", not "we couldn't poll."
		if trades == nil && updates == nil {
			obs.ExternalPollerPollsTotal.WithLabelValues(name, "skipped").Inc()
			obs.ExternalPollerLastSuccessUnix.WithLabelValues(name).Set(float64(time.Now().Unix()))
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
