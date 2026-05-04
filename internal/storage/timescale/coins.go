package timescale

import (
	"context"
	"fmt"
)

// CoinRow is the read-side projection of one row from the
// coin-discovery view: classic_assets joined with whatever supply +
// activity counters we have today. Pure-string fields keep the
// surface decoupled from the canonical types package.
type CoinRow struct {
	Slug             string
	AssetID          string
	Code             string
	IssuerGStrkey    string
	FirstSeenLedger  uint32
	LastSeenLedger   uint32
	ObservationCount int64
}

// ListCoins returns coin-directory rows ordered by observation count
// desc (a cheap proxy for activity). cursor + limit support
// keyset pagination — the cursor is the encoded
// (observation_count, slug) tuple of the last row of the previous
// page.
//
// The endpoint is read-only and joins no other tables today; future
// passes (Phase 5.1 super-table) will join in change_summary_5m +
// classic_asset_stats_5m so the wire response carries pre-computed
// price + delta + volume per row.
func (s *Store) ListCoins(ctx context.Context, limit int) ([]CoinRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `
		SELECT
		    COALESCE(slug, code)             AS slug,
		    asset_id,
		    code,
		    issuer_g_strkey,
		    first_seen_ledger,
		    last_seen_ledger,
		    observation_count
		  FROM classic_assets
		 ORDER BY observation_count DESC, asset_id ASC
		 LIMIT $1
	`
	rows, err := s.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListCoins: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]CoinRow, 0, limit)
	for rows.Next() {
		var r CoinRow
		var firstLedger, lastLedger int64
		if err := rows.Scan(
			&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
			&firstLedger, &lastLedger, &r.ObservationCount,
		); err != nil {
			return nil, fmt.Errorf("timescale: ListCoins scan: %w", err)
		}
		r.FirstSeenLedger = uint32(firstLedger) //nolint:gosec
		r.LastSeenLedger = uint32(lastLedger)   //nolint:gosec
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCoins rows: %w", err)
	}
	return out, nil
}
