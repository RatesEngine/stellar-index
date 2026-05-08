package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// CoinRow is the read-side projection of one row from the
// coin-discovery view: classic_assets joined with whatever supply +
// activity counters we have today. Pure-string fields keep the
// surface decoupled from the canonical types package.
//
// VWAP / Volume24hUSD / MarketCapUSD are nullable when the
// aggregator hasn't yet computed values for the asset — newly-
// observed assets, illiquid tokens with no off-chain peg, etc.
// Pointer types let the wire layer emit `null` cleanly.
type CoinRow struct {
	Slug             string
	AssetID          string
	Code             string
	IssuerGStrkey    string
	FirstSeenLedger  uint32
	LastSeenLedger   uint32
	ObservationCount int64

	// Latest VWAP-against-USD if available. Nil when:
	//   - no off-chain peg (illiquid Soroban-only token), or
	//   - the asset has no `fiat:USD` quote pair, or
	//   - the freeze writer has frozen this pair.
	PriceUSD *string
	// Trailing-24h USD-denominated trade volume. Nil when no
	// trades hit `usd_volume`-eligible quotes in 24h.
	Volume24hUSD *string
	// Market cap in USD = price × circulating_supply when both
	// are known. Nil when either component is missing.
	MarketCapUSD *string
	// Circulating supply in canonical units (decimal string;
	// scale matches asset decimals). Nil for assets the supply
	// pipeline doesn't yet cover.
	CirculatingSupply *string
	// Trailing-1h / 24h / 7d price change as a signed percentage
	// with two fractional digits (e.g. "+1.27", "-0.05", "0.00").
	// Nil when the asset has no current price, or when no
	// matching past-bucket exists in prices_1m within the
	// window-specific tolerance (±5min for 1h, ±30min for 24h,
	// ±2h for 7d).
	Change1hPct  *string
	Change24hPct *string
	Change7dPct  *string
}

// CoinsOrder controls the sort + cursor scheme used by ListCoins.
// Default ObservationCountDesc preserves the original "rank by
// activity" semantics; Volume24hUSDDesc is the volume-first view
// the explorer's /assets table can opt into for live-volume
// rankings.
type CoinsOrder int

const (
	// CoinsOrderObservationCountDesc orders by all-time observation
	// count desc (a cheap activity proxy). Cursor is
	// `<obs_count>:<asset_id>`.
	CoinsOrderObservationCountDesc CoinsOrder = iota
	// CoinsOrderVolume24hUSDDesc orders by trailing-24h USD volume
	// desc (NULLS LAST), with `<asset_id>` as the tie-breaker.
	// Cursor is `<vol_or_blank>:<asset_id>`.
	CoinsOrderVolume24hUSDDesc
)

// ListCoinsOptions bundles the optional filters / paging
// parameters for ListCoins. Zero values are the API defaults.
type ListCoinsOptions struct {
	// Limit clamps to [1, 500]; 0 → 100.
	Limit int
	// Issuer, when non-empty, restricts to that G-strkey.
	Issuer string
	// Cursor is the keyset cursor returned by the previous
	// response's NextCursor field. Empty for the first page.
	Cursor string
	// Q, when non-empty, filters rows where code, slug, or
	// issuer_g_strkey contains the substring (case-insensitive).
	// Useful for the explorer's `/assets?q=…` search box —
	// otherwise a 440K-asset directory is unsearchable.
	Q string
	// Order controls the sort + cursor scheme. Zero value is
	// observation_count desc (preserves the historical contract).
	Order CoinsOrder
}

// ListCoins returns coin-directory rows ordered by observation
// count desc (a cheap proxy for activity).
//
// Pagination uses a keyset cursor: the cursor encodes the
// (observation_count, asset_id) tuple of the last row from the
// previous page. Empty cursor means "first page". Cursor format:
// `<observation_count>:<asset_id>`.
func (s *Store) ListCoins(ctx context.Context, limit int, issuer, cursor string) ([]CoinRow, error) {
	return s.ListCoinsExt(ctx, ListCoinsOptions{Limit: limit, Issuer: issuer, Cursor: cursor})
}

// ListCoinsExt is ListCoins with the full options bag. ListCoins
// is preserved as the legacy 3-arg call so existing callers
// (handler, integration tests) compile unchanged; new callers
// pass ListCoinsOptions to opt into Q.
func (s *Store) ListCoinsExt(ctx context.Context, opts ListCoinsOptions) ([]CoinRow, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query, args := buildCoinsQuery(limit, opts.Issuer, opts.Cursor, opts.Q, opts.Order)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListCoins: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]CoinRow, 0, limit)
	for rows.Next() {
		r, err := scanCoinRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCoins rows: %w", err)
	}
	return out, nil
}

// scanCoinRow is the row-projection shared by ListCoinsExt and
// GetCoinBySlug. The two queries return the same column shape, so
// the scan + nullable-string unpack lives in one place. Pulled
// out of the listing loop so ListCoinsExt stays under the gocognit
// threshold as the wire shape grows.
func scanCoinRow(scanner interface {
	Scan(dest ...any) error
},
) (CoinRow, error) {
	var r CoinRow
	var (
		firstLedger, lastLedger int64
		priceUSD                sql.NullString
		volume24hUSD            sql.NullString
		marketCapUSD            sql.NullString
		circulatingSupply       sql.NullString
		change1hPct             sql.NullString
		change24hPct            sql.NullString
		change7dPct             sql.NullString
	)
	if err := scanner.Scan(
		&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
		&firstLedger, &lastLedger, &r.ObservationCount,
		&priceUSD, &volume24hUSD, &marketCapUSD, &circulatingSupply,
		&change1hPct, &change24hPct, &change7dPct,
	); err != nil {
		return CoinRow{}, fmt.Errorf("timescale: scan coin: %w", err)
	}
	r.FirstSeenLedger = uint32(firstLedger) //nolint:gosec
	r.LastSeenLedger = uint32(lastLedger)   //nolint:gosec
	r.PriceUSD = nullStringPtr(priceUSD)
	r.Volume24hUSD = nullStringPtr(volume24hUSD)
	r.MarketCapUSD = nullStringPtr(marketCapUSD)
	r.CirculatingSupply = nullStringPtr(circulatingSupply)
	r.Change1hPct = nullStringPtr(change1hPct)
	r.Change24hPct = nullStringPtr(change24hPct)
	r.Change7dPct = nullStringPtr(change7dPct)
	return r, nil
}

func nullStringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

// listCoinsBaseSelect is the CTE-laden SELECT shared by every
// permutation of WHERE-clause buildCoinsQuery composes. Pulled
// out of the function body so buildCoinsQuery stays under the
// funlen threshold and the SQL is editable as a single block.
//
// Volume aggregation: prices_1m.volume_usd summed across the
// trailing 24h, where the asset participates as base OR quote.
// classic_asset_stats_5m is unwritten today (migration shipped
// without a writer); most classic assets have no direct
// fiat:USD pair either. The CTE-with-UNION sidesteps both.
//
// Price + 24h change: latest + 24h-ago snapshots, with XLM
// triangulation when the direct asset/fiat:USD pair doesn't
// exist. DISTINCT ON gives one "latest per asset" row without a
// window function. ±30min tolerance on 24h-ago so sparse-trade
// assets still produce a change %. Change is computed as
// (latest / ago - 1) * 100 to two fractional digits; NULL when
// either side is missing.
//
// market_cap_usd + circulating_supply remain NULL — their proper
// sources (asset_supply_history) aren't running for the long
// tail of classic assets today, and fabricating values would
// defeat the "stop lying" rule.
const listCoinsBaseSelect = `
		WITH per_asset_24h_vol AS (
		  SELECT asset_id, SUM(volume_usd) AS vol_usd
		    FROM (
		      SELECT base_asset  AS asset_id, volume_usd
		        FROM prices_1m
		       WHERE bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		      UNION ALL
		      SELECT quote_asset AS asset_id, volume_usd
		        FROM prices_1m
		       WHERE bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		    ) t
		   GROUP BY asset_id
		),
		direct_usd AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'fiat:USD'
		     AND bucket >= now() - INTERVAL '7 days'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		direct_usd_1h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		direct_usd_24h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		direct_usd_7d AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		asset_vs_xlm AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'native'
		     AND bucket >= now() - INTERVAL '7 days'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		asset_vs_xlm_1h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		asset_vs_xlm_24h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		asset_vs_xlm_7d AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY base_asset, bucket DESC
		),
		xlm_usd AS (
		  -- prices_1m doesn't carry (native, fiat:USD) rows — XLM's
		  -- USD price is computed by the aggregator's triangulation
		  -- worker and lives in Redis, not the materialised view.
		  -- Mirror the aggregator's stablecoin-proxy policy in SQL
		  -- (CLAUDE.md: "stablecoin fiat-proxy is aggregator policy"
		  -- — USDC ≈ USD): use the latest on-chain XLM/USDC vwap as
		  -- the XLM/USD price. Circle's USDC issuer G-strkey is
		  -- hardcoded; a future revision pulls the list from
		  -- [trades].usd_pegged_classic_assets.
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC
		   LIMIT 1
		),
		xlm_usd_1h AS (
		  -- 1h-ago XLM/USD via the same stablecoin-proxy policy.
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC
		   LIMIT 1
		),
		xlm_usd_24h AS (
		  -- 24h-ago XLM/USD via the same stablecoin-proxy policy
		  -- as xlm_usd above.
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC
		   LIMIT 1
		),
		xlm_usd_7d AS (
		  -- 7d-ago XLM/USD via the same stablecoin-proxy policy.
		  SELECT vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC
		   LIMIT 1
		)
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id,
		    ca.code,
		    ca.issuer_g_strkey,
		    ca.first_seen_ledger,
		    ca.last_seen_ledger,
		    ca.observation_count,
		    -- Round to 10 dp on the wire — NUMERIC * NUMERIC
		    -- preserves full precision (36+ digits) which is just
		    -- noise for a display value. 10 dp covers sub-millicent
		    -- precision (1e-10) which is finer than any asset's
		    -- meaningful tick size.
		    -- XLM itself (asset_id='native') has no rows in
		    -- direct_usd or asset_vs_xlm — its base_asset is 'native'
		    -- but neither (native, fiat:USD) nor (native, native)
		    -- exists in prices_1m. Use the xlm_usd CTE directly.
		    ROUND(COALESCE(
		      CASE WHEN ca.asset_id = 'native'
		           THEN (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		      END,
		      direct.vwap,
		      vs_xlm.vwap * (SELECT vwap FROM xlm_usd)
		    ), 10)::text                          AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_1h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN direct.vwap IS NOT NULL AND direct_1h.vwap IS NOT NULL
		           AND direct_1h.vwap > 0
		      THEN to_char((direct.vwap / direct_1h.vwap - 1) * 100, 'FM999999990.00')
		      WHEN vs_xlm.vwap IS NOT NULL AND vs_xlm_1h.vwap IS NOT NULL
		           AND vs_xlm_1h.vwap > 0
		      THEN to_char((vs_xlm.vwap / vs_xlm_1h.vwap - 1) * 100, 'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_1h_pct,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_24h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN direct.vwap IS NOT NULL AND direct_24h.vwap IS NOT NULL
		           AND direct_24h.vwap > 0
		      THEN to_char((direct.vwap / direct_24h.vwap - 1) * 100, 'FM999999990.00')
		      WHEN vs_xlm.vwap IS NOT NULL AND vs_xlm_24h.vwap IS NOT NULL
		           AND vs_xlm_24h.vwap > 0
		      THEN to_char((vs_xlm.vwap / vs_xlm_24h.vwap - 1) * 100, 'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_24h_pct,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_7d) - 1) * 100,
		                  'FM999999990.00')
		      WHEN direct.vwap IS NOT NULL AND direct_7d.vwap IS NOT NULL
		           AND direct_7d.vwap > 0
		      THEN to_char((direct.vwap / direct_7d.vwap - 1) * 100, 'FM999999990.00')
		      WHEN vs_xlm.vwap IS NOT NULL AND vs_xlm_7d.vwap IS NOT NULL
		           AND vs_xlm_7d.vwap > 0
		      THEN to_char((vs_xlm.vwap / vs_xlm_7d.vwap - 1) * 100, 'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_7d_pct
		  FROM classic_assets ca
		  LEFT JOIN per_asset_24h_vol vol         ON vol.asset_id        = ca.asset_id
		  LEFT JOIN direct_usd        direct      ON direct.asset_id     = ca.asset_id
		  LEFT JOIN direct_usd_1h     direct_1h   ON direct_1h.asset_id  = ca.asset_id
		  LEFT JOIN direct_usd_24h    direct_24h  ON direct_24h.asset_id = ca.asset_id
		  LEFT JOIN direct_usd_7d     direct_7d   ON direct_7d.asset_id  = ca.asset_id
		  LEFT JOIN asset_vs_xlm      vs_xlm      ON vs_xlm.asset_id     = ca.asset_id
		  LEFT JOIN asset_vs_xlm_1h   vs_xlm_1h   ON vs_xlm_1h.asset_id  = ca.asset_id
		  LEFT JOIN asset_vs_xlm_24h  vs_xlm_24h  ON vs_xlm_24h.asset_id = ca.asset_id
		  LEFT JOIN asset_vs_xlm_7d   vs_xlm_7d   ON vs_xlm_7d.asset_id  = ca.asset_id
`

