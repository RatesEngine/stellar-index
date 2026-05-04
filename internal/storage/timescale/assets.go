package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
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
func (s *Store) HasAsset(ctx context.Context, a canonical.Asset) (bool, error) {
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
