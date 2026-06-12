// Package statsflush owns the periodic worker that exports
// dispatcher in-memory counters to the decoder_stats_5m hypertable.
//
// The dispatcher tracks events_seen / decode_errors / orphan_events
// per source as cumulative process counters. The flusher samples
// those counters every interval, computes the delta against its
// last snapshot, and writes one row per (source, bucket) tuple to
// postgres.
//
// Snapshot-and-delta (not snapshot-and-clear) is the contract:
// resetting the dispatcher's counters from outside the dispatcher
// would race with concurrent decoder writes. Computing deltas
// owner-side keeps the dispatcher untouched.
//
// Powers /v1/diagnostics/decoders + the explorer /diagnostics
// decoder-coverage table per docs/architecture/explorer-data-
// inventory.md §7.22.
package statsflush

import (
	"context"
	"log/slog"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

// StatsSource is the seam the flusher reads from. The Dispatcher
// satisfies it directly; tests can substitute a fake.
type StatsSource interface {
	Stats() dispatcher.Stats
}

// LedgerSource provides the most-recently-ingested ledger for the
// last_ledger column. Optional — nil leaves last_ledger NULL.
type LedgerSource interface {
	LatestLedger() uint32
}

// Flusher snapshots dispatcher counters every interval and writes
// per-bucket deltas to the decoder_stats_5m hypertable.
//
// Run via [Flusher.Run]; caller cancels via context.
type Flusher struct {
	source   StatsSource
	store    *timescale.Store
	logger   *slog.Logger
	interval time.Duration
	ledger   LedgerSource

	// Cumulative counters captured on the previous tick. Subtracting
	// from current values yields the delta for this bucket.
	last dispatcher.Stats
}

// Options tunes a Flusher at construction time.
type Options struct {
	// Interval between snapshots. Default 5 min — matches the
	// decoder_stats_5m hypertable name + chunk interval.
	Interval time.Duration

	// LedgerSource is consulted on every flush so each row carries
	// the latest ingested ledger. nil leaves the column NULL.
	LedgerSource LedgerSource
}

// New constructs a Flusher. Logger is required — the flusher logs
// every tick at DEBUG, plus any postgres write failures at WARN.
func New(source StatsSource, store *timescale.Store, logger *slog.Logger, opts Options) *Flusher {
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Flusher{
		source:   source,
		store:    store,
		logger:   logger,
		interval: interval,
		ledger:   opts.LedgerSource,
		last: dispatcher.Stats{
			EventsSeen:   map[string]int{},
			DecodeErrors: map[string]int{},
			OrphanEvents: map[string]int{},
		},
	}
}

// Run blocks until ctx is cancelled, flushing every interval.
// Returns nil on context cancellation; never returns an error
// (per-tick failures log + continue).
func (f *Flusher) Run(ctx context.Context) error {
	t := time.NewTicker(f.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			// One last flush before exiting so a clean shutdown
			// captures the final partial bucket.
			f.flush(ctx)
			return nil
		case now := <-t.C:
			f.flushAt(ctx, now)
		}
	}
}

// flush is the convenience wrapper around flushAt that uses
// time.Now. Used for the shutdown drain.
func (f *Flusher) flush(ctx context.Context) {
	f.flushAt(ctx, time.Now())
}

// flushAt computes the per-source delta against the last snapshot
// and writes the rows. Intentionally exported via the lowercase
// name; tests reach in via deterministic clock injection in
// New(). The bucket is floored to the configured interval so two
// independent flushers (e.g. during a leader handover) write to
// the same row.
func (f *Flusher) flushAt(ctx context.Context, now time.Time) {
	bucket := now.UTC().Truncate(f.interval)
	current := f.source.Stats()

	rows := make([]timescale.DecoderStatsBucket, 0, len(current.DecodeErrors)+len(current.OrphanEvents))
	sources := allSources(current, f.last)

	var lastLedger uint32
	if f.ledger != nil {
		lastLedger = f.ledger.LatestLedger()
	}

	for source := range sources {
		delta := timescale.DecoderStatsBucket{
			Bucket:       bucket,
			Source:       source,
			EventsSeen:   int64(current.EventsSeen[source] - f.last.EventsSeen[source]),
			DecodeErrors: int64(current.DecodeErrors[source] - f.last.DecodeErrors[source]),
			OrphanEvents: int64(current.OrphanEvents[source] - f.last.OrphanEvents[source]),
			LastLedger:   lastLedger,
		}
		// Skip rows where every counter is zero AND we have no
		// ledger context. Avoids writing meaningless zero-rows on
		// quiet sources.
		if delta.EventsSeen == 0 && delta.DecodeErrors == 0 && delta.OrphanEvents == 0 && delta.LastLedger == 0 {
			continue
		}
		rows = append(rows, delta)
	}

	// Surface dispatcher-level tx-read errors at WARN when a delta
	// appears in this flush window. The counter sits outside the
	// per-source row schema (LedgerTransactionReader.Read failures
	// aren't attributable to a source) so the statsflush hypertable
	// can't carry it; the WARN log is the canonical signal until
	// it gets promoted to a Prometheus counter.
	if delta := current.TxReadErrors - f.last.TxReadErrors; delta > 0 {
		f.logger.Warn("dispatcher: tx-read errors during this flush window",
			"delta", delta,
			"total", current.TxReadErrors,
			"window", f.interval.String(),
		)
	}

	// G15-06: a climbing tx-event-read-error count means
	// GetTransactionEvents is failing (e.g. an unsupported future meta
	// version), silently dropping every tx's Soroban events. Surfaced at
	// WARN so the break is visible rather than masquerading as clean
	// (empty) ledgers that the completeness reconcile would still pass.
	if delta := current.TxEventReadErrors - f.last.TxEventReadErrors; delta > 0 {
		f.logger.Warn("dispatcher: tx-event read errors during this flush window — Soroban events being dropped",
			"delta", delta,
			"total", current.TxEventReadErrors,
			"window", f.interval.String(),
		)
	}

	if len(rows) > 0 {
		if err := f.store.InsertDecoderStats(ctx, rows); err != nil {
			f.logger.Warn("decoder-stats flush failed", "rows", len(rows), "err", err)
		}
	}

	// Snapshot for next-tick delta computation. Make a copy of the
	// maps so concurrent dispatcher writes can't mutate our reference.
	f.last = dispatcher.Stats{
		EventsSeen:        copyIntMap(current.EventsSeen),
		DecodeErrors:      copyIntMap(current.DecodeErrors),
		OrphanEvents:      copyIntMap(current.OrphanEvents),
		UnmatchedHits:     current.UnmatchedHits,
		TxReadErrors:      current.TxReadErrors,
		TxEventReadErrors: current.TxEventReadErrors,
	}
}

// allSources returns the union of source keys present in either
// the current or the last snapshot. Lets us write a delta row
// even when a source's counter went to zero (e.g. all errors
// resolved; we want to record the "fresh data point" for the
// dashboard line).
func allSources(current, last dispatcher.Stats) map[string]struct{} {
	out := make(map[string]struct{}, len(current.EventsSeen)+len(current.DecodeErrors)+len(current.OrphanEvents))
	for k := range current.EventsSeen {
		out[k] = struct{}{}
	}
	for k := range current.DecodeErrors {
		out[k] = struct{}{}
	}
	for k := range current.OrphanEvents {
		out[k] = struct{}{}
	}
	for k := range last.EventsSeen {
		out[k] = struct{}{}
	}
	for k := range last.DecodeErrors {
		out[k] = struct{}{}
	}
	for k := range last.OrphanEvents {
		out[k] = struct{}{}
	}
	return out
}

func copyIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