// buildCoinsQuery composes the WHERE + ORDER + LIMIT around
// listCoinsBaseSelect, given the limit / issuer-filter / keyset
// cursor / search query. The combinatorial explosion of
// (issuer × cursor × q) is too painful as a switch; use a
// slice + numbered placeholders.
func buildCoinsQuery(limit int, issuer, cursor, q string, order CoinsOrder) (string, []any) {
	var (
		conds []string
		args  []any
	)
	if issuer != "" {
		args = append(args, issuer)
		conds = append(conds, fmt.Sprintf("ca.issuer_g_strkey = $%d", len(args)))
	}
	if q != "" {
		args = append(args, "%"+q+"%")
		conds = append(conds, fmt.Sprintf(
			"(LOWER(ca.code) LIKE LOWER($%d) OR LOWER(COALESCE(ca.slug, ca.code)) LIKE LOWER($%d) OR LOWER(ca.issuer_g_strkey) LIKE LOWER($%d))",
			len(args), len(args), len(args)))
	}
	args = append(args, coinsCursorArgs(cursor, order)...)
	if cursor != "" {
		conds = append(conds, coinsCursorPredicate(order, len(args)))
	}
	args = append(args, limit)
	limitPlaceholder := fmt.Sprintf("$%d", len(args))

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	return listCoinsBaseSelect + where + coinsOrderBy(order) + " LIMIT " + limitPlaceholder, args
}

// coinsCursorArgs returns the positional args appended for the
// active cursor format. Empty cursor → no args.
func coinsCursorArgs(cursor string, order CoinsOrder) []any {
	if cursor == "" {
		return nil
	}
	if order == CoinsOrderVolume24hUSDDesc {
		vol, assetID := parseVolumeCursor(cursor)
		return []any{vol, assetID}
	}
	obsCount, assetID := parseCoinCursor(cursor)
	return []any{obsCount, assetID}
}

// coinsCursorPredicate returns the WHERE clause that resumes
// pagination strictly past the supplied cursor under the active
// ordering. `argEnd` is the index of the last cursor placeholder.
func coinsCursorPredicate(order CoinsOrder, argEnd int) string {
	if order == CoinsOrderVolume24hUSDDesc {
		// Mixed-direction tuple compare: volume DESC, asset_id ASC.
		// Encode as `(v < cv) OR (v = cv AND asset_id > cid)`.
		// COALESCE-to-zero so NULL volumes compare as 0 (sorts last).
		return fmt.Sprintf(
			"(COALESCE(vol.vol_usd, 0) < $%d::numeric "+
				"OR (COALESCE(vol.vol_usd, 0) = $%d::numeric AND ca.asset_id > $%d))",
			argEnd-1, argEnd-1, argEnd)
	}
	return fmt.Sprintf(
		"(ca.observation_count, ca.asset_id) < ($%d, $%d)",
		argEnd-1, argEnd)
}

func coinsOrderBy(order CoinsOrder) string {
	if order == CoinsOrderVolume24hUSDDesc {
		return " ORDER BY COALESCE(vol.vol_usd, 0) DESC, ca.asset_id ASC"
	}
	return " ORDER BY ca.observation_count DESC, ca.asset_id ASC"
}

// parseVolumeCursor decodes a `<vol_or_blank>:<asset_id>` cursor.
// Empty volume sorts as 0 (joins the null-volume tail).
func parseVolumeCursor(cursor string) (vol, assetID string) {
	for i := 0; i < len(cursor); i++ {
		if cursor[i] == ':' {
			v := cursor[:i]
			if v == "" {
				v = "0"
			}
			return v, cursor[i+1:]
		}
	}
	return "0", ""
}

// CoinPricePoint is one hourly USD-price sample in a price-history
// series. `T` is the bucket end (RFC 3339); `P` is the USD price
// rounded to 10 dp via the same direct-or-XLM-triangulated path
// as price_usd. Pointer P so an hour with no trades comes back
// as null rather than zero.
type CoinPricePoint struct {
	T string
	P *string
}

