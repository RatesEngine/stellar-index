package baseline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// DefaultWindow is the size of the rolling training window per
// ADR-0019: 30 days of 1m bucket VWAPs.
const DefaultWindow = 30 * 24 * time.Hour

// VWAPSource reads bucket-aligned 1m VWAPs for a pair over a
// half-open window [from, to). Implementations are expected to
// return values in chronological order (oldest first) — the
// downstream [ReturnsFromVWAPs] depends on that ordering for
// bucket-to-bucket return computation.
//
// Production wiring: a thin adapter around
// `timescale.Store.VWAPsForPair1m`.
type VWAPSource interface {
	VWAPsForPair1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]float64, error)
}

// Sink persists a freshly-computed baseline. Implementations are
// expected to be UPSERT — one row per pair, the latest computation
// wins. Production wiring: an adapter that builds a
// `timescale.StoredBaseline` and calls `Store.UpsertBaseline`.
//
// The interface takes the metadata fields directly rather than a
// pre-built struct so the refresher doesn't depend on the storage
// package — keeps the dep direction clean (storage adapter
// implements `Sink`, not the other way around).
type Sink interface {
	UpsertBaseline(
		ctx context.Context,
		pair canonical.Pair,
		computedAt, windowStart, windowEnd time.Time,
		b Baseline,
	) error
}

// Refresher recomputes per-pair baselines from the prices_1m CAGG
// and writes them through a [Sink]. Designed to run as a separate
// goroutine in the aggregator binary on a slow cadence (e.g.
// hourly) — baselines are 30-day rolling stats, so refreshing at
// the 1m bucket cadence would be wasted work.
type Refresher struct {
	src    VWAPSource
	sink   Sink
	window time.Duration
	logger *slog.Logger
}

// NewRefresher constructs a Refresher. Pass `window <= 0` to use
// [DefaultWindow]. Logger is required (use slog.Default() if you
// don't have one).
func NewRefresher(src VWAPSource, sink Sink, window time.Duration, logger *slog.Logger) *Refresher {
	if window <= 0 {
		window = DefaultWindow
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Refresher{src: src, sink: sink, window: window, logger: logger}
}

// RefreshOutcome describes the per-pair outcome of one refresh
// attempt — used by [RefreshSummary] to give callers a structured
// breakdown of what happened across a batch.
type RefreshOutcome int

const (
	OutcomeOK RefreshOutcome = iota
	OutcomeNotEnoughSamples
	OutcomeReadError
	OutcomeWriteError
)

func (o RefreshOutcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeNotEnoughSamples:
		return "not_enough_samples"
	case OutcomeReadError:
		return "read_error"
	case OutcomeWriteError:
		return "write_error"
	default:
		return "unknown"
	}
}

// RefreshSummary aggregates the outcomes of a [Refresher.RefreshAll]
// run. Counts per outcome let the caller emit metrics in one place
// without scanning per-pair errors.
type RefreshSummary struct {
	OK               int
	NotEnoughSamples int
	ReadErrors       int
	WriteErrors      int
}

// RefreshPair recomputes the baseline for one pair and writes it.
//
// Returns:
//
//   - (OutcomeOK, nil) on a successful upsert
//   - (OutcomeNotEnoughSamples, [ErrNotEnoughSamples]) when the
//     window has fewer than [MinSamples] returns — the caller
//     applies the bootstrap policy (ADR-0019 §"Bootstrap policy")
//     rather than treating this as an error
//   - (OutcomeReadError, err) on a [VWAPSource] failure
//   - (OutcomeWriteError, err) on a [Sink] failure
func (r *Refresher) RefreshPair(ctx context.Context, pair canonical.Pair) (RefreshOutcome, error) {
	now := time.Now().UTC()
	windowStart := now.Add(-r.window)

	vwaps, err := r.src.VWAPsForPair1m(ctx, pair, windowStart, now)
	if err != nil {
		return OutcomeReadError, fmt.Errorf("baseline: VWAPsForPair1m %s: %w", pair.String(), err)
	}

	returns := ReturnsFromVWAPs(vwaps)
	b, err := FromReturns(returns)
	if err != nil {
		// ErrNotEnoughSamples is the only error FromReturns produces;
		// translate it into the OutcomeNotEnoughSamples path so callers
		// can count "this pair is still in bootstrap" without parsing
		// the error string.
		if errors.Is(err, ErrNotEnoughSamples) {
			return OutcomeNotEnoughSamples, err
		}
		return OutcomeReadError, fmt.Errorf("baseline: FromReturns %s: %w", pair.String(), err)
	}

	if err := r.sink.UpsertBaseline(ctx, pair, now, windowStart, now, b); err != nil {
		return OutcomeWriteError, fmt.Errorf("baseline: UpsertBaseline %s: %w", pair.String(), err)
	}
	return OutcomeOK, nil
}

// RefreshAll runs [Refresher.RefreshPair] for every pair in
// `pairs` with up to `concurrency` in flight at once. Per-pair
// failures are logged but don't abort the batch — a transient
// failure on one pair shouldn't starve the others. Returns a
// summary of outcomes across the batch.
//
// concurrency <= 0 falls back to 1 (serial). Use a value at or
// below your DB connection-pool size to avoid pool exhaustion.
func (r *Refresher) RefreshAll(ctx context.Context, pairs []canonical.Pair, concurrency int) RefreshSummary {
	if concurrency < 1 {
		concurrency = 1
	}

	type result struct {
		outcome RefreshOutcome
	}
	results := make(chan result, len(pairs))

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
loop:
	for _, pair := range pairs {
		select {
		case <-ctx.Done():
			break loop // exit the outer for, not just this select
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(pair canonical.Pair) {
			defer wg.Done()
			defer func() { <-sem }()

			outcome, err := r.RefreshPair(ctx, pair)
			if err != nil && !errors.Is(err, ErrNotEnoughSamples) && ctx.Err() == nil {
				r.logger.Warn("baseline refresh failed",
					"pair", pair.String(), "outcome", outcome.String(), "err", err)
			}
			results <- result{outcome: outcome}
		}(pair)
	}
	wg.Wait()
	close(results)

	var sum RefreshSummary
	for res := range results {
		switch res.outcome {
		case OutcomeOK:
			sum.OK++
		case OutcomeNotEnoughSamples:
			sum.NotEnoughSamples++
		case OutcomeReadError:
			sum.ReadErrors++
		case OutcomeWriteError:
			sum.WriteErrors++
		}
	}
	return sum
}
