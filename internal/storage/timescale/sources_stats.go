package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SourceStats is the per-source 24h activity row.
type SourceStats struct {
	Source        string
	TradeCount24h int64
	// VolumeUSD24h is SUM(usd_volume) over trades in the trailing
	// 24h. Numeric stringified so we don't lose precision crossing
	// the wire (and to match the rest of the Volume24hUSD shape).
	// "" when no trades had populated usd_volume in the window
	// (e.g. an oracle source whose decoder doesn't set usd_volume).
	VolumeUSD24h sql.NullString
	// MarketsCount24h is COUNT(DISTINCT (base_asset, quote_asset))
	// — the number of unique (base, quote) pairs the source
	// observed in the trailing 24h. A useful "pools per DEX"
	// proxy for AMMs where each pair contract = one pool.
	MarketsCount24h int64
}

// GetSourceStats returns trailing-24h trade counts grouped by source.
// Cheap aggregation against the trades hypertable; the source
// column is well-covered by the (ts, source, base_asset,
// quote_asset) ingest pattern.
//
// Sources with no trades in 24h are absent from the result —
// callers join against the static external.Registry to fill in.
//
// Volume derivation mirrors buildPoolsQuery (markets.go): for
// trades with non-null usd_volume we use it as-is (Phase 1
// USD-pegged-quote path); for trades with native or XLM SAC on
// either side we derive from base/quote_amount × XLM/USD via the
// same on-chain XLM/USDC vwap that powers /v1/coins. Pure
// SEP-41/SEP-41 swaps still contribute zero to the per-source
// total — separate piece of work to wire per-token oracles.
func (s *Store) GetSourceStats(ctx context.Context) ([]SourceStats, error) {
	const q = `
		WITH xlm_usd AS (
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		     AND bucket >= NOW() - INTERVAL '24 hours'
		   ORDER BY bucket DESC
		   LIMIT 1
		)
		SELECT source,
		       COUNT(*)::bigint AS trades_24h,
		       SUM(
		         CASE
		           WHEN usd_volume IS NOT NULL
		             THEN usd_volume::numeric
		           WHEN base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (base_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           WHEN quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (quote_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		         END
		       )::text AS volume_usd_24h,
		       COUNT(DISTINCT (base_asset, quote_asset))::bigint AS markets_24h
		  FROM trades
		 WHERE ts >= now() - INTERVAL '24 hours'
		 GROUP BY source
		 ORDER BY 2 DESC
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("timescale: GetSourceStats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SourceStats
	for rows.Next() {
		var ss SourceStats
		if err := rows.Scan(
			&ss.Source,
			&ss.TradeCount24h,
			&ss.VolumeUSD24h,
			&ss.MarketsCount24h,
		); err != nil {
			return nil, fmt.Errorf("timescale: GetSourceStats scan: %w", err)
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetSourceStats rows: %w", err)
	}
	return out, nil
}

// SourceVolumeBucket is one hour-resolution USD-volume datapoint
// for a single source. Hour is the bucket start (UTC); VolumeUSD
// is the same XLM/USD-derived sum that GetSourceStats's column
// uses, just narrowed to the bucket.
type SourceVolumeBucket struct {
	Source     string
	Hour       time.Time
	VolumeUSD  string // numeric stringified for precision parity
	TradeCount int64
}

// GetSourceVolumeHistory24h returns one row per (source, hour) for
// the trailing 24h, suitable for per-source sparklines on the
// /dexes + /exchanges overview tables. Sources with no trades in
// any given hour are absent — callers fill missing hours with a
// zero datapoint client-side.
//
// Same volume-derivation CTE as GetSourceStats; the only
// difference is the per-hour grouping.
func (s *Store) GetSourceVolumeHistory24h(ctx context.Context) ([]SourceVolumeBucket, error) {
	return s.sourceVolumeHistory(ctx, "24 hours")
}

// GetSourceVolumeHistory7d is the 7-day variant — same hourly grouping
// (168 buckets/source), powering the source page's 7d activity toggle.
func (s *Store) GetSourceVolumeHistory7d(ctx context.Context) ([]SourceVolumeBucket, error) {
	return s.sourceVolumeHistory(ctx, "7 days")
}

// sourceVolumeHistory returns per-(source, hour) volume + trade count
// over a trailing window. `window` is a Postgres interval literal bound
// as $1 (e.g. "24 hours", "7 days") — a bind param, not concatenated.
// The xlm_usd price CTE stays at 24h (we want the CURRENT XLM/USD rate
// to value legs, regardless of the history window).
func (s *Store) sourceVolumeHistory(ctx context.Context, window string) ([]SourceVolumeBucket, error) {
	const q = `
		WITH xlm_usd AS (
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		     AND bucket >= NOW() - INTERVAL '24 hours'
		   ORDER BY bucket DESC
		   LIMIT 1
		)
		SELECT source,
		       date_trunc('hour', ts) AS hour,
		       COALESCE(SUM(
		         CASE
		           WHEN usd_volume IS NOT NULL
		             THEN usd_volume::numeric
		           WHEN base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (base_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           WHEN quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (quote_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		         END
		       ), 0)::text AS volume_usd,
		       COUNT(*)::bigint AS trade_count
		  FROM trades
		 WHERE ts >= date_trunc('hour', NOW() - $1::interval)
		 GROUP BY source, hour
		 ORDER BY source, hour
	`
	rows, err := s.db.QueryContext(ctx, q, window)
	if err != nil {
		return nil, fmt.Errorf("timescale: sourceVolumeHistory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SourceVolumeBucket
	for rows.Next() {
		var b SourceVolumeBucket
		if err := rows.Scan(&b.Source, &b.Hour, &b.VolumeUSD, &b.TradeCount); err != nil {
			return nil, fmt.Errorf("timescale: sourceVolumeHistory scan: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: sourceVolumeHistory rows: %w", err)
	}
	return out, nil
}

// The shared CTE + SELECT for the per-source breakdowns. Two fully
// static query strings (NOT string-concatenated — gosec G202) that
// differ only in the WHERE predicate; the volume derivation matches
// GetSourceStats (XLM/USD fallback for native / XLM-SAC legs).
const (
	pairSourceStatsQuery = `
		WITH xlm_usd AS (
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		     AND bucket >= NOW() - INTERVAL '24 hours'
		   ORDER BY bucket DESC
		   LIMIT 1
		)
		SELECT source,
		       COUNT(*)::bigint AS trades_24h,
		       SUM(
		         CASE
		           WHEN usd_volume IS NOT NULL
		             THEN usd_volume::numeric
		           WHEN base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (base_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           WHEN quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (quote_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		         END
		       )::text AS volume_usd_24h,
		       COUNT(DISTINCT (base_asset, quote_asset))::bigint AS markets_24h
		  FROM trades
		 WHERE ts >= now() - INTERVAL '24 hours'
		   AND base_asset = $1 AND quote_asset = $2
		 GROUP BY source
		 ORDER BY 2 DESC
	`
	assetSourceStatsQuery = `
		WITH xlm_usd AS (
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		     AND bucket >= NOW() - INTERVAL '24 hours'
		   ORDER BY bucket DESC
		   LIMIT 1
		)
		SELECT source,
		       COUNT(*)::bigint AS trades_24h,
		       SUM(
		         CASE
		           WHEN usd_volume IS NOT NULL
		             THEN usd_volume::numeric
		           WHEN base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (base_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           WHEN quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (quote_amount / 1e7::numeric) * (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		         END
		       )::text AS volume_usd_24h,
		       COUNT(DISTINCT (base_asset, quote_asset))::bigint AS markets_24h
		  FROM trades
		 WHERE ts >= now() - INTERVAL '24 hours'
		   AND (base_asset = $1 OR quote_asset = $1)
		 GROUP BY source
		 ORDER BY 2 DESC
	`
)

// PairSourceStats returns trailing-24h per-source USD volume + trade
// count for a single (base, quote) market, ordered by volume desc.
// Backs the volume-by-source breakdown (pie) on the market-pair page —
// the recent-trades feed only samples a page of rows, so an accurate
// 24h share needs this aggregate.
func (s *Store) PairSourceStats(ctx context.Context, base, quote string) ([]SourceStats, error) {
	return s.scanSourceStats(ctx, pairSourceStatsQuery, base, quote)
}

// AssetSourceStats returns trailing-24h per-source USD volume + trade
// count aggregated over every market the asset appears in (base OR
// quote side). Backs the volume-by-source breakdown on the asset page.
func (s *Store) AssetSourceStats(ctx context.Context, asset string) ([]SourceStats, error) {
	return s.scanSourceStats(ctx, assetSourceStatsQuery, asset)
}

// scanSourceStats runs a (static) per-source-breakdown query with the
// given bound args and scans the rows. MarketsCount24h is meaningful
// only for the asset variant (distinct pairs per source); it's 1 for a
// single pair.
func (s *Store) scanSourceStats(ctx context.Context, query string, args ...any) ([]SourceStats, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: scanSourceStats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SourceStats
	for rows.Next() {
		var ss SourceStats
		if err := rows.Scan(
			&ss.Source,
			&ss.TradeCount24h,
			&ss.VolumeUSD24h,
			&ss.MarketsCount24h,
		); err != nil {
			return nil, fmt.Errorf("timescale: scanSourceStats scan: %w", err)
		}
		out = append(out, ss)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: scanSourceStats rows: %w", err)
	}
	return out, nil
}