// GetCoinPriceHistory24h returns up to 24 hourly USD price samples
// for the asset, ordered by bucket ASC (oldest first). Each
// sample uses the same direct-then-XLM-triangulated path as
// price_usd, but bucketed to the 1-hour grain. Powers a sparkline
// column on /assets and a price chart preview on the detail page.
//
// Buckets with no underlying trades produce a null P. Callers can
// either render a gap or interpolate; we leave that to the UI.
func (s *Store) GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]CoinPricePoint, error) {
	const q = `
		WITH hours AS (
		  SELECT generate_series(
		    date_trunc('hour', now() - INTERVAL '23 hours'),
		    date_trunc('hour', now()),
		    INTERVAL '1 hour'
		  ) AS bucket
		),
		direct_per_hour AS (
		  SELECT date_trunc('hour', bucket) AS h, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = $1
		     AND quote_asset = 'fiat:USD'
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY h
		),
		asset_xlm_per_hour AS (
		  SELECT date_trunc('hour', bucket) AS h, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = $1
		     AND quote_asset = 'native'
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY h
		),
		xlm_usd_per_hour AS (
		  -- Same stablecoin-proxy fallback as the listing query —
		  -- prices_1m doesn't carry (native, fiat:USD) rows.
		  SELECT date_trunc('hour', bucket) AS h, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY h
		)
		SELECT
		    to_char(hours.bucket, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS t,
		    ROUND(COALESCE(
		      -- XLM itself: use the xlm_usd_per_hour CTE directly.
		      -- Without this, XLM's sparkline is all nulls because
		      -- direct_per_hour and asset_xlm_per_hour both filter
		      -- on base_asset=$1 (= 'native') with quote_asset
		      -- 'fiat:USD' / 'native' — neither has rows.
		      CASE WHEN $1 = 'native' THEN xu.vwap ELSE NULL END,
		      d.vwap,
		      x.vwap * xu.vwap
		    ), 10)::text AS p
		  FROM hours
		  LEFT JOIN direct_per_hour     d  ON d.h  = hours.bucket
		  LEFT JOIN asset_xlm_per_hour  x  ON x.h  = hours.bucket
		  LEFT JOIN xlm_usd_per_hour    xu ON xu.h = hours.bucket
		 ORDER BY hours.bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinPriceHistory24h: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]CoinPricePoint, 0, 24)
	for rows.Next() {
		var pt CoinPricePoint
		var p sql.NullString
		if err := rows.Scan(&pt.T, &p); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinPriceHistory24h scan: %w", err)
		}
		if p.Valid && p.String != "" {
			s := p.String
			pt.P = &s
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinPriceHistory24h rows: %w", err)
	}
	return out, nil
}

// GetCoinPriceHistory7d returns up to 7 daily USD price samples
// for the asset, ordered by bucket ASC (oldest first). Same
// direct-then-XLM-triangulated path as GetCoinPriceHistory24h, but
// bucketed to the 1-day grain over the last 7 days.
//
// Buckets with no underlying trades produce a null P.
func (s *Store) GetCoinPriceHistory7d(ctx context.Context, assetID string) ([]CoinPricePoint, error) {
	const q = `
		WITH days AS (
		  SELECT generate_series(
		    date_trunc('day', now() - INTERVAL '6 days'),
		    date_trunc('day', now()),
		    INTERVAL '1 day'
		  ) AS bucket
		),
		direct_per_day AS (
		  SELECT date_trunc('day', bucket) AS d, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = $1
		     AND quote_asset = 'fiat:USD'
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY d
		),
		asset_xlm_per_day AS (
		  SELECT date_trunc('day', bucket) AS d, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = $1
		     AND quote_asset = 'native'
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY d
		),
		xlm_usd_per_day AS (
		  SELECT date_trunc('day', bucket) AS d, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY d
		)
		SELECT
		    to_char(days.bucket, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS t,
		    ROUND(COALESCE(
		      CASE WHEN $1 = 'native' THEN xu.vwap ELSE NULL END,
		      d.vwap,
		      x.vwap * xu.vwap
		    ), 10)::text AS p
		  FROM days
		  LEFT JOIN direct_per_day      d  ON d.d  = days.bucket
		  LEFT JOIN asset_xlm_per_day   x  ON x.d  = days.bucket
		  LEFT JOIN xlm_usd_per_day     xu ON xu.d = days.bucket
		 ORDER BY days.bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q, assetID)
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinPriceHistory7d: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]CoinPricePoint, 0, 7)
	for rows.Next() {
		var pt CoinPricePoint
		var p sql.NullString
		if err := rows.Scan(&pt.T, &p); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinPriceHistory7d scan: %w", err)
		}
		if p.Valid && p.String != "" {
			s := p.String
			pt.P = &s
		}
		out = append(out, pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinPriceHistory7d rows: %w", err)
	}
	return out, nil
}

// CoinATH is the asset's all-time high USD price plus the day
// it was observed. Computed across every USD-quoted day-bucket
// in `prices_1d` (direct USD-stablecoin pairs and `fiat:USD`).
// Triangulated paths (asset/XLM × XLM/USD) are intentionally
// excluded — they introduce two layers of price-discovery
// noise and a single bad XLM/USD reading on a thin day could
// fabricate an ATH.
type CoinATH struct {
	USD string // numeric, fixed-point string (preserves precision)
	At  string // RFC-3339 day-bucket the high was set
}

// GetCoinATH returns the asset's all-time-high USD price.
//
// Sources `prices_1d` filtered to USD-denominated quotes — i.e.
// the canonical USDC/USDT issuers, plus the synthetic `fiat:USD`
// quote used by off-chain CEX feeds. Returns the (high_price,
// bucket_day) tuple where high_price is maximal.
//
// For native XLM the asset is on the BASE side of every USD pair,
// so the same query works without a special case. Returns
// (nil, nil) cleanly when the asset has never had a USD-quoted
// day with non-null high_price (very thin assets).
func (s *Store) GetCoinATH(ctx context.Context, assetID string) (*CoinATH, error) {
	const q = `
		SELECT
		    high_price::text,
		    to_char(bucket, 'YYYY-MM-DD"T"00:00:00"Z"')
		  FROM prices_1d
		 WHERE base_asset = $1
		   AND quote_asset IN (
		     'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		     'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		     'fiat:USD'
		   )
		   AND high_price IS NOT NULL
		 ORDER BY high_price DESC
		 LIMIT 1
	`
	var ath CoinATH
	switch err := s.db.QueryRowContext(ctx, q, assetID).Scan(&ath.USD, &ath.At); {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("timescale: GetCoinATH: %w", err)
	}
	return &ath, nil
}

