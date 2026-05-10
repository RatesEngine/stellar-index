package timescale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// FreezeEventSink is the timescale-backed implementation of
// freeze.EventSink. Records every clear→firing transition into the
// `freeze_events` hypertable so the explorer /anomalies timeline
// has durable history.
//
// Idempotent on the (asset, quote) currently-firing row: if a row
// with recovered_at IS NULL already exists for the pair, RecordFreeze
// is a no-op. The pre-existing Redis-marker write that drives the
// API's flags.frozen field stays the source-of-truth for liveness;
// this struct only mirrors the transitions for offline reads.
type FreezeEventSink struct {
	db        *sql.DB
	clock     func() time.Time
	getLedger LedgerProvider
}

// LedgerProvider is the seam for reading the most-recently-ingested
// ledger sequence. Used to stamp `frozen_at_ledger` on inserts so
// the timeline can be re-anchored on a specific ledger range later.
//
// Implementations must be concurrent-safe and cheap (it's called on
// the aggregator's hot path). A typical implementation reads an
// atomic int that the indexer updates on every cursor advance.
type LedgerProvider interface {
	LatestLedger() uint32
}

// NewFreezeEventSink constructs the sink. clock + getLedger are
// optional; nil clock falls back to time.Now, nil getLedger means
// frozen_at_ledger is stamped 0 (acceptable for tests; production
// always wires a real provider).
func NewFreezeEventSink(s *Store, opts ...FreezeEventSinkOption) *FreezeEventSink {
	sink := &FreezeEventSink{
		db:    s.db,
		clock: time.Now,
	}
	for _, opt := range opts {
		opt(sink)
	}
	return sink
}

// FreezeEventSinkOption tunes a FreezeEventSink at construction.
type FreezeEventSinkOption func(*FreezeEventSink)

// WithFreezeClock injects a deterministic clock for tests.
func WithFreezeClock(clock func() time.Time) FreezeEventSinkOption {
	return func(s *FreezeEventSink) {
		s.clock = clock
	}
}

// WithFreezeLedgerProvider wires the ledger seam so inserts capture
// frozen_at_ledger.
func WithFreezeLedgerProvider(p LedgerProvider) FreezeEventSinkOption {
	return func(s *FreezeEventSink) {
		s.getLedger = p
	}
}

// RecordFreeze implements freeze.EventSink.
//
// Idempotent: if a row already exists for (asset, quote) with
// recovered_at IS NULL, this is a no-op (the pair is already
// recorded as currently-firing; another Mark call is just a TTL
// refresh from the orchestrator's perspective). Otherwise INSERT
// a new row.
//
// Implementation note: the idempotency check + INSERT happen in the
// same transaction so two concurrent Mark calls for the same pair
// can't both insert. The (asset_id, quote_id, frozen_at) PK has
// timestamp resolution; if two callers try to insert at the
// identical microsecond, one wins on PK-conflict and the other
// silently no-ops via ON CONFLICT DO NOTHING.
func (s *FreezeEventSink) RecordFreeze(ctx context.Context, asset, quote canonical.Asset, decision anomaly.Decision) error {
	now := s.clock().UTC()
	var ledger uint32
	if s.getLedger != nil {
		ledger = s.getLedger.LatestLedger()
	}

	detail, err := encodeFreezeDetail(decision)
	if err != nil {
		return fmt.Errorf("timescale: RecordFreeze: encode detail: %w", err)
	}

	// Translate the anomaly Decision into the table's reason CHECK.
	// Phase 1 deviations + Phase 2 confidence-based decisions both
	// land here; we expose the most-specific reason we have.
	reason := mapFreezeReason(decision)

	const q = `
		INSERT INTO freeze_events (
		    asset_id, quote_id,
		    frozen_at, frozen_at_ledger,
		    reason, frozen_value,
		    detail
		)
		SELECT $1, $2, $3, $4, $5, $6, $7
		WHERE NOT EXISTS (
		    SELECT 1 FROM freeze_events
		    WHERE asset_id = $1 AND quote_id = $2 AND recovered_at IS NULL
		)
		ON CONFLICT (asset_id, quote_id, frozen_at) DO NOTHING
	`
	if _, err := s.db.ExecContext(ctx, q,
		asset.String(), quote.String(),
		now, int64(ledger),
		reason,
		// frozen_value: the Decision shape doesn't carry the bucket
		// VWAP we're freezing on. Pass 0 for now; recovered via the
		// previous-bucket lookup at API serve time. Future improvement:
		// extend Decision to include the LKG value so the sink can
		// stamp it directly.
		0,
		detail,
	); err != nil {
		return fmt.Errorf("timescale: RecordFreeze %s/%s: %w",
			asset.String(), quote.String(), err)
	}
	return nil
}

// MarkRecovered closes out the currently-firing row for (asset,
// quote). Called by a recovery worker (or the aggregator when it
// detects a previously-frozen pair has cleared) — NOT by the
// freeze.Writer.Mark path.
//
// Idempotent: if no open row exists, returns ErrNotFound. Caller
// can swallow and continue.
func (s *FreezeEventSink) MarkRecovered(ctx context.Context, asset, quote canonical.Asset) error {
	now := s.clock().UTC()
	var ledger uint32
	if s.getLedger != nil {
		ledger = s.getLedger.LatestLedger()
	}

	const q = `
		UPDATE freeze_events
		   SET recovered_at        = $3,
		       recovered_at_ledger = $4
		 WHERE asset_id = $1 AND quote_id = $2 AND recovered_at IS NULL
	`
	res, err := s.db.ExecContext(ctx, q,
		asset.String(), quote.String(), now, int64(ledger))
	if err != nil {
		return fmt.Errorf("timescale: MarkRecovered %s/%s: %w",
			asset.String(), quote.String(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("timescale: MarkRecovered %s/%s: rows affected: %w",
			asset.String(), quote.String(), err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// encodeFreezeDetail captures the Decision's diagnostic context as
// the freeze_events.detail jsonb. Loose schema by design — different
// freeze paths (Phase 1 class-deviation, Phase 2 multi-signal) carry
// different fields and we want to preserve them all without a
// migration per addition.
func encodeFreezeDetail(decision anomaly.Decision) ([]byte, error) {
	if decision.Reason == "" && decision.DeviationPct == 0 && decision.Class == "" {
		return nil, nil
	}
	d := map[string]any{
		"action":        string(decision.Action),
		"class":         string(decision.Class),
		"deviation_pct": decision.DeviationPct,
		"reason":        decision.Reason,
	}
	return json.Marshal(d)
}

// mapFreezeReason translates the Decision into one of the values
// allowed by the freeze_events.reason CHECK constraint.
func mapFreezeReason(decision anomaly.Decision) string {
	// Phase 2 freezes carry "phase2:..." in Reason — currently
	// surfaced as 'divergence' (multi-source disagreement is the
	// driver). Phase 1 single-source / outlier paths fall through
	// to the default mapping.
	if len(decision.Reason) > 7 && decision.Reason[:7] == "phase2:" {
		return "divergence"
	}
	if decision.Action == anomaly.ActionFreeze {
		return "outlier_storm"
	}
	return "manual"
}

// noteForLogger returns nil because the log-on-failure semantics are
// already handled by the freeze.Writer wrapper. Exposed for tests
// that want to assert the sink swallows errors gracefully.
//
// (Currently unreferenced in production code; retained for future
// use when the recovery worker lands.)
//
//nolint:unused // referenced from tests
func noteForLogger(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
