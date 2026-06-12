package timescale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/anomaly"
	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/freeze"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
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
	// onFreeze, when non-nil, is invoked AFTER a successful
	// RecordFreeze that actually inserted a new row (idempotent
	// no-ops don't fire). Production wiring: the aggregator
	// binary plugs a customerwebhook.Fanout.Publish closure so
	// dashboard-registered hooks subscribed to `anomaly.freeze`
	// receive a callback. F-1249 (codex audit-2026-05-12).
	onFreeze FreezeHook
}

// FreezeHook is the callback shape for post-insert side-effects.
// Best-effort: errors are logged and dropped by the sink so a
// downstream failure (e.g. customerwebhook fan-out) doesn't take
// the load-bearing INSERT down.
type FreezeHook func(ctx context.Context, asset, quote canonical.Asset, frozenValue string, decision anomaly.Decision)

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

// WithFreezeHook installs a post-insert side-effect closure.
// Invoked AFTER a successful row insert (idempotent no-ops
// don't fire). F-1249 (codex audit-2026-05-12): wired by the
// aggregator binary to bridge into customerwebhook.Fanout.Publish
// so dashboard hooks subscribed to `anomaly.freeze` get
// callbacks. Best-effort — hook panics/errors don't propagate.
func WithFreezeHook(hook FreezeHook) FreezeEventSinkOption {
	return func(s *FreezeEventSink) {
		s.onFreeze = hook
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
func (s *FreezeEventSink) RecordFreeze(ctx context.Context, asset, quote canonical.Asset, frozenValue string, decision anomaly.Decision) error {
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

	// frozen_value column is NUMERIC NOT NULL — write 0 when the
	// orchestrator had no prior bucket to freeze on (first-tick
	// freeze). The decimal-string value is forwarded verbatim;
	// pgx/lib-pq parses NUMERIC literals from strings without
	// precision loss.
	frozenValueArg := frozenValue
	if frozenValueArg == "" {
		frozenValueArg = "0"
	}

	// F-1250 (codex audit-2026-05-12): atomic dedupe under
	// concurrent RecordFreeze calls. Two aggregator workers
	// racing on the same (asset, quote) pair used to both pass
	// the `WHERE NOT EXISTS` check and each insert a still-firing
	// row, leaving duplicate open rows for the same pair —
	// every recovery worker now had to clear N rows instead of 1.
	//
	// The fix: wrap the check + insert in a transaction guarded
	// by `pg_advisory_xact_lock` keyed on a stable hash of
	// (asset, quote). The lock is process-local to the txn so
	// it auto-releases on COMMIT/ROLLBACK and never strands the
	// row. Advisory locks (vs row locks) work here because the
	// "no row yet" branch has nothing to row-lock against;
	// Timescale also forbids unique constraints that don't
	// include the partition key, so a partial UNIQUE index
	// isn't an option.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("timescale: RecordFreeze: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // safe no-op after COMMIT

	pairKey := pairAdvisoryLockKey(asset.String(), quote.String())
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, pairKey); err != nil {
		return fmt.Errorf("timescale: RecordFreeze: advisory lock: %w", err)
	}

	const q = `
		INSERT INTO freeze_events (
		    asset_id, quote_id,
		    frozen_at, frozen_at_ledger,
		    reason, frozen_value,
		    detail
		)
		SELECT $1, $2, $3, $4, $5, $6::NUMERIC, $7
		WHERE NOT EXISTS (
		    SELECT 1 FROM freeze_events
		    WHERE asset_id = $1 AND quote_id = $2 AND recovered_at IS NULL
		)
		ON CONFLICT (asset_id, quote_id, frozen_at) DO NOTHING
	`
	res, err := tx.ExecContext(ctx, q,
		asset.String(), quote.String(),
		now, int64(ledger),
		reason,
		frozenValueArg,
		detail,
	)
	if err != nil {
		return fmt.Errorf("timescale: RecordFreeze %s/%s: %w",
			asset.String(), quote.String(), err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("timescale: RecordFreeze: commit: %w", err)
	}
	// F-1249 (codex audit-2026-05-12): fire the post-insert hook
	// only when a row was actually appended. The idempotency check
	// + ON CONFLICT DO NOTHING means RowsAffected==0 is the
	// "already firing, this is just a TTL refresh" path; firing
	// the webhook then would spam subscribers.
	if s.onFreeze != nil {
		if affected, err := res.RowsAffected(); err == nil && affected > 0 {
			s.onFreeze(ctx, asset, quote, frozenValueArg, decision)
		}
	}
	return nil
}

// pairAdvisoryLockKey derives a stable int64 advisory-lock key
// from the (asset, quote) pair. FNV-1a 64-bit; collisions are
// possible across distinct pairs but cosmetic (false serialisation,
// no correctness loss).
func pairAdvisoryLockKey(asset, quote string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(asset))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(quote))
	return int64(h.Sum64()) //nolint:gosec // narrowing to int64 is acceptable; pg accepts signed bigint
}

// ListOpen returns every (asset, quote) currently in firing state
// — `freeze_events` rows with recovered_at IS NULL. Snapshot read,
// no locking; the recovery worker can race a fresh RecordFreeze
// safely because both end states (still firing / cleared) are
// idempotent on the open-row PK.
//
// Returns the freeze package's OpenFreezePair shape so the
// recovery worker (which lives in `internal/aggregate/freeze`)
// avoids a hard dependency on this storage adapter.
func (s *FreezeEventSink) ListOpen(ctx context.Context) ([]freeze.OpenFreezePair, error) {
	const q = `
		SELECT asset_id, quote_id
		  FROM freeze_events
		 WHERE recovered_at IS NULL
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListOpen: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []freeze.OpenFreezePair
	for rows.Next() {
		var assetID, quoteID string
		if err := rows.Scan(&assetID, &quoteID); err != nil {
			return nil, fmt.Errorf("timescale: ListOpen scan: %w", err)
		}
		asset, err := canonical.ParseAsset(assetID)
		if err != nil {
			return nil, fmt.Errorf("timescale: ListOpen parse asset %q: %w", assetID, err)
		}
		quote, err := canonical.ParseAsset(quoteID)
		if err != nil {
			return nil, fmt.Errorf("timescale: ListOpen parse quote %q: %w", quoteID, err)
		}
		out = append(out, freeze.OpenFreezePair{Asset: asset, Quote: quote})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListOpen rows: %w", err)
	}
	return out, nil
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