// GetCoinsATHBatch returns ATH USD price + day for each asset_id
// in a single round trip. DISTINCT ON picks the (high_price-max,
// bucket) tuple per base_asset; the same USD-quote allowlist as
// the per-asset GetCoinATH.
//
// Empty input returns an empty map cleanly. Asset_ids with no
// USD-quoted history are simply absent from the result map.
//
// Powers `?include=ath` on /v1/coins so /assets can show "% from
// ATH" without N+1 round trips.
func (s *Store) GetCoinsATHBatch(ctx context.Context, assetIDs []string) (map[string]CoinATH, error) {
	out := make(map[string]CoinATH, len(assetIDs))
	if len(assetIDs) == 0 {
		return out, nil
	}
	const q = `
		SELECT DISTINCT ON (base_asset)
		    base_asset,
		    high_price::text,
		    to_char(bucket, 'YYYY-MM-DD"T"00:00:00"Z"')
		  FROM prices_1d
		 WHERE base_asset = ANY($1)
		   AND quote_asset IN (
		     'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		     'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		     'fiat:USD'
		   )
		   AND high_price IS NOT NULL
		 ORDER BY base_asset, high_price DESC
	`
	rows, err := s.db.QueryContext(ctx, q, pq.Array(assetIDs))
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsATHBatch: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var assetID string
		var ath CoinATH
		if err := rows.Scan(&assetID, &ath.USD, &ath.At); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinsATHBatch scan: %w", err)
		}
		out[assetID] = ath
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsATHBatch rows: %w", err)
	}
	return out, nil
}

// CoinTopMarket is one entry in the top-markets preview returned
// alongside a single coin lookup. Compact summary suitable for an
// asset detail page header — the full markets list lives on
// /v1/markets.
type CoinTopMarket struct {
	Counterparty  string  // the OTHER side of the pair (the side that's NOT this asset)
	Side          string  // "base" if this asset was base, else "quote"
	Volume24hUSD  *string // trailing-24h USD volume for this pair; nil if no USD-equivalent trades
	TradeCount24h int64
}

// GetCoinMarketsCount returns the count of distinct (base, quote)
// pairs the asset participated in over the trailing 24h with a
// non-null prices_1m bucket. Cheaper than GetCoinTopMarkets — no
// volume aggregation, no limit, no ordering. Powers the asset
// detail page's "Markets: 12" header chip.
//
// Returns 0 cleanly when the asset has no rows in the window.
func (s *Store) GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error) {
	const q = `
		SELECT COUNT(*) FROM (
		  SELECT DISTINCT base_asset, quote_asset
		    FROM prices_1m
		   WHERE bucket >= now() - INTERVAL '24 hours'
		     AND (base_asset = $1 OR quote_asset = $1)
		) t
	`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, assetID).Scan(&n); err != nil {
		return 0, fmt.Errorf("timescale: GetCoinMarketsCount: %w", err)
	}
	return n, nil
}

// GetCoinTradeCount24h returns the count of trades the given asset
// participated in (as base OR quote) over the trailing 24 hours.
// Reads the `trades` hypertable directly — accurate down to the
// individual trade rather than the prices_1m bucket aggregation
// used by GetCoinMarketsCount.
//
// Powers the asset detail page header — `observation_count` shows
// the all-time figure, this gives the "what's happening right now"
// counterpart so a user can tell at a glance whether the asset is
// active or a long tail entry.
//
// Returns 0 cleanly when the asset has no trades in the window.
func (s *Store) GetCoinTradeCount24h(ctx context.Context, assetID string) (int64, error) {
	const q = `
		SELECT COUNT(*)
		  FROM trades
		 WHERE ts >= now() - INTERVAL '24 hours'
		   AND (base_asset = $1 OR quote_asset = $1)
	`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, assetID).Scan(&n); err != nil {
		return 0, fmt.Errorf("timescale: GetCoinTradeCount24h: %w", err)
	}
	return n, nil
}

// GetCoinTopMarkets returns up to `limit` markets the given asset
// participates in (as base OR quote), ordered by trailing-24h USD
// volume desc. Used by the explorer asset-detail page to show a
// "Top markets" preview without a separate /v1/markets call.
//
// limit clamps to [1, 20]; default 5.
func (s *Store) GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]CoinTopMarket, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	const q = `
		WITH per_pair_24h AS (
		  SELECT base_asset, quote_asset,
		         SUM(volume_usd)::text AS vol_usd
		    FROM prices_1m
		   WHERE bucket >= now() - INTERVAL '24 hours'
		     AND volume_usd IS NOT NULL
		     AND (base_asset = $1 OR quote_asset = $1)
		   GROUP BY base_asset, quote_asset
		),
		per_pair_count AS (
		  SELECT base_asset, quote_asset,
		         COUNT(*) FILTER (WHERE ts > now() - INTERVAL '24 hours') AS n
		    FROM trades
		   WHERE ts >= now() - INTERVAL '24 hours'
		     AND (base_asset = $1 OR quote_asset = $1)
		   GROUP BY base_asset, quote_asset
		)
		SELECT
		    CASE WHEN p.base_asset = $1 THEN p.quote_asset ELSE p.base_asset END AS counterparty,
		    CASE WHEN p.base_asset = $1 THEN 'base' ELSE 'quote' END             AS side,
		    p.vol_usd                                                            AS vol_24h_usd,
		    COALESCE(c.n, 0)                                                     AS n_24h
		  FROM per_pair_24h p
		  LEFT JOIN per_pair_count c
		    ON c.base_asset = p.base_asset AND c.quote_asset = p.quote_asset
		 ORDER BY p.vol_usd::numeric DESC NULLS LAST
		 LIMIT $2
	`
	rows, err := s.db.QueryContext(ctx, q, assetID, limit)
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinTopMarkets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]CoinTopMarket, 0, limit)
	for rows.Next() {
		var m CoinTopMarket
		var vol sql.NullString
		if err := rows.Scan(&m.Counterparty, &m.Side, &vol, &m.TradeCount24h); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinTopMarkets scan: %w", err)
		}
		if vol.Valid && vol.String != "" && vol.String != "0" {
			v := vol.String
			m.Volume24hUSD = &v
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinTopMarkets rows: %w", err)
	}
	return out, nil
}

