package timescale

import (
	"context"
	"fmt"
)

// countRecentEventsQuery is the trailing-24h per-protocol event census
// behind GET /v1/protocols' events_24h column: one UNION ALL pass over
// every served protocol table, each leg labelled with the logical
// source name the API's protocol registry uses.
//
// Legs and their timestamp columns (verified against migrations/):
//
//   - trades (ts, GROUP BY source) — sdex / soroswap / aquarius /
//     phoenix / comet trade rows (also yields CEX/FX sources; callers
//     look up only the names they care about).
//   - blend_positions / blend_emissions / blend_admin
//     (ledger_close_time) + blend_auctions (ts) — summed as 'blend'.
//   - blend_backstop_events (ledger_close_time) — 'blend_backstop'
//     (the Backstop insurance module, a separate logical source).
//   - phoenix_liquidity + phoenix_stake_events (ledger_close_time) —
//     added into 'phoenix' on top of its trades leg.
//   - comet_liquidity (ledger_close_time) — added into 'comet'.
//   - soroswap_skim_events (ledger_close_time) — added into 'soroswap'.
//   - defindex_flows (ledger_close_time) — 'defindex'.
//   - cctp_events / rozo_events (ts) — 'cctp' / 'rozo'.
//   - soroswap_router_swaps (ledger_close_time) — 'soroswap-router'.
//   - oracle_updates (ts, GROUP BY source) — reflector-dex /
//     reflector-cex / reflector-fx / redstone / band.
//
// Every leg is a count over a hypertable's most recent 24h of chunks —
// cheap relative to the 24h volume scans /v1/markets already runs —
// and the API fronts it with a 60s cache.
const countRecentEventsQuery = `
	SELECT source, count(*) AS n
	  FROM trades
	 WHERE ts >= now() - interval '24 hours'
	 GROUP BY source
	UNION ALL
	SELECT 'blend', count(*) FROM blend_positions
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'blend', count(*) FROM blend_emissions
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'blend', count(*) FROM blend_admin
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'blend', count(*) FROM blend_auctions
	 WHERE ts >= now() - interval '24 hours'
	UNION ALL
	SELECT 'blend_backstop', count(*) FROM blend_backstop_events
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'phoenix', count(*) FROM phoenix_liquidity
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'phoenix', count(*) FROM phoenix_stake_events
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'comet', count(*) FROM comet_liquidity
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'soroswap', count(*) FROM soroswap_skim_events
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'defindex', count(*) FROM defindex_flows
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT 'cctp', count(*) FROM cctp_events
	 WHERE ts >= now() - interval '24 hours'
	UNION ALL
	SELECT 'rozo', count(*) FROM rozo_events
	 WHERE ts >= now() - interval '24 hours'
	UNION ALL
	SELECT 'soroswap-router', count(*) FROM soroswap_router_swaps
	 WHERE ledger_close_time >= now() - interval '24 hours'
	UNION ALL
	SELECT source, count(*) FROM oracle_updates
	 WHERE ts >= now() - interval '24 hours'
	 GROUP BY source
`

// CountRecentEventsBySource returns the trailing-24h decoded-event
// count per logical source name. Multi-table sources (blend, phoenix,
// comet, soroswap) are summed across their tables here, so the caller
// gets one number per source. The map also carries trades' off-chain
// sources (binance, kraken, …); protocol-scoped callers simply ignore
// the extra keys.
func (s *Store) CountRecentEventsBySource(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, countRecentEventsQuery)
	if err != nil {
		return nil, fmt.Errorf("timescale: CountRecentEventsBySource: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int64, 32)
	for rows.Next() {
		var (
			source string
			n      int64
		)
		if err := rows.Scan(&source, &n); err != nil {
			return nil, fmt.Errorf("timescale: CountRecentEventsBySource scan: %w", err)
		}
		out[source] += n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: CountRecentEventsBySource rows: %w", err)
	}
	return out, nil
}
