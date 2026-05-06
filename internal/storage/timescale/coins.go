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

	// Per-row joins:
	//   stats — latest classic_asset_stats_5m bucket per asset
	//           gives volume_24h_usd + outstanding_supply.
	//   price — latest prices_1m bucket per asset against
	//           fiat:USD gives the live VWAP.
	// LATERAL ... LIMIT 1 makes both joins index-friendly: the
	// (asset_id, bucket DESC) index on classic_asset_stats_5m and
	// the equivalent on prices_1m turn each row's lookup into an
	// O(1) index seek.
	const baseSelect = `
		SELECT
		    COALESCE(ca.slug, ca.code)            AS slug,
		    ca.asset_id,
		    ca.code,
		    ca.issuer_g_strkey,
		    ca.first_seen_ledger,
		    ca.last_seen_ledger,
		    ca.observation_count,
		    price.vwap                            AS price_usd,
		    stats.volume_24h_usd                  AS volume_24h_usd,
		    CASE
		      WHEN price.vwap IS NOT NULL AND stats.outstanding_supply IS NOT NULL
		      THEN price.vwap * stats.outstanding_supply
		      ELSE NULL
		    END                                   AS market_cap_usd,
		    stats.outstanding_supply              AS circulating_supply
		  FROM classic_assets ca
		  LEFT JOIN LATERAL (
		    SELECT volume_24h_usd, outstanding_supply
		      FROM classic_asset_stats_5m
		     WHERE asset_id = ca.asset_id
		     ORDER BY bucket DESC
		     LIMIT 1
		  ) stats ON TRUE
		  LEFT JOIN LATERAL (
		    SELECT vwap
		      FROM prices_1m
		     WHERE base_asset = ca.asset_id
		       AND quote_asset = 'fiat:USD'
		     ORDER BY bucket DESC
		     LIMIT 1
		  ) price ON TRUE
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
