package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	// Trailing-24h price change as a signed percentage with two
	// fractional digits (e.g. "+1.27", "-0.05", "0.00"). Nil
	// when the asset has no current price, or when no
	// 24h-ago bucket exists in prices_1m within a ±30min
	// tolerance.
	Change24hPct *string
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
		var r CoinRow
		var (
			firstLedger, lastLedger int64
			priceUSD                sql.NullString
			volume24hUSD            sql.NullString
			marketCapUSD            sql.NullString
			circulatingSupply       sql.NullString
			change24hPct            sql.NullString
		)
		if err := rows.Scan(
			&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
			&firstLedger, &lastLedger, &r.ObservationCount,
			&priceUSD, &volume24hUSD, &marketCapUSD, &circulatingSupply,
			&change24hPct,
		); err != nil {
			return nil, fmt.Errorf("timescale: ListCoins scan: %w", err)
		}
		r.FirstSeenLedger = uint32(firstLedger) //nolint:gosec
		r.LastSeenLedger = uint32(lastLedger)   //nolint:gosec
		if priceUSD.Valid {
			r.PriceUSD = &priceUSD.String
		}
		if volume24hUSD.Valid {
			r.Volume24hUSD = &volume24hUSD.String
		}
		if marketCapUSD.Valid {
			r.MarketCapUSD = &marketCapUSD.String
		}
		if circulatingSupply.Valid {
			r.CirculatingSupply = &circulatingSupply.String
		}
		if change24hPct.Valid {
			r.Change24hPct = &change24hPct.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCoins rows: %w", err)
	}
	return out, nil
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
		direct_usd_24h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '24 hours 30 minutes'
		                   AND now() - INTERVAL '23 hours 30 minutes'
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
		asset_vs_xlm_24h AS (
		  SELECT DISTINCT ON (base_asset) base_asset AS asset_id, vwap
		    FROM prices_1m
		   WHERE quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '24 hours 30 minutes'
		                   AND now() - INTERVAL '23 hours 30 minutes'
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
		     AND bucket BETWEEN now() - INTERVAL '24 hours 30 minutes'
		                   AND now() - INTERVAL '23 hours 30 minutes'
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
		    ROUND(COALESCE(
		      direct.vwap,
		      vs_xlm.vwap * (SELECT vwap FROM xlm_usd)
		    ), 10)::text                          AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply,
		    CASE
		      WHEN direct.vwap IS NOT NULL AND direct_24h.vwap IS NOT NULL
		           AND direct_24h.vwap > 0
		      THEN to_char((direct.vwap / direct_24h.vwap - 1) * 100, 'FM999999990.00')
		      WHEN vs_xlm.vwap IS NOT NULL AND vs_xlm_24h.vwap IS NOT NULL
		           AND vs_xlm_24h.vwap > 0
		      THEN to_char((vs_xlm.vwap / vs_xlm_24h.vwap - 1) * 100, 'FM999999990.00')
		      ELSE NULL
		    END                                   AS change_24h_pct
		  FROM classic_assets ca
		  LEFT JOIN per_asset_24h_vol vol         ON vol.asset_id        = ca.asset_id
		  LEFT JOIN direct_usd        direct      ON direct.asset_id     = ca.asset_id
		  LEFT JOIN direct_usd_24h    direct_24h  ON direct_24h.asset_id = ca.asset_id
		  LEFT JOIN asset_vs_xlm      vs_xlm      ON vs_xlm.asset_id     = ca.asset_id
		  LEFT JOIN asset_vs_xlm_24h  vs_xlm_24h  ON vs_xlm_24h.asset_id = ca.asset_id
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
		direct_usd_24h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'fiat:USD'
		     AND bucket BETWEEN now() - INTERVAL '24 hours 30 minutes'
		                   AND now() - INTERVAL '23 hours 30 minutes'
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
		asset_vs_xlm_24h AS (
		  SELECT vwap FROM prices_1m
		   WHERE base_asset  = (SELECT asset_id FROM chosen)
		     AND quote_asset = 'native'
		     AND bucket BETWEEN now() - INTERVAL '24 hours 30 minutes'
		                   AND now() - INTERVAL '23 hours 30 minutes'
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
		)
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id, ca.code, ca.issuer_g_strkey,
		    ca.first_seen_ledger, ca.last_seen_ledger, ca.observation_count,
		    ROUND(COALESCE(
		      (SELECT vwap FROM direct_usd),
		      (SELECT vwap FROM asset_vs_xlm) * (SELECT vwap FROM xlm_usd)
		    ), 10)::text                          AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply,
		    CASE
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
		    END                                   AS change_24h_pct
		  FROM chosen
		  JOIN classic_assets ca ON ca.asset_id = chosen.asset_id
		  LEFT JOIN per_asset_24h_vol vol ON true
`

func (s *Store) GetCoinBySlug(ctx context.Context, slug string) (CoinRow, error) {
	var (
		r                        CoinRow
		firstLedger, lastLedger  int64
		priceUSD, volume24hUSD   sql.NullString
		marketCapUSD, circSupply sql.NullString
		change24hPct             sql.NullString
	)
	err := s.db.QueryRowContext(ctx, getCoinBySlugSQL, slug).Scan(
		&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
		&firstLedger, &lastLedger, &r.ObservationCount,
		&priceUSD, &volume24hUSD, &marketCapUSD, &circSupply,
		&change24hPct,
	)
	if err != nil {
		return CoinRow{}, err
	}
	r.FirstSeenLedger = uint32(firstLedger) //nolint:gosec
	r.LastSeenLedger = uint32(lastLedger)   //nolint:gosec
	if priceUSD.Valid {
		r.PriceUSD = &priceUSD.String
	}
	if volume24hUSD.Valid {
		r.Volume24hUSD = &volume24hUSD.String
	}
	if marketCapUSD.Valid {
		r.MarketCapUSD = &marketCapUSD.String
	}
	if circSupply.Valid {
		r.CirculatingSupply = &circSupply.String
	}
	if change24hPct.Valid {
		r.Change24hPct = &change24hPct.String
	}
	return r, nil
}

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
