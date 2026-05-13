package supply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// LedgerLookup is the storage-side primitive the [Refresher] uses
// to resolve "what's the most recent known chain ledger." Production
// impl wraps timescale.Store.ListCursors and takes the max
// last_ledger across all sources; tests pass in-memory fakes.
type LedgerLookup interface {
	LatestKnownLedger(ctx context.Context) (uint32, time.Time, error)
}

// SnapshotComputer is the supply-package primitive — computes one
// [Supply] for the given ledger. Production impl is *XLMComputer;
// classic + SEP-41 computers can plug in once they ship.
type SnapshotComputer interface {
	Compute(ctx context.Context, ledger uint32, observedAt time.Time) (Supply, error)
}

// SnapshotInserter writes one [Supply] into the persistence layer.
// Production impl is timescale.Store.InsertSupply; idempotent on
// (asset_key, ledger_sequence).
type SnapshotInserter interface {
	InsertSupply(ctx context.Context, snap Supply) error
}

// Outcome is what one [Refresher.Tick] produced. Drives the
// aggregator's Prometheus counters; OutcomeKind is a stable
// string suitable for a metric label.
type Outcome struct {
	Kind     OutcomeKind
	Snapshot Supply // populated on OutcomeKindOK only
	Err      error  // populated on every error outcome
}

// OutcomeKind identifies a refresh outcome. Values are stable
// metric-label strings.
type OutcomeKind string

const (
	OutcomeKindOK               OutcomeKind = "ok"
	OutcomeKindNoLedger         OutcomeKind = "no_ledger"         // LedgerLookup error
	OutcomeKindNoObservation    OutcomeKind = "no_observation"    // ChainReader fell through with no static fallback either
	OutcomeKindComputeError     OutcomeKind = "compute_error"     // computer failed for non-observation reasons
	OutcomeKindStaleComponent   OutcomeKind = "stale_component"   // F-1236: a component observation lags the snapshot ledger past the configured threshold
	OutcomeKindMissingFreshness OutcomeKind = "missing_freshness" // F-1236 wave 60 (codex audit-2026-05-13): strict mode + MinComponentLedger==0 (no signal); reject rather than publish without a freshness anchor
	OutcomeKindWriteError       OutcomeKind = "write_error"       // InsertSupply failed
)

// DefaultStaleComponentLedgers is the F-1236 freshness threshold
// the Refresher applies when none is operator-configured: a
// snapshot whose MinComponentLedger lags the snapshot ledger by
// more than 1000 ledgers (~85 min at 5s ledger close cadence)
// is rejected. Operators tune via [WithStaleComponentLedgers].
//
// Conservative default — most operator deployments see all
// supply observers complete within one ledger of the trade
// indexer, so 1000 is large enough to never false-reject under
// normal load while small enough to catch a genuinely stalled
// observer before the supply table accrues misleading rows.
const DefaultStaleComponentLedgers uint32 = 1000

// Refresher runs one supply-snapshot cycle per [Refresher.Tick]
// call. Composes ledger resolution + computer + inserter; the
// aggregator drives it via a ticker in its own goroutine,
// mirroring the baseline-refresher shape.
type Refresher struct {
	ledgers                 LedgerLookup
	computer                SnapshotComputer
	inserter                SnapshotInserter
	logger                  *slog.Logger
	staleComponentLedger    uint32
	strictFreshnessRequired bool
}

// RefresherOption tunes a [Refresher].
type RefresherOption func(*Refresher)

// WithStaleComponentLedgers overrides the F-1236 (codex
// audit-2026-05-12) freshness threshold. The Refresher rejects
// a snapshot when (snap.LedgerSequence - snap.MinComponentLedger)
// exceeds this value AND MinComponentLedger > 0 (zero means the
// computer didn't populate the field — legacy path stays
// unaffected). Set to 0 to disable the gate.
func WithStaleComponentLedgers(maxLag uint32) RefresherOption {
	return func(r *Refresher) {
		r.staleComponentLedger = maxLag
	}
}

// WithStrictFreshnessRequired flips the Refresher into the
// stricter F-1236 wave-60 (codex audit-2026-05-13) posture:
// a snapshot whose `MinComponentLedger == 0` is rejected with
// [OutcomeKindMissingFreshness] rather than passing the gate.
// Default false preserves the legacy permissive interpretation
// of zero ("no freshness signal — let it through") so
// deployments running the static-XLM fallback or where one of
// the freshness producers can transiently fail (Postgres
// timeout, Redis blip) keep publishing snapshots.
//
// Operators turn this on after every freshness producer is
// confirmed wired AND every reader is shown to never
// fail-open under steady-state load — typically post-launch,
// after a few weeks of green snapshot timers. Once enabled,
// the supply table only ever accumulates rows whose component
// observations are demonstrably anchored to a recent ledger.
func WithStrictFreshnessRequired(strict bool) RefresherOption {
	return func(r *Refresher) {
		r.strictFreshnessRequired = strict
	}
}

