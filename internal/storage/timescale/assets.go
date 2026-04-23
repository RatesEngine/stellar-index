package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// DistinctAssets returns one page of assets that have appeared in
// the trades hypertable (as base OR quote). Cursor-based pagination
// keyed on the asset-id string. Empty cursor starts from the
// beginning. limit is clamped to [1, 500].
//
// Returns (assets, nextCursor, err). nextCursor is empty when the
// page is the last one.
//
// Performance note: this UNIONs two DISTINCT scans across the
// trades hypertable — it works but isn't fast once we have
// millions of trades. The planned optimisation is a materialised
// `asset_catalogue` table populated incrementally by the indexer
// (TODO(#0) — migrations/0004). Until then, this is correct.
func (s *Store) DistinctAssets(ctx context.Context, cursor string, limit int) ([]canonical.Asset, string, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	const q = `
        SELECT asset FROM (
            SELECT DISTINCT base_asset  AS asset FROM trades
            UNION
            SELECT DISTINCT quote_asset AS asset FROM trades
        ) s
        WHERE ($1 = '' OR asset > $1)
        ORDER BY asset
        LIMIT $2
    `
	// We ask for one extra row to detect whether another page
	// exists — if we get (limit + 1) rows, the first `limit` are
	// the page and the last row's asset-id is the next cursor.
	rows, err := s.db.QueryContext(ctx, q, cursor, limit+1)
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
