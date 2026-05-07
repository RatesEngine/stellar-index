package forex

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Worker periodically fetches the upstream rates + names and
// installs the result into a [Cache]. Designed to run as a
// goroutine for the lifetime of the API process.
type Worker struct {
	client   *Client
	cache    *Cache
	logger   *slog.Logger
	interval time.Duration
}

// NewWorker constructs the worker. interval is the refresh
// cadence — currency-api updates daily so anything < 1h is wasted
// fetches; 1h is a reasonable default that keeps the cache fresh
// across operator restarts.
func NewWorker(client *Client, cache *Cache, logger *slog.Logger, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Worker{
		client:   client,
		cache:    cache,
		logger:   logger,
		interval: interval,
	}
}

// Run blocks until ctx is cancelled. Fetches once immediately so
// the cache is populated before the first /v1/currencies request
// (subject to the upstream's response time), then refreshes every
// interval. Failures are logged but never crash the worker — the
// cache holds the prior snapshot until a refresh succeeds.
func (w *Worker) Run(ctx context.Context) error {
	w.refreshOnce(ctx)

	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			w.refreshOnce(ctx)
		}
	}
}

// refreshOnce performs a single fetch+install cycle. Errors get
// logged at warn level (not error — a stale cache is degraded
// service, not a crash condition).
func (w *Worker) refreshOnce(ctx context.Context) {
	rates, publishedAt, err := w.client.LatestUSDRates(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Warn("forex: rates fetch failed", "err", err)
		return
	}
	names, err := w.client.CurrencyNames(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Warn("forex: names fetch failed", "err", err)
		return
	}
	snap := buildSnapshot(rates, names, publishedAt, time.Now().UTC())
	w.cache.Set(snap)
	w.logger.Info("forex: snapshot installed",
		"currencies", len(snap.Currencies),
		"published_at", publishedAt,
	)
}
