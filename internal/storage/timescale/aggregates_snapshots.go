package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ─── rewritten_vwap_snapshots storage methods (ADR-0025 phase 1, Option B) ─
//
// Mirror of the Redis vwap: surface for the rewritten-pair
// rolling-window snapshots shipped in the Option B variant of
// migration 0027. See ADR-0025 for the bucketed-vs-snapshot tradeoff
// and the original CLAUDE.md / ADR-0014 invariant on
// "stablecoin-fiat-proxy is aggregator policy."
//
// All exported methods carry "Snapshot" in their names so the call
// sites read explicitly: the literal-pair path (prices_1m via
// LatestClosedVWAP1mForPair) and the snapshot path are kept
// visually distinct.

// VwapSnapshotRow is one row in rewritten_vwap_snapshots.
//
// VWAP / Volume / VolumeUSD are decimal strings (NUMERIC at the
// postgres layer); ADR-0003 mandates string-based handling at the
// boundary so a float round-trip never loses precision.
type VwapSnapshotRow struct {
	ObservedAt    time.Time
	BaseAsset     string
	QuoteAsset    string
	WindowSeconds int
	VWAP          string
	Volume        string
	VolumeUSD     string // empty when no contributing trade had usd_volume
	TradeCount    int64
	Sources       []string
}

// InsertRewrittenVWAPSnapshot writes one snapshot row. Append-only
// in the common case (one row per (pair, window) per orchestrator
// tick); if the orchestrator restarts and re-fires a tick at the
// exact same observed_at the ON CONFLICT clause makes the second
// write a no-op rather than fail. The PK is
// (base, quote, window, observed_at) — the conflict is rare, but
// not impossible under restart-replay.
//
// Phase 2 caller: orchestrator.cacheClosedBucket.
func (s *Store) InsertRewrittenVWAPSnapshot(ctx context.Context, row VwapSnapshotRow) error {
	if row.VWAP == "" {
		return fmt.Errorf("timescale: InsertRewrittenVWAPSnapshot: VWAP is required")
	}
	if row.WindowSeconds <= 0 {
		return fmt.Errorf("timescale: InsertRewrittenVWAPSnapshot: window_seconds must be > 0, got %d", row.WindowSeconds)
	}
	const q = `
        INSERT INTO rewritten_vwap_snapshots (
            observed_at, base_asset, quote_asset, window_seconds,
            vwap, volume, volume_usd, trade_count, sources
        ) VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::numeric,$8,$9)
        ON CONFLICT (base_asset, quote_asset, window_seconds, observed_at)
        DO NOTHING
    `
	if _, err := s.db.ExecContext(ctx, q,
		row.ObservedAt.UTC(),
		row.BaseAsset,
		row.QuoteAsset,
		row.WindowSeconds,
		row.VWAP,
		row.Volume,
		row.VolumeUSD,
		row.TradeCount,
		stringArray(row.Sources),
	); err != nil {
		return fmt.Errorf("timescale: InsertRewrittenVWAPSnapshot %s/%s/%d@%s: %w",
			row.BaseAsset, row.QuoteAsset, row.WindowSeconds,
			row.ObservedAt.UTC().Format(time.RFC3339), err)
	}
	return nil
}

// LatestRewrittenVWAPSnapshot returns the most-recent snapshot for
// the given (pair, window). The "latest" is the row with the highest
// observed_at; the closed-bucket guard from prices_1m doesn't apply
// here because rolling-window VWAPs are by definition computed
// against ALREADY-CLOSED trade data, not against a per-bucket
// open/close boundary.
//
// Returns [sql.ErrNoRows] when no snapshot exists for the pair and
// window. Phase 3 caller: storePriceReader.LatestPrice (one branch
// of the fallback chain).
func (s *Store) LatestRewrittenVWAPSnapshot(ctx context.Context, p canonical.Pair, window time.Duration) (VwapSnapshotRow, error) {
	const q = `
        SELECT observed_at, base_asset, quote_asset, window_seconds,
               vwap::text, volume::text, COALESCE(volume_usd::text, ''),
               trade_count, sources
          FROM rewritten_vwap_snapshots
         WHERE base_asset    = $1
           AND quote_asset   = $2
           AND window_seconds = $3
         ORDER BY observed_at DESC
         LIMIT 1
    `
	var row VwapSnapshotRow
	err := s.db.QueryRowContext(ctx, q,
		p.Base.String(), p.Quote.String(), int(window.Seconds()),
	).Scan(
		&row.ObservedAt,
		&row.BaseAsset,
		&row.QuoteAsset,
		&row.WindowSeconds,
		&row.VWAP,
		&row.Volume,
		&row.VolumeUSD,
		&row.TradeCount,
		(*stringArray)(&row.Sources),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return VwapSnapshotRow{}, sql.ErrNoRows
	}
	if err != nil {
		return VwapSnapshotRow{}, fmt.Errorf("timescale: LatestRewrittenVWAPSnapshot: %w", err)
	}
	if row.Sources == nil {
		row.Sources = []string{}
	}
	return row, nil
}

// RewrittenVWAPSnapshotsInRange returns chronologically-ordered
// (oldest-first) snapshots for the given (pair, window) over the
// half-open window `[from, to)`. Used by the change-summary worker
// for fiat-target entities and by /v1/history for rewritten pairs.
//
// Empty slice + nil error when the pair has no snapshots in range.
//
// limit caps the row count to bound memory under wide ranges; a
// caller asking for "all 30 days at 30s tick = 86,400 rows" can
// request that much, but the typical caller wants the most-recent
// N points so an explicit cap is required (not optional like
// the literal-pair version).
func (s *Store) RewrittenVWAPSnapshotsInRange(ctx context.Context, p canonical.Pair, window time.Duration, from, to time.Time, limit int) ([]VwapSnapshotRow, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("timescale: RewrittenVWAPSnapshotsInRange: to %v <= from %v", to, from)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("timescale: RewrittenVWAPSnapshotsInRange: limit must be > 0, got %d", limit)
	}
	const q = `
        SELECT observed_at, base_asset, quote_asset, window_seconds,
               vwap::text, volume::text, COALESCE(volume_usd::text, ''),
               trade_count, sources
          FROM rewritten_vwap_snapshots
         WHERE base_asset    = $1
           AND quote_asset   = $2
           AND window_seconds = $3
           AND observed_at  >= $4
           AND observed_at  <  $5
         ORDER BY observed_at ASC
         LIMIT $6
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), int(window.Seconds()),
		from.UTC(), to.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: RewrittenVWAPSnapshotsInRange: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]VwapSnapshotRow, 0, 64)
	for rows.Next() {
		var row VwapSnapshotRow
		if err := rows.Scan(
			&row.ObservedAt,
			&row.BaseAsset,
			&row.QuoteAsset,
			&row.WindowSeconds,
			&row.VWAP,
			&row.Volume,
			&row.VolumeUSD,
			&row.TradeCount,
			(*stringArray)(&row.Sources),
		); err != nil {
			return nil, fmt.Errorf("timescale: RewrittenVWAPSnapshotsInRange scan: %w", err)
		}
		if row.Sources == nil {
			row.Sources = []string{}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: RewrittenVWAPSnapshotsInRange rows: %w", err)
	}
	return out, nil
}