// GetCoinBySlug returns one row matching the given slug. Returns
// sql.ErrNoRows when the slug doesn't match a known classic asset.
//
// Mirrors ListCoins's per-row metric shape (price/volume/market cap/
// supply) so the explorer can render an asset detail page from a
// single endpoint without scanning the top-N listing first.
// getCoinBySlugSQL is the SQL behind GetCoinBySlug. Hoisted out
// of the function body to keep GetCoinBySlug under the funlen
// threshold; the helpers above already document the chosen-CTE
// pattern that keeps the volume sum and price triangulation on
// the same canonical row.
const getCoinBySlugSQL = `
		WITH chosen AS (
		  SELECT asset_id
		    FROM classic_assets
		   WHERE COALESCE(slug, code) = $1
		   ORDER BY (slug = $1) DESC NULLS LAST,
		            observation_count DESC, asset_id ASC
		   LIMIT 1
		),
		per_asset_24h_vol AS (
		  SELECT SUM(volume_usd) AS vol_usd
		    FROM (
		      SELECT volume_usd FROM prices_1m
		       WHERE base_asset = (SELECT asset_id FROM chosen)
		         AND bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		      UNION ALL
		      SELECT volume_usd FROM prices_1m
		       WHERE quote_asset = (SELECT asset_id FROM chosen)
		         AND bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		    ) t
		),
		direct_usd AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'fiat:USD'
		     AND bucket >= now() - INTERVAL '7 days'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		direct_usd_1h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		direct_usd_24h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		direct_usd_7d AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		asset_vs_xlm AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'native'
		     AND bucket >= now() - INTERVAL '7 days'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		asset_vs_xlm_1h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		asset_vs_xlm_24h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		asset_vs_xlm_7d AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd AS (
		  -- Same stablecoin-proxy policy as the listing query:
		  -- prices_1m doesn't carry (native, fiat:USD) rows; use
		  -- on-chain XLM/USDC (or USDT) as the USD-equivalent.
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_1h AS (
		  -- 1h-ago XLM/USD for change_1h_pct via stablecoin proxy.
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_24h AS (
		  -- 24h-ago XLM/USD for change_24h_pct via stablecoin proxy.
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_7d AS (
		  -- 7d-ago XLM/USD for change_7d_pct via stablecoin proxy.
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		)
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id, ca.code, ca.issuer_g_strkey,
		    ca.first_seen_ledger, ca.last_seen_ledger, ca.observation_count,
		    -- XLM (asset_id='native') has no rows in direct_usd or
		    -- asset_vs_xlm — its base_asset is 'native' but neither
		    -- (native, fiat:USD) nor (native, native) exists in
		    -- prices_1m. Use the xlm_usd CTE directly.
		    ROUND(COALESCE(
		      CASE WHEN ca.asset_id = 'native'
		           THEN (SELECT vwap FROM xlm_usd)
		           ELSE NULL
		      END,
		      (SELECT vwap FROM direct_usd),
		      (SELECT vwap FROM asset_vs_xlm) * (SELECT vwap FROM xlm_usd)
		    ), 10)::text                          AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_1h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM direct_usd) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_1h) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_1h) > 0
		      THEN to_char(((SELECT vwap FROM direct_usd)
		                  / (SELECT vwap FROM direct_usd_1h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM asset_vs_xlm) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_1h) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_1h) > 0
		      THEN to_char(((SELECT vwap FROM asset_vs_xlm)
		                  / (SELECT vwap FROM asset_vs_xlm_1h) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_1h_pct,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_24h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM direct_usd) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_24h) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_24h) > 0
		      THEN to_char(((SELECT vwap FROM direct_usd)
		                  / (SELECT vwap FROM direct_usd_24h) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM asset_vs_xlm) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_24h) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_24h) > 0
		      THEN to_char(((SELECT vwap FROM asset_vs_xlm)
		                  / (SELECT vwap FROM asset_vs_xlm_24h) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_24h_pct,
		    CASE
		      WHEN ca.asset_id = 'native'
		           AND (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_7d) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM direct_usd) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_7d) IS NOT NULL
		           AND (SELECT vwap FROM direct_usd_7d) > 0
		      THEN to_char(((SELECT vwap FROM direct_usd)
		                  / (SELECT vwap FROM direct_usd_7d) - 1) * 100,
		                  'FM999999990.00')
		      WHEN (SELECT vwap FROM asset_vs_xlm) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_7d) IS NOT NULL
		           AND (SELECT vwap FROM asset_vs_xlm_7d) > 0
		      THEN to_char(((SELECT vwap FROM asset_vs_xlm)
		                  / (SELECT vwap FROM asset_vs_xlm_7d) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_7d_pct
		  FROM chosen
		  JOIN classic_assets ca ON ca.asset_id = chosen.asset_id
		  LEFT JOIN per_asset_24h_vol vol ON true
`

func (s *Store) GetCoinBySlug(ctx context.Context, slug string) (CoinRow, error) {
	r, err := scanCoinRow(s.db.QueryRowContext(ctx, getCoinBySlugSQL, slug))
	if err != nil {
		// Surface sql.ErrNoRows unwrapped so handler errors.Is checks
		// keep matching; scanCoinRow wraps with %w which preserves it.
		return CoinRow{}, err
	}
	return r, nil
}

// GetNativeCoinRow returns the synthetic CoinRow for native XLM.
//
// Native XLM has no row in classic_assets — that table only tracks
// issued classic assets, by definition. Without a special-case path
// the public lookup `/v1/coins/XLM` either 404s (no slug match) or
// returns whichever issued token's code happens to be "XLM" wins
// the disambiguation tiebreak (today: a scam token issued by
// GAE5PQNUIP5E…).
//
// Population:
//   - Slug / Code: hardcoded "XLM"
//   - AssetID: "native" (the canonical pair-side identifier)
//   - IssuerGStrkey: "" (native has no issuer)
//   - First/Last seen ledger: the trades hypertable's min/max for
//     base_asset='native' OR quote_asset='native'
//   - ObservationCount: total trades touching native in the
//     hypertable, capped at int64
//   - PriceUSD + Change*Pct: same xlm_usd / xlm_usd_{1h,24h,7d}
//     stablecoin-proxy chain used by GetCoinBySlug + the listing
//     query for non-native assets
//   - Volume24hUSD: SUM(volume_usd) where the asset is base or quote
//     in the trailing 24h
//   - MarketCapUSD / CirculatingSupply: NULL — supply pipeline
//     doesn't yet emit a row for native (algorithm 1 work)
//
// Always returns a populated row (no sql.ErrNoRows path) — the
// underlying CTEs LEFT JOIN out to NULL when there's no data.
func (s *Store) GetNativeCoinRow(ctx context.Context) (CoinRow, error) {
	return scanCoinRow(s.db.QueryRowContext(ctx, getNativeCoinSQL))
}

