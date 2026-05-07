package timescale

import (
	"context"
	"database/sql"
	"fmt"
)

// SourceStats is the per-source 24h activity row.
type SourceStats struct {
	Source         string
	TradeCount24h  int64
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
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
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
		             THEN (base_amount / 1e7) * (SELECT vwap FROM xlm_usd)
		           WHEN quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
		             THEN (quote_amount / 1e7) * (SELECT vwap FROM xlm_usd)
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