// NewRefresher constructs the Refresher.
func NewRefresher(ledgers LedgerLookup, computer SnapshotComputer, inserter SnapshotInserter, logger *slog.Logger, opts ...RefresherOption) *Refresher {
	r := &Refresher{
		ledgers:              ledgers,
		computer:             computer,
		inserter:             inserter,
		logger:               logger,
		staleComponentLedger: DefaultStaleComponentLedgers,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Tick runs one refresh cycle:
//
//  1. Resolve the latest known chain ledger.
//  2. Compute the supply at that ledger.
//  3. Insert the snapshot (idempotent on conflict).
//
// Returns an [Outcome] for metric emission. Tick does NOT bubble
// errors — it logs them and returns the outcome so the
// surrounding goroutine never crashes the aggregator's whole
// loop on a transient supply-side issue.
func (r *Refresher) Tick(ctx context.Context) Outcome {
	ledger, observedAt, err := r.ledgers.LatestKnownLedger(ctx)
	if err != nil {
		r.logger.Warn("supply refresh: no ledger", "err", err)
		return Outcome{Kind: OutcomeKindNoLedger, Err: err}
	}

	snap, err := r.computer.Compute(ctx, ledger, observedAt)
	if err != nil {
		// Distinguish the "no observation" outcome (which the
		// ChainReader surfaces with ErrNoObservation when both live
		// AND static fall through) from generic compute errors so
		// operators can chart the bootstrap-progress signal.
		kind := OutcomeKindComputeError
		if errors.Is(err, ErrNoObservation) {
			kind = OutcomeKindNoObservation
		}
		r.logger.Warn("supply refresh: compute failed",
			"err", err, "ledger", ledger, "kind", string(kind))
		return Outcome{Kind: kind, Err: err}
	}

	// F-1236 wave 60 (codex audit-2026-05-13): strict mode
	// rejects snapshots that arrive with NO freshness signal
	// (MinComponentLedger == 0), instead of the legacy
	// permissive interpretation ("no signal — let it through").
	// Default off: preserves backwards compat for deployments on
	// the static-XLM fallback or with transiently-failing
	// freshness producers. Operators turn it on once every
	// producer is wired + every reader is shown to never
	// fail-open under steady-state load.
	if r.strictFreshnessRequired && snap.MinComponentLedger == 0 {
		err := fmt.Errorf("supply: strict-freshness mode — snapshot has no MinComponentLedger anchor")
		r.logger.Warn("supply refresh: rejecting freshness-less snapshot under strict mode",
			"asset", snap.AssetKey,
			"snapshot_ledger", snap.LedgerSequence)
		return Outcome{Kind: OutcomeKindMissingFreshness, Err: err, Snapshot: snap}
	}

	// F-1236 (codex audit-2026-05-12): reject snapshots whose
	// per-component observations lag the snapshot ledger by more
	// than the configured threshold. MinComponentLedger == 0
	// means the computer didn't populate the field (legacy
	// path); we don't gate in that case so deployments without
	// freshness-aware computers stay on the pre-F-1236 posture.
	if r.staleComponentLedger > 0 && snap.MinComponentLedger > 0 {
		if snap.LedgerSequence > snap.MinComponentLedger &&
			snap.LedgerSequence-snap.MinComponentLedger > r.staleComponentLedger {
			err := fmt.Errorf("supply: stale component — snapshot ledger %d, min component ledger %d, gap %d > threshold %d",
				snap.LedgerSequence, snap.MinComponentLedger,
				snap.LedgerSequence-snap.MinComponentLedger, r.staleComponentLedger)
			r.logger.Warn("supply refresh: rejecting stale-component snapshot",
				"asset", snap.AssetKey,
				"snapshot_ledger", snap.LedgerSequence,
				"min_component_ledger", snap.MinComponentLedger,
				"gap", snap.LedgerSequence-snap.MinComponentLedger,
				"threshold", r.staleComponentLedger)
			return Outcome{Kind: OutcomeKindStaleComponent, Err: err, Snapshot: snap}
		}
	}

	if err := r.inserter.InsertSupply(ctx, snap); err != nil {
		r.logger.Error("supply refresh: insert failed",
			"err", err, "asset", snap.AssetKey, "ledger", snap.LedgerSequence)
		return Outcome{Kind: OutcomeKindWriteError, Err: err, Snapshot: snap}
	}

	r.logger.Debug("supply refresh ok",
		"asset", snap.AssetKey,
		"ledger", snap.LedgerSequence,
		"circulating", snap.CirculatingSupply.String())
	return Outcome{Kind: OutcomeKindOK, Snapshot: snap}
}

// String renders the outcome for log lines / test fixtures. Stable
// across versions.
func (o Outcome) String() string {
	if o.Err != nil {
		return fmt.Sprintf("%s: %v", o.Kind, o.Err)
	}
	return string(o.Kind)
}