// getNativeCoinSQL is GetNativeCoinRow's query, hoisted to a
// package constant so the function body stays under the funlen
// threshold. Returns the same column shape as listCoinsBaseSelect
// + getCoinBySlugSQL — the shared scanCoinRow projector handles
// it identically.
const getNativeCoinSQL = `
		WITH per_asset_24h_vol AS (
		  SELECT SUM(volume_usd) AS vol_usd
		    FROM (
		      SELECT volume_usd FROM prices_1m
		       WHERE base_asset = 'native'
		         AND bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		      UNION ALL
		      SELECT volume_usd FROM prices_1m
		       WHERE quote_asset = 'native'
		         AND bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		    ) t
		),
		xlm_usd AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_1h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '90 minutes'
		                   AND now() - INTERVAL '55 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_24h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '26 hours'
		                   AND now() - INTERVAL '23 hours 30 minutes'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		xlm_usd_7d AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket BETWEEN now() - INTERVAL '7 days 12 hours'
		                   AND now() - INTERVAL '6 days 22 hours'
		     AND vwap IS NOT NULL
		   ORDER BY bucket DESC LIMIT 1
		),
		ledger_bounds AS (
		  -- Always return one row with placeholder zeros — the
		  -- previous version scanned the trades hypertable for
		  -- 7 days OF native-touching rows, which is millions of
		  -- rows on a busy ledger and timed out under lock-table
		  -- pressure (53200). Native XLM is well-known; the
		  -- explorer doesn't need an accurate first_seen_ledger
		  -- for it. observation_count uses prices_1m row count
		  -- as a cheap proxy.
		  SELECT
		    0::bigint AS first_ledger,
		    0::bigint AS last_ledger,
		    COALESCE(
		      (SELECT COUNT(*)::bigint
		         FROM prices_1m
		        WHERE bucket >= now() - INTERVAL '24 hours'
		          AND (base_asset = 'native' OR quote_asset = 'native')),
		      0
		    ) AS obs_count
		)
		SELECT
		    'XLM'                                  AS slug,
		    'native'                               AS asset_id,
		    'XLM'                                  AS code,
		    ''                                     AS issuer_g_strkey,
		    lb.first_ledger                        AS first_seen_ledger,
		    lb.last_ledger                         AS last_seen_ledger,
		    lb.obs_count                           AS observation_count,
		    ROUND((SELECT vwap FROM xlm_usd), 10)::text AS price_usd,
		    vol.vol_usd                            AS volume_24h_usd,
		    NULL::numeric                          AS market_cap_usd,
		    NULL::numeric                          AS circulating_supply,
		    CASE
		      WHEN (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_1h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_1h) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                    AS change_1h_pct,
		    CASE
		      WHEN (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_24h) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_24h) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                    AS change_24h_pct,
		    CASE
		      WHEN (SELECT vwap FROM xlm_usd) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) IS NOT NULL
		           AND (SELECT vwap FROM xlm_usd_7d) > 0
		      THEN to_char(((SELECT vwap FROM xlm_usd)
		                  / (SELECT vwap FROM xlm_usd_7d) - 1) * 100,
		                  'FM999999990.00')
		      ELSE NULL
		    END                                    AS change_7d_pct
		  FROM ledger_bounds lb
		  LEFT JOIN per_asset_24h_vol vol ON true
`

// LatestAssetStats returns per-asset 24h volume + supply stats
// for /v1/assets/{id}. Volume sums prices_1m.volume_usd across
// pairs where the asset is base or quote (mirrors
// Volume24hUSDForAsset). Supply is null for now — the source
// table classic_asset_stats_5m is unwritten.
//
// Always returns nil error for a row that simply has no stats;
// the LEFT JOINs evaluate to NULL.
func (s *Store) LatestAssetStats(ctx context.Context, assetID string) (CoinRow, error) {
	const q = `
		SELECT COALESCE(SUM(volume_usd), 0)::text
		  FROM (
		    SELECT volume_usd FROM prices_1m
		     WHERE base_asset = $1
		       AND bucket >= now() - INTERVAL '24 hours'
		       AND bucket  <  now()
		    UNION ALL
		    SELECT volume_usd FROM prices_1m
		     WHERE quote_asset = $1
		       AND bucket >= now() - INTERVAL '24 hours'
		       AND bucket  <  now()
		  ) t
	`
	var vol string
	if err := s.db.QueryRowContext(ctx, q, assetID).Scan(&vol); err != nil {
		return CoinRow{}, fmt.Errorf("timescale: LatestAssetStats: %w", err)
	}
	out := CoinRow{AssetID: assetID}
	if vol != "" && vol != "0" {
		out.Volume24hUSD = &vol
	}
	return out, nil
}

// parseCoinCursor decodes a `<obs_count>:<asset_id>` cursor.
// Empty cursor → (0, "") which means "no cursor". Malformed
// cursors fall through to the same (the handler validates the
// shape upstream; we tolerate junk by ignoring it).
func parseCoinCursor(cursor string) (obsCount int64, assetID string) {
	if cursor == "" {
		return 0, ""
	}
	for i := 0; i < len(cursor); i++ {
		if cursor[i] == ':' {
			n := int64(0)
			for j := 0; j < i; j++ {
				c := cursor[j]
				if c < '0' || c > '9' {
					return 0, ""
				}
				n = n*10 + int64(c-'0')
			}
			return n, cursor[i+1:]
		}
	}
	return 0, ""
}

// EncodeCoinCursor pairs with parseCoinCursor — the API handler
// emits the encoded form as the next-page cursor in pagination
// meta. Exported so v1/coins.go can call it without duplicating.
func EncodeCoinCursor(obsCount int64, assetID string) string {
	return fmt.Sprintf("%d:%s", obsCount, assetID)
}

