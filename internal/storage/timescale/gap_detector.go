package timescale

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/RatesEngine/rates-engine/internal/obs"
)

// GapDetectorInterval is the cadence at which [RunGapDetector]
// re-scans soroban_events for contiguous data-coverage gaps.
//
// Why 5 minutes:
//   - The expensive part is the LAG()-over-DISTINCT scan, which on
//     r1 against ~12 M distinct ledgers runs in ~2-3 s.
//   - The metric feeds a paging alert on a >threshold gap; 5 min
//     means the alert fires within ~6-8 min of the gap forming,
//     which is the right urgency for an ingest halt.
//   - 5 min × 13 sources (when SDEX gap detection ships) is still
//     under 5% wall-clock of a single connection, which is plenty
//     of head room.
const GapDetectorInterval = 5 * time.Minute

// GapDetectorMinGapSize is the threshold below which a contiguous
// gap is treated as expected no-Soroban-activity noise rather than
// an ingest gap. Matches `ratesengine-ops find-data-gaps`'s
// default of 1000 ledgers (~1.5 h of network time) — see the
// godoc on that subcommand for the rationale.
const GapDetectorMinGapSize = int64(1000)

// gapDetectorTimeout caps one scan attempt. The actual r1 latency
// is ~2-3 s; 60 s is enough headroom for a transient Postgres blip
// or a chunk-compression mid-pass without holding the goroutine
// open through a deeper outage.
const gapDetectorTimeout = 60 * time.Second

// RunGapDetector blocks until ctx is cancelled, periodically
// scanning soroban_events for contiguous ledger-coverage gaps and
// emitting [obs.IngestGapLedgers] + [obs.IngestGapCount] +
// [obs.IngestGapMaxSize] gauges per source plus the
// [obs.IngestGapDetectorRunsTotal] +
// [obs.IngestGapDetectorDurationSeconds] meta-metrics for the
// worker itself.
//
// Data-derived complement to the cursor-derived density projection
// in /v1/diagnostics/ingestion. Cursor coverage measures process
// state ("did we walk this ledger") and can read 100% while data
// is missing — the F-0020 audit found exactly that, with the
// soroban_events writer halted across a 92,737-ledger contiguous
// window while the cursor inventory + density projection said
// fine. This worker scans the data table directly and surfaces the
// honest signal as a Prometheus gauge that operators (and an
// alert rule) can act on.
//
// Failure semantics: a transient Postgres error on a single
// detector cycle does NOT clear the gauges — the last-known value
// stays put. Operators rely on the paired
// `ratesengine_ingest_gap_detector_runs_total{outcome=error}`
// counter to detect a sustained detector outage (e.g. Postgres
// down for >24 h).
//
// First scan runs immediately on goroutine start so the gauges are
// populated before the first interval tick — a process that's just
// come up has a non-empty signal within ~3 s rather than ~5 min.
func RunGapDetector(ctx context.Context, store *Store, logger *slog.Logger) error {
	if store == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	runOneGapDetectorCycle(ctx, store, logger)

	ticker := time.NewTicker(GapDetectorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			runOneGapDetectorCycle(ctx, store, logger)
		}
	}
}

// runOneGapDetectorCycle is one scan + metric-emission pass.
// Separated from RunGapDetector so the cycle is unit-testable
// (the test wires a real Store via testcontainers + asserts the
// gauges directly).
func runOneGapDetectorCycle(ctx context.Context, store *Store, logger *slog.Logger) {
	const source = "soroban-events"
	start := time.Now()
	scanCtx, cancel := context.WithTimeout(ctx, gapDetectorTimeout)
	defer cancel()

	tip, err := resolveGapDetectorTip(scanCtx, store)
	if err != nil {
		obs.IngestGapDetectorRunsTotal.WithLabelValues(source, "error").Inc()
		obs.IngestGapDetectorDurationSeconds.WithLabelValues(source, "error").Observe(time.Since(start).Seconds())
		logger.Warn("gap-detector: tip resolve failed", "err", err)
		return
	}

	gaps, err := store.FindSorobanEventsLedgerGaps(scanCtx, 0, tip, GapDetectorMinGapSize)
	if err != nil {
		obs.IngestGapDetectorRunsTotal.WithLabelValues(source, "error").Inc()
		obs.IngestGapDetectorDurationSeconds.WithLabelValues(source, "error").Observe(time.Since(start).Seconds())
		logger.Warn("gap-detector: scan failed", "err", err, "tip", tip)
		return
	}

	var totalMissing, largest int64
	for _, g := range gaps {
		totalMissing += g.Size
		if g.Size > largest {
			largest = g.Size
		}
	}

	obs.IngestGapLedgers.WithLabelValues(source).Set(float64(totalMissing))
	obs.IngestGapCount.WithLabelValues(source).Set(float64(len(gaps)))
	obs.IngestGapMaxSize.WithLabelValues(source).Set(float64(largest))
	obs.IngestGapDetectorRunsTotal.WithLabelValues(source, "ok").Inc()
	obs.IngestGapDetectorDurationSeconds.WithLabelValues(source, "ok").Observe(time.Since(start).Seconds())

	if totalMissing > 0 {
		logger.Warn("gap-detector: data-coverage gaps detected",
			"source", source,
			"tip", tip,
			"total_missing_ledgers", totalMissing,
			"gap_count", len(gaps),
			"max_gap_size", largest,
		)
	} else {
		logger.Debug("gap-detector: clean coverage", "source", source, "tip", tip)
	}
}

// resolveGapDetectorTip reads the live ledgerstream cursor's
// last_ledger as the scan's upper bound. Used in lieu of "scan
// to MAX(ledger) in soroban_events" because that would silently
// scan ABOVE tip if soroban_events has stale rows from a previous
// test fixture; using the cursor is the authoritative "what's
// the live tip right now" answer.
//
// Returns 0 if no live cursor row exists (test fixture / region
// without live ingest); the caller's [FindSorobanEventsLedgerGaps]
// is safe at to=0 → it returns no gaps because the WHERE filter
// yields zero rows. The detector still emits a runs_total
// increment so operators can tell the worker is alive and just
// has nothing to scan.
func resolveGapDetectorTip(ctx context.Context, store *Store) (int64, error) {
	c, err := store.GetCursor(ctx, "ledgerstream", "")
	if err != nil {
		// ErrNotFound is OK — no live cursor yet means the worker
		// has nothing to scan. Any other error is real.
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return int64(c.LastLedger), nil
}
