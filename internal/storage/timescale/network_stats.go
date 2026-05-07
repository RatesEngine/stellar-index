package timescale

import (
	"context"
	"database/sql"
	"fmt"
)

// NetworkStats is the aggregate counts powering the home network
// strip on ratesengine.net. Single SQL query computes them all so
// the explorer doesn't need 4 separate fetches.
type NetworkStats struct {
	// Volume24hUSD: SUM of prices_1m.volume_usd across every pair
	// over the trailing 24h. Decimal string per ADR-0003 (the
	// volume column is NUMERIC and can exceed int64 in raw cents).
	// Nil when prices_1m has no recent rows.
	Volume24hUSD *string
	// MarketsCount24h: distinct (base, quote) pairs that recorded
	// any volume_usd in the trailing 24h. ~10× larger than the
	// classic_assets count (each asset participates in many pairs).
	MarketsCount24h int64
	// AssetsIndexed: total rows in classic_assets — ~440K today.
	// Doesn't filter by recent activity; this is "what we know
	// about", not "what's currently trading".
	AssetsIndexed int64
	// LatestLedger: max(ingestion_cursors.last_ledger) across
	// non-backfill sources. Mirrors what the diagnostics page
	// surfaces; included here so the home strip doesn't need a
	// separate /v1/diagnostics/cursors call.
	LatestLedger int64
}

// GetNetworkStats returns the home-page aggregate stats in one call.
// Cheap query: hypertable-bound time filter for volume; one COUNT
// for distinct markets; one COUNT for classic_assets; one MAX for
// the live cursor.
func (s *Store) GetNetworkStats(ctx context.Context) (NetworkStats, error) {
	const q = `
		SELECT
		  (SELECT SUM(volume_usd)::text FROM prices_1m
		    WHERE bucket >= now() - INTERVAL '24 hours'
		      AND volume_usd IS NOT NULL)                      AS volume_24h_usd,
		  (SELECT COUNT(*)::bigint FROM (
		     SELECT DISTINCT base_asset, quote_asset FROM prices_1m
		      WHERE bucket >= now() - INTERVAL '24 hours'
		        AND volume_usd IS NOT NULL
		   ) t)                                                AS markets_count_24h,
		  (SELECT COUNT(*)::bigint FROM classic_assets)        AS assets_indexed,
		  COALESCE(
		    (SELECT MAX(last_ledger)::bigint FROM ingestion_cursors
		      WHERE source <> 'backfill'),
		    0
		  )                                                    AS latest_ledger
	`
	var (
		out    NetworkStats
		volStr sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, q).Scan(
		&volStr,
		&out.MarketsCount24h,
		&out.AssetsIndexed,
		&out.LatestLedger,
	); err != nil {
		return NetworkStats{}, fmt.Errorf("timescale: GetNetworkStats: %w", err)
	}
	if volStr.Valid {
		v := volStr.String
		out.Volume24hUSD = &v
	}
	return out, nil
}