// GetCoinsPriceHistory24hBatch returns 24h hourly USD-price series
// for many assets in one query. Result is keyed by asset_id; the
// per-asset slice has up to 24 ordered points (oldest → newest).
// Assets with no trade history in the window get an empty slice
// (callers can render that as "no chart").
//
// Same direct-then-XLM-triangulated path as the single-asset
// GetCoinPriceHistory24h; just a single CTE pass over all
// requested assets at once.
func (s *Store) GetCoinsPriceHistory24hBatch(ctx context.Context, assetIDs []string) (map[string][]CoinPricePoint, error) {
	if len(assetIDs) == 0 {
		return map[string][]CoinPricePoint{}, nil
	}
	const q = `
		WITH hours AS (
		  SELECT generate_series(
		    date_trunc('hour', now() - INTERVAL '23 hours'),
		    date_trunc('hour', now()),
		    INTERVAL '1 hour'
		  ) AS bucket
		),
		direct_per_hour AS (
		  SELECT base_asset AS asset_id,
		         date_trunc('hour', bucket) AS h,
		         last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = ANY($1)
		     AND quote_asset = 'fiat:USD'
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY base_asset, h
		),
		asset_xlm_per_hour AS (
		  SELECT base_asset AS asset_id,
		         date_trunc('hour', bucket) AS h,
		         last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = ANY($1)
		     AND quote_asset = 'native'
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY base_asset, h
		),
		xlm_usd_per_hour AS (
		  SELECT date_trunc('hour', bucket) AS h, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND vwap IS NOT NULL
		   GROUP BY h
		),
		want AS (
		  SELECT unnest($1::text[]) AS asset_id
		)
		SELECT
		    w.asset_id,
		    to_char(hours.bucket, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS t,
		    ROUND(COALESCE(
		      CASE WHEN w.asset_id = 'native' THEN xu.vwap ELSE NULL END,
		      d.vwap,
		      x.vwap * xu.vwap
		    ), 10)::text AS p
		  FROM want w
		  CROSS JOIN hours
		  LEFT JOIN direct_per_hour     d  ON d.h  = hours.bucket AND d.asset_id  = w.asset_id
		  LEFT JOIN asset_xlm_per_hour  x  ON x.h  = hours.bucket AND x.asset_id  = w.asset_id
		  LEFT JOIN xlm_usd_per_hour    xu ON xu.h = hours.bucket
		 ORDER BY w.asset_id, hours.bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q, pq.Array(assetIDs))
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsPriceHistory24hBatch: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string][]CoinPricePoint, len(assetIDs))
	for rows.Next() {
		var assetID string
		var pt CoinPricePoint
		var p sql.NullString
		if err := rows.Scan(&assetID, &pt.T, &p); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinsPriceHistory24hBatch scan: %w", err)
		}
		if p.Valid && p.String != "" {
			s := p.String
			pt.P = &s
		}
		out[assetID] = append(out[assetID], pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsPriceHistory24hBatch rows: %w", err)
	}
	return out, nil
}

// GetCoinsPriceHistory7dBatch returns 7d daily USD-price series for
// many assets in one query. 7-bucket-deep daily grain, mirroring
// the per-asset GetCoinPriceHistory7d. Same direct-then-XLM-
// triangulated path; one query for many asset_ids.
func (s *Store) GetCoinsPriceHistory7dBatch(ctx context.Context, assetIDs []string) (map[string][]CoinPricePoint, error) {
	if len(assetIDs) == 0 {
		return map[string][]CoinPricePoint{}, nil
	}
	const q = `
		WITH days AS (
		  SELECT generate_series(
		    date_trunc('day', now() - INTERVAL '6 days'),
		    date_trunc('day', now()),
		    INTERVAL '1 day'
		  ) AS bucket
		),
		direct_per_day AS (
		  SELECT base_asset AS asset_id,
		         date_trunc('day', bucket) AS d,
		         last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = ANY($1)
		     AND quote_asset = 'fiat:USD'
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY base_asset, d
		),
		asset_xlm_per_day AS (
		  SELECT base_asset AS asset_id,
		         date_trunc('day', bucket) AS d,
		         last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = ANY($1)
		     AND quote_asset = 'native'
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY base_asset, d
		),
		xlm_usd_per_day AS (
		  SELECT date_trunc('day', bucket) AS d, last(vwap, bucket)::numeric AS vwap
		    FROM prices_1m
		   WHERE base_asset = 'native'
		     AND quote_asset IN (
		       'USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
		       'USDT-GCQTGZQQ5G4PTM2GL7CDIFKUBIPEC52BROAQIAPW53XBRJVN6ZJVTG6V',
		       'fiat:USD'
		     )
		     AND bucket >= date_trunc('day', now() - INTERVAL '6 days')
		     AND vwap IS NOT NULL
		   GROUP BY d
		),
		want AS (
		  SELECT unnest($1::text[]) AS asset_id
		)
		SELECT
		    w.asset_id,
		    to_char(days.bucket, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS t,
		    ROUND(COALESCE(
		      CASE WHEN w.asset_id = 'native' THEN xu.vwap ELSE NULL END,
		      d.vwap,
		      x.vwap * xu.vwap
		    ), 10)::text AS p
		  FROM want w
		  CROSS JOIN days
		  LEFT JOIN direct_per_day     d  ON d.d  = days.bucket AND d.asset_id  = w.asset_id
		  LEFT JOIN asset_xlm_per_day  x  ON x.d  = days.bucket AND x.asset_id  = w.asset_id
		  LEFT JOIN xlm_usd_per_day    xu ON xu.d = days.bucket
		 ORDER BY w.asset_id, days.bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q, pq.Array(assetIDs))
	if err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsPriceHistory7dBatch: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string][]CoinPricePoint, len(assetIDs))
	for rows.Next() {
		var assetID string
		var pt CoinPricePoint
		var p sql.NullString
		if err := rows.Scan(&assetID, &pt.T, &p); err != nil {
			return nil, fmt.Errorf("timescale: GetCoinsPriceHistory7dBatch scan: %w", err)
		}
		if p.Valid && p.String != "" {
			s := p.String
			pt.P = &s
		}
		out[assetID] = append(out[assetID], pt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetCoinsPriceHistory7dBatch rows: %w", err)
	}
	return out, nil
}
