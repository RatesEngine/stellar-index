package timescale

import (
	"context"
	"database/sql"
	"fmt"
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
}

// ListCoins returns coin-directory rows ordered by observation
// count desc (a cheap proxy for activity).
//
// Pagination uses a keyset cursor: the cursor encodes the
// (observation_count, asset_id) tuple of the last row from the
// previous page. Empty cursor means "first page". Cursor format:
// `<observation_count>:<asset_id>`.
//
// issuer, when non-empty, filters to assets minted by that
// G-strkey — used by the explorer to deep-link from /issuers
// into "assets by this issuer."
func (s *Store) ListCoins(ctx context.Context, limit int, issuer, cursor string) ([]CoinRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query, args := buildCoinsQuery(limit, issuer, cursor)
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
		)
		if err := rows.Scan(
			&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
			&firstLedger, &lastLedger, &r.ObservationCount,
			&priceUSD, &volume24hUSD, &marketCapUSD, &circulatingSupply,
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
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: ListCoins rows: %w", err)
	}
	return out, nil
}

// buildCoinsQuery composes the SELECT + WHERE + ORDER + LIMIT
// for ListCoins, given the limit / issuer-filter / keyset
// cursor. Pulled out of ListCoins so the latter stays under the
// gocognit threshold; same SQL surface area, just cleanly
// separated from the row-scanning logic.
func buildCoinsQuery(limit int, issuer, cursor string) (string, []any) {
	cursorObsCount, cursorAssetID := parseCoinCursor(cursor)

	// Volume aggregation comes from prices_1m by summing
	// volume_usd across the trailing 24h, where the asset
	// participates as base OR quote. This sidesteps two
	// problems: classic_asset_stats_5m is unwritten today
	// (migration shipped without a writer); and most Stellar
	// classic assets don't have a direct fiat:USD pair so a
	// LATERAL price-by-pair lookup returns null for them.
	//
	// Price + market_cap_usd + circulating_supply are NOT
	// joined here. Their proper sources are the aggregator's
	// stablecoin-policy USD-price pipeline and the supply
	// pipeline (asset_supply_history) — neither of which is
	// running for the long tail of classic assets today.
	// Surfacing fabricated values would defeat the whole
	// "stop lying" rule. They render as null until the
	// pipelines are wired.
	const baseSelect = `
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
		)
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id,
		    ca.code,
		    ca.issuer_g_strkey,
		    ca.first_seen_ledger,
		    ca.last_seen_ledger,
		    ca.observation_count,
		    NULL::numeric                         AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply
		  FROM classic_assets ca
		  LEFT JOIN per_asset_24h_vol vol ON vol.asset_id = ca.asset_id
	`
	hasCursor := cursor != ""
	switch {
	case issuer == "" && !hasCursor:
		return baseSelect + `
		 ORDER BY ca.observation_count DESC, ca.asset_id ASC
		 LIMIT $1`, []any{limit}
	case issuer == "" && hasCursor:
		// Keyset: rows AFTER (cursorObsCount, cursorAssetID) under
		// the (obs_count DESC, asset_id ASC) sort.
		return baseSelect + `
		 WHERE (ca.observation_count, ca.asset_id) < ($1, $2)
		 ORDER BY ca.observation_count DESC, ca.asset_id ASC
		 LIMIT $3`, []any{cursorObsCount, cursorAssetID, limit}
	case issuer != "" && !hasCursor:
		return baseSelect + `
		 WHERE ca.issuer_g_strkey = $1
		 ORDER BY ca.observation_count DESC, ca.asset_id ASC
		 LIMIT $2`, []any{issuer, limit}
	default: // issuer != "" && hasCursor
		return baseSelect + `
		 WHERE ca.issuer_g_strkey = $1
		   AND (ca.observation_count, ca.asset_id) < ($2, $3)
		 ORDER BY ca.observation_count DESC, ca.asset_id ASC
		 LIMIT $4`, []any{issuer, cursorObsCount, cursorAssetID, limit}
	}
}

// GetCoinBySlug returns one row matching the given slug. Returns
// sql.ErrNoRows when the slug doesn't match a known classic asset.
//
// Mirrors ListCoins's per-row metric shape (price/volume/market cap/
// supply) so the explorer can render an asset detail page from a
// single endpoint without scanning the top-N listing first.
func (s *Store) GetCoinBySlug(ctx context.Context, slug string) (CoinRow, error) {
	const q = `
		WITH per_asset_24h_vol AS (
		  SELECT asset_id, SUM(volume_usd) AS vol_usd
		    FROM (
		      SELECT base_asset  AS asset_id, volume_usd
		        FROM prices_1m
		       WHERE bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		         AND base_asset = (
		           SELECT asset_id FROM classic_assets
		            WHERE COALESCE(slug, code) = $1 LIMIT 1
		         )
		      UNION ALL
		      SELECT quote_asset AS asset_id, volume_usd
		        FROM prices_1m
		       WHERE bucket >= now() - INTERVAL '24 hours'
		         AND bucket  <  now()
		         AND volume_usd IS NOT NULL
		         AND quote_asset = (
		           SELECT asset_id FROM classic_assets
		            WHERE COALESCE(slug, code) = $1 LIMIT 1
		         )
		    ) t
		   GROUP BY asset_id
		)
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id, ca.code, ca.issuer_g_strkey,
		    ca.first_seen_ledger, ca.last_seen_ledger, ca.observation_count,
		    NULL::numeric                         AS price_usd,
		    vol.vol_usd                           AS volume_24h_usd,
		    NULL::numeric                         AS market_cap_usd,
		    NULL::numeric                         AS circulating_supply
		  FROM classic_assets ca
		  LEFT JOIN per_asset_24h_vol vol ON vol.asset_id = ca.asset_id
		 WHERE COALESCE(ca.slug, ca.code) = $1
		 ORDER BY (ca.slug = $1) DESC NULLS LAST,
		          ca.observation_count DESC,
		          ca.asset_id ASC
		 LIMIT 1
	`
	var (
		r                        CoinRow
		firstLedger, lastLedger  int64
		priceUSD, volume24hUSD   sql.NullString
		marketCapUSD, circSupply sql.NullString
	)
	err := s.db.QueryRowContext(ctx, q, slug).Scan(
		&r.Slug, &r.AssetID, &r.Code, &r.IssuerGStrkey,
		&firstLedger, &lastLedger, &r.ObservationCount,
		&priceUSD, &volume24hUSD, &marketCapUSD, &circSupply,
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
