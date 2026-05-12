package freeze

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// OpenFreezeLister enumerates the (asset, quote) pairs that the
// durable mirror still records as firing. Implemented by
// `internal/storage/timescale.FreezeEventSink.ListOpen`.
type OpenFreezeLister interface {
	ListOpen(ctx context.Context) ([]OpenFreezePair, error)
}

// OpenFreezePair mirrors `timescale.OpenFreeze` to keep the freeze
// package free of a hard dependency on the storage adapter.
type OpenFreezePair struct {
	Asset canonical.Asset
	Quote canonical.Asset
}

// Recoverer closes a firing freeze row, stamping recovered_at=now
// on the open `freeze_events` row for (asset, quote). Implemented
// by `internal/storage/timescale.FreezeEventSink.MarkRecovered`.
type Recoverer interface {
	MarkRecovered(ctx context.Context, asset, quote canonical.Asset) error
}

// Recovery is the freeze-recovery worker. It periodically lists
// every still-open `freeze_events` row, checks whether the Redis
// marker for (asset, quote) is still alive, and stamps
// recovered_at on the durable row when the marker is gone.
//
// This is the inverse half of the freeze pipeline: the orchestrator
// writes a Redis marker + INSERTs an open `freeze_events` row when
// a freeze fires; the marker has a TTL (typically a few minutes);
// when the underlying anomaly clears, the orchestrator stops
// refreshing the marker and the TTL elapses; the recovery worker
// notices and closes the durable row so the explorer /anomalies
// timeline shows a finished freeze rather than a forever-firing one.
//
// Why poll Redis instead of subscribing to keyspace expiry events?
// Redis keyspace notifications are off by default and operators
// typically don't enable them in production (significant CPU
// overhead). A 60-second poll loop is cheap (one Redis MGET per
// minute against the small set of currently-firing pairs) and
// matches the fact that the API's freshness SLO is also minute-
// scale — sub-minute recovery latency on the explorer timeline
// would be wasted precision.
type Recovery struct {
	cache    RedisCache
	lister   OpenFreezeLister
	closer   Recoverer
	interval time.Duration
	logger   *slog.Logger
}

// RecoveryOptions tunes a [Recovery] worker.
type RecoveryOptions struct {
	// Interval between polls. Default 60s — matches the API
	// freshness SLO and keeps Redis-side load minimal even when
	// many pairs are firing concurrently.
	Interval time.Duration
	// Logger receives structured logs at INFO (recovery events) and
	// WARN (Redis or postgres failures). Default = slog.Default().
	Logger *slog.Logger
}

// NewRecovery constructs a recovery worker. cache + lister + closer
// are all required — passing nil panics, since a recovery worker
// without one of its three legs is a no-op pretending to be useful.
func NewRecovery(
	cache RedisCache,
	lister OpenFreezeLister,
	closer Recoverer,
	opts RecoveryOptions,
) *Recovery {
	if cache == nil {
		panic("freeze: NewRecovery: cache must not be nil")
	}
	if lister == nil {
		panic("freeze: NewRecovery: lister must not be nil")
	}
	if closer == nil {
		panic("freeze: NewRecovery: closer must not be nil")
	}
	if opts.Interval <= 0 {
		opts.Interval = 60 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Recovery{
		cache:    cache,
		lister:   lister,
		closer:   closer,
		interval: opts.Interval,
		logger:   opts.Logger.With("component", "freeze-recovery"),
	}
}

// Run drives the recovery loop until ctx is cancelled. Returns
// the context error on shutdown — caller's WaitGroup-style worker
// pattern can `_ = r.Run(ctx)` and move on.
func (r *Recovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	// Run once immediately so the explorer doesn't wait one full
	// interval after aggregator restart for stale-row cleanup.
	r.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

// tick performs one recovery sweep: list open rows, check Redis,
// close the rows whose marker is gone. Errors are logged + counted;
// never propagated (a failed sweep is recoverable on the next tick).
func (r *Recovery) tick(ctx context.Context) {
	open, err := r.lister.ListOpen(ctx)
	if err != nil {
		r.logger.Warn("list open freezes", "err", err)
		obs.AnomalyFreezeRecoverySweepsTotal.WithLabelValues("error").Inc()
		return
	}
	if len(open) == 0 {
		obs.AnomalyFreezeRecoverySweepsTotal.WithLabelValues("ok").Inc()
		return
	}
	var recovered, stillFiring, errs int
	for _, p := range open {
		key := cachekeys.Freeze(p.Asset, p.Quote)
		_, err := r.cache.Get(ctx, key).Bytes()
		switch {
		case errors.Is(err, redis.Nil):
			// Marker gone → freeze cleared. Close the durable row.
			if err := r.closer.MarkRecovered(ctx, p.Asset, p.Quote); err != nil {
				r.logger.Warn("MarkRecovered failed",
					"asset", p.Asset.String(),
					"quote", p.Quote.String(),
					"err", err)
				errs++
				continue
			}
			recovered++
			obs.AnomalyFreezeRecoveredTotal.Inc()
			r.logger.Info("freeze recovered",
				"asset", p.Asset.String(),
				"quote", p.Quote.String())
		case err != nil:
			r.logger.Warn("Redis Get failed during recovery sweep",
				"key", key,
				"err", fmt.Errorf("recovery: %w", err))
			errs++
		default:
			stillFiring++
		}
	}
	r.logger.Debug("recovery sweep complete",
		"open", len(open),
		"recovered", recovered,
		"still_firing", stillFiring,
		"errs", errs)
	if errs > 0 {
		obs.AnomalyFreezeRecoverySweepsTotal.WithLabelValues("partial").Inc()
	} else {
		obs.AnomalyFreezeRecoverySweepsTotal.WithLabelValues("ok").Inc()
	}
}
