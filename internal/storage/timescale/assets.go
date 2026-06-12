package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DistinctAssets returns one page of assets that have appeared in
// the trades hypertable (as base OR quote) within the last
// [MarketsRecencyWindow] (14 days by default). Cursor-based
// pagination keyed on the asset-id string. Empty cursor starts
// from the beginning. limit is clamped to [1, 500].
//
// Returns (assets, nextCursor, err). nextCursor is empty when the
// page is the last one.
//
// Recency window: matches /v1/markets's "active assets" semantic.
// Without the window the UNIONed DISTINCT scans run across every
// chunk in the trades hypertable (539M+ rows on r1) — measured at
// 4-5 minutes per call, far past any client deadline. With the
// 14-day cap the scan touches ~1.5M rows and finishes inside the
// 30s API budget. Pre-2026-05-04 the unbounded query ran every
// /v1/assets call; the recency cap brings the endpoint into the
// SLA range without a new materialised table. The planned
// optimisation is a materialised `asset_catalogue` populated
// incrementally by the indexer (future migration; not on main
// today) — that would let us drop the recency bound entirely.
func (s *Store) DistinctAssets(ctx context.Context, cursor string, limit int) ([]canonical.Asset, string, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	// `since` is computed Go-side rather than `NOW() - INTERVAL`
	// so the planner sees a constant timestamp parameter and prunes
	// chunks at plan time. Same trick the markets query uses.
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	const q = `
        SELECT asset FROM (
            SELECT DISTINCT base_asset  AS asset FROM trades WHERE ts >= $3
            UNION
            SELECT DISTINCT quote_asset AS asset FROM trades WHERE ts >= $3
        ) s
        WHERE ($1 = '' OR asset > $1)
        ORDER BY asset
        LIMIT $2
    `
	// We ask for one extra row to detect whether another page
	// exists — if we get (limit + 1) rows, the first `limit` are
	// the page and the last row's asset-id is the next cursor.
	rows, err := s.db.QueryContext(ctx, q, cursor, limit+1, since)
	if err != nil {
		return nil, "", fmt.Errorf("timescale: DistinctAssets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]canonical.Asset, 0, limit)
	hasMore := false
	n := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, "", fmt.Errorf("timescale: DistinctAssets scan: %w", err)
		}
		n++
		if n > limit {
			// Extra row — not returned; it only tells us another page
			// exists. The nextCursor below is still the last row IN
			// the page so the next query resumes via `asset > cursor`.
			hasMore = true
			break
		}
		parsed, perr := canonical.ParseAsset(raw)
		if perr != nil {
			return nil, "", fmt.Errorf("timescale: DistinctAssets parse %q: %w", raw, perr)
		}
		out = append(out, parsed)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("timescale: DistinctAssets rows: %w", err)
	}

	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = out[len(out)-1].String()
	}
	return out, nextCursor, nil
}

// HasAsset reports whether the asset appears anywhere in the trades
// hypertable. Cheap existence check — doesn't page through data.
//
// Returns (true, nil) for known asset; (false, nil) for unknown;
// (_, err) for a query failure.
//
// Dispatch:
//
//   - AssetClassic: PK lookup on `classic_assets`. The registry table
//     has one row per (code, issuer) ever observed and a primary key
//     on `asset_id`; an unknown classic asset costs one index seek.
//     Bypasses the trades hypertable entirely. F-0157 perf
//     (audit-2026-05-26): pre-fix `/v1/assets/AAAA-G…` cold path was
//     4-5 s because the `WHERE base_asset = $1 OR quote_asset = $1`
//     across 2.7 B trades rows had to seek every chunk's index.
//   - All other types (native / soroban / fiat / crypto / rwa): fall
//     back to the original trades-scan path. Native + fiat/crypto/rwa
//     canonical forms are always-known so the scan finds matches
//     fast; Soroban contracts hit the same OR scan but typical r1
//     contract count is bounded.
func (s *Store) HasAsset(ctx context.Context, a canonical.Asset) (bool, error) {
	if a.Type == canonical.AssetClassic {
		return s.hasClassicAsset(ctx, a)
	}
	return s.hasAssetByTradesScan(ctx, a)
}

// hasClassicAsset is the F-0157-perf fast path: PK lookup on
// classic_assets. The registry was specifically designed (migration
// 0023) as "the catalogue of every classic asset ever observed,"
// populated by the trade-insert hook via
// `Store.registerClassicAssetSeen`. So the asset's presence in
// classic_assets is a strict subset of its presence in trades —
// which means an asset_id NOT in classic_assets has no trades
// either, and we can short-circuit without touching the hypertable.
func (s *Store) hasClassicAsset(ctx context.Context, a canonical.Asset) (bool, error) {
	const q = `SELECT EXISTS (SELECT 1 FROM classic_assets WHERE asset_id = $1)`
	var exists bool
	err := s.db.QueryRowContext(ctx, q, a.String()).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("timescale: hasClassicAsset: %w", err)
	}
	return exists, nil
}

func (s *Store) hasAssetByTradesScan(ctx context.Context, a canonical.Asset) (bool, error) {
	const q = `
        SELECT EXISTS (
            SELECT 1 FROM trades
            WHERE base_asset = $1 OR quote_asset = $1
            LIMIT 1
        )
    `
	var exists bool
	err := s.db.QueryRowContext(ctx, q, a.String()).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("timescale: HasAsset: %w", err)
	}
	return exists, nil
}
