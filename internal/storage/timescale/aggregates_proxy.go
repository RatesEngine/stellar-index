package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ─── prices_1m_proxy storage methods (ADR-0025 phase 1) ───────────
//
// Mirror of the prices_1m read+write surface for the rewritten-pair
// hypertable shipped in migration 0027. See ADR-0025 for the design
// space; CLAUDE.md / ADR-0014 for the "stablecoin-fiat-proxy is
// aggregator policy" invariant that motivates the separate table.
//
// All three exported methods carry "Proxy" suffix to match the table
// name and to make the call-site decision visible (the read path
// chooses literal-vs-proxy explicitly, not via a hidden union).

// Vwap1mProxyRow is one row in prices_1m_proxy. Same shape as
// [Vwap1mRow] minus the OHLC fields (rewritten-pair OHLC is
// post-launch — see migration 0027 commentary).
type Vwap1mProxyRow struct {
	Bucket     time.Time
	BaseAsset  string
	QuoteAsset string
	VWAP       string
	TWAP       string // empty when the source aggregator didn't compute it
	Volume     string
	VolumeUSD  string // empty when the contributing trades carried no usd_volume
	TradeCount int64
	Sources    []string
}

// UpsertProxyVWAP writes one rewritten-pair VWAP row. Idempotent:
// if a row already exists for (bucket, base, quote) it's updated
// with the new values (e.g. on aggregator restart re-running a
// tick that landed mid-write last time).
//
// Called by the aggregator orchestrator after each closed-bucket
// VWAP write that targeted a fiat-quoted pair via the
// stablecoin-fiat-proxy expansion. Failure is the caller's to
// log + count — the storage method just returns the wrapped error.
//
// Phase 1 ships the method but no caller yet. Phase 2 wires it
// into orchestrator.cacheClosedBucket.
func (s *Store) UpsertProxyVWAP(ctx context.Context, row Vwap1mProxyRow) error {
	if row.VWAP == "" {
		return fmt.Errorf("timescale: UpsertProxyVWAP: VWAP is required")
	}
	const q = `
        INSERT INTO prices_1m_proxy (
            bucket, base_asset, quote_asset,
            vwap, twap, volume, volume_usd, trade_count, sources
        ) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,'')::numeric,$8,$9)
        ON CONFLICT (bucket, base_asset, quote_asset) DO UPDATE SET
            vwap        = EXCLUDED.vwap,
            twap        = EXCLUDED.twap,
            volume      = EXCLUDED.volume,
            volume_usd  = EXCLUDED.volume_usd,
            trade_count = EXCLUDED.trade_count,
            sources     = EXCLUDED.sources
    `
	if _, err := s.db.ExecContext(ctx, q,
		row.Bucket.UTC(),
		row.BaseAsset,
		row.QuoteAsset,
		row.VWAP,
		row.TWAP,
		row.Volume,
		row.VolumeUSD,
		row.TradeCount,
		stringArray(row.Sources),
	); err != nil {
		return fmt.Errorf("timescale: UpsertProxyVWAP %s/%s@%s: %w",
			row.BaseAsset, row.QuoteAsset, row.Bucket.UTC().Format(time.RFC3339), err)
	}
	return nil
}

// LatestClosedVWAP1mForPairProxy returns the most-recent closed
// rewritten-pair row, or [sql.ErrNoRows] when the pair has no rows
// yet. Mirror of [LatestClosedVWAP1mForPair] for the proxy table.
//
// The closed-bucket guard (`bucket + 1 min <= now()`) matches the
// literal-pair query so a caller checking both tables can treat
// the results as a single time-coherent series.
//
// Phase 3 wires this into the API price-reader fallback chain
// (prices_1m → prices_1m_proxy → Redis vwap: → trade-table).
func (s *Store) LatestClosedVWAP1mForPairProxy(ctx context.Context, p canonical.Pair) (Vwap1mRow, error) {
	const q = `
        SELECT bucket, base_asset, quote_asset, vwap::text, trade_count, sources
          FROM prices_1m_proxy
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket + INTERVAL '1 minute' <= now()
         ORDER BY bucket DESC
         LIMIT 1
    `
	var row Vwap1mRow
	err := s.db.QueryRowContext(ctx, q,
		p.Base.String(), p.Quote.String(),
	).Scan(
		&row.Bucket,
		&row.BaseAsset,
		&row.QuoteAsset,
		&row.VWAP,
		&row.TradeCount,
		(*stringArray)(&row.Sources),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Vwap1mRow{}, sql.ErrNoRows
	}
	if err != nil {
		return Vwap1mRow{}, fmt.Errorf("timescale: LatestClosedVWAP1mForPairProxy: %w", err)
	}
	normalizeVwapSources(&row)
	return row, nil
}

// TimedVWAPs1mForChangeSummaryProxy returns chronologically-ordered
// (oldest-first) rewritten-pair VWAPs in the half-open window
// `[from, to)`. Same return shape as
// [Store.TimedVWAPs1mForChangeSummary] — uses [ChangeSummaryPoint]
// rather than [baseline.TimedVWAP] so the changesummary package
// doesn't take a baseline import. Consumed by the change-summary
// worker for fiat-target entities (where the literal-pair query
// returns empty because the literal pair has no `prices_1m` rows
// under that base/quote string).
//
// Empty slice + nil error when the pair has no rows in the window.
func (s *Store) TimedVWAPs1mForChangeSummaryProxy(ctx context.Context, p canonical.Pair, from, to time.Time) ([]ChangeSummaryPoint, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummaryProxy: to %v <= from %v", to, from)
	}
	const q = `
        SELECT bucket + INTERVAL '1 minute' AS bucket_end, vwap::text
          FROM prices_1m_proxy
         WHERE base_asset = $1
           AND quote_asset = $2
           AND bucket >= $3
           AND bucket  <  $4
         ORDER BY bucket ASC
    `
	rows, err := s.db.QueryContext(ctx, q,
		p.Base.String(), p.Quote.String(), from.UTC(), to.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummaryProxy: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]ChangeSummaryPoint, 0, 256)
	for rows.Next() {
		var p ChangeSummaryPoint
		if err := rows.Scan(&p.At, &p.Value); err != nil {
			return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummaryProxy scan: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: TimedVWAPs1mForChangeSummaryProxy rows: %w", err)
	}
	return out, nil
}
