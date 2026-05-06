package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Market is one distinct (base, quote) pair summary with activity
// statistics. Returned by [Store.DistinctPairs].
//
// Volume24hUSD is the trailing-24h USD volume summed from the
// prices_1m hypertable (which has per-bucket volume_usd
// computed by the aggregator). Pointer + nil-when-zero so a
// pair with no USD-equivalent trades emits JSON null rather
// than "0" — important for downstream filtering.
type Market struct {
	Pair          canonical.Pair
	LastTradeAt   time.Time
	TradeCount24h int64
	Volume24hUSD  *string
}

// MarketsRecencyWindow bounds the trades scanned by DistinctPairs.
// Pairs that haven't traded inside the window are excluded from the
// `/v1/markets` listing — the public contract is "active markets",
// not "every pair ever observed". The window is exposed as a var so
// tests can override it without changing the public function signature.
//
// Empirical sizing on r1 at the 14-day default:
//   - 2026-04 baseline (441M trades, 1100+ chunks, no concurrent
//     backfill): ~540 ms cold, ~50 ms warm.
//   - 2026-05-04 measurement (539M trades, 787 chunks, 16-way
//     parallel backfill running across 50M-62M ledger range):
//     ~7 s cold first call, ~400 ms warm steady-state.
//
// The cold-call regression is dominated by buffer-cache eviction
// from the concurrent backfill — recent chunks are pushed out and
// the GROUP BY across the 14-day window has to re-fault them in.
// Steady-state warm at 400 ms is also ~8x the original 50 ms because
// the trades hypertable has grown ~22 % and the chunks at the window
// boundary are still being column-store-compressed asynchronously.
// Once the backfill completes and the columnstore policy catches up
// the warm baseline should approach the original 50 ms again.
//
// 30-day: ~9 s with JIT, ~3 s without — too slow for a hot path.
// 90-day: ~16-19 s — exceeded the 30s client deadline.
//
// 14 days passes the "active markets" intuition (a market that
// hasn't traded in two weeks isn't really active) and the
// COMPRESSED chunks at the boundary fit in postgres's shared
// buffers. A future materialised market_catalogue would let us
// drop the recency bound entirely.
var MarketsRecencyWindow = 14 * 24 * time.Hour

// MarketsOrder controls the sort + cursor scheme used by
// DistinctPairs. Default is alphabetic by `<base>|<quote>` (stable
// pagination through the entire 14-day-active set). Volume24hDesc
// is used by the explorer's `/markets` page so the most active
// USD-volume pairs surface in the first page rather than being
// alphabetically deep behind ~5K dust pairs.
type MarketsOrder int

const (
	// MarketsOrderPair = ORDER BY (base|quote) ASC. Cursor is the
	// (base|quote) string. Stable; iterates the full set.
	MarketsOrderPair MarketsOrder = iota
	// MarketsOrderVolume24hDesc = ORDER BY volume_24h_usd DESC NULLS
	// LAST, (base|quote) ASC. Cursor is `<vol_or_blank>:<base|quote>`.
	// Pairs with USD volume surface first; the long-tail dust comes
	// last. Useful for "what are the active markets" queries.
	MarketsOrderVolume24hDesc
)

// DistinctPairs returns one page of recently-active (base, quote)
// pairs from the trades hypertable, each with its most-recent trade
// timestamp, 24h trade count, and 24h USD volume.
//
// Pagination is cursor-based; the cursor format depends on
// `order`. Empty cursor starts from the beginning.
//
// limit clamps to [1, 500] — matching DistinctAssets for consistency.
//
// Recency window: the query scans only chunks within the last
// MarketsRecencyWindow (default 14d) so chunk pruning bounds I/O on
// a hypertable with hundreds of millions of trades.
func (s *Store) DistinctPairs(ctx context.Context, cursor string, limit int) ([]Market, string, error) {
	return s.DistinctPairsExt(ctx, cursor, limit, MarketsOrderPair)
}

// DistinctPairsExt is DistinctPairs with explicit ordering control.
// DistinctPairs is preserved as the legacy 3-arg form so existing
// callers compile unchanged.
func (s *Store) DistinctPairsExt(ctx context.Context, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	// `since` is computed Go-side instead of `NOW() - INTERVAL` so
	// the planner sees a constant timestamp parameter and can prune
	// chunks at plan time rather than relying on stable-function
	// evaluation. count_24h uses FILTER (more readable than the
	// SUM/CASE form, identical plan).
	//
	// Overfetch by one (LIMIT $3 = limit+1) to detect "more pages
	// exist". The extra row isn't returned to the caller; its only
	// purpose is to toggle whether we emit a nextCursor.
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	// LEFT JOIN to a per-pair 24h volume_usd CTE rather than
	// folding SUM into the main GROUP BY — the trades table has
	// no volume_usd column (it's computed at aggregation time),
	// and joining a small (one-row-per-active-pair) CTE keeps
	// the trades scan unchanged. prices_1m's PRIMARY KEY is
	// (base_asset, quote_asset, bucket) so the GROUP BY there
	// is index-driven.
	q, args := buildDistinctPairsQuery(since, cursor, limit, order)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("timescale: DistinctPairs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out, hasMore, err := scanDistinctPairs(rows, limit)
	if err != nil {
		return nil, "", err
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		nextCursor = encodeMarketsCursor(out[len(out)-1], order)
	}
	return out, nextCursor, nil
}

// scanDistinctPairs reads up to `limit+1` rows; the +1th row toggles
// hasMore. Pulled out of DistinctPairsExt so the latter stays under
// the gocognit threshold.
func scanDistinctPairs(rows *sql.Rows, limit int) ([]Market, bool, error) {
	out := make([]Market, 0, limit)
	n := 0
	hasMore := false
	for rows.Next() {
		var (
			baseRaw, quoteRaw string
			lastAt            time.Time
			count24h          int64
			vol24hUSD         sql.NullString
		)
		if err := rows.Scan(&baseRaw, &quoteRaw, &lastAt, &count24h, &vol24hUSD); err != nil {
			return nil, false, fmt.Errorf("timescale: DistinctPairs scan: %w", err)
		}
		n++
		if n > limit {
			hasMore = true
			break
		}
		m, err := buildMarketRow(baseRaw, quoteRaw, lastAt, count24h, vol24hUSD)
		if err != nil {
			return nil, false, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("timescale: DistinctPairs rows: %w", err)
	}
	return out, hasMore, nil
}

func buildMarketRow(baseRaw, quoteRaw string, lastAt time.Time, count24h int64, vol24hUSD sql.NullString) (Market, error) {
	base, err := canonical.ParseAsset(baseRaw)
	if err != nil {
		return Market{}, fmt.Errorf("timescale: DistinctPairs base %q: %w", baseRaw, err)
	}
	quote, err := canonical.ParseAsset(quoteRaw)
	if err != nil {
		return Market{}, fmt.Errorf("timescale: DistinctPairs quote %q: %w", quoteRaw, err)
	}
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		return Market{}, fmt.Errorf("timescale: DistinctPairs pair: %w", err)
	}
	m := Market{
		Pair:          pair,
		LastTradeAt:   lastAt.UTC(),
		TradeCount24h: count24h,
	}
	if vol24hUSD.Valid && vol24hUSD.String != "" && vol24hUSD.String != "0" {
		v := vol24hUSD.String
		m.Volume24hUSD = &v
	}
	return m, nil
}

// encodeMarketsCursor formats the last-row cursor for the active
// ordering — pair-only for MarketsOrderPair, `<vol>:<pair>` for
// MarketsOrderVolume24hDesc.
func encodeMarketsCursor(last Market, order MarketsOrder) string {
	pairKey := last.Pair.Base.String() + "|" + last.Pair.Quote.String()
	if order == MarketsOrderVolume24hDesc {
		vol := ""
		if last.Volume24hUSD != nil {
			vol = *last.Volume24hUSD
		}
		return vol + ":" + pairKey
	}
	return pairKey
}

// buildDistinctPairsQuery composes the per-pair-volume CTE +
// SELECT for DistinctPairs given the limit and ordering. Pulled
// out of DistinctPairs so the latter stays under the gocognit
// threshold and the two ordering branches are readable side-by-
// side.
//
// Cursor formats:
//   - MarketsOrderPair          — `<base>|<quote>`. Strict-greater
//     comparison resumes pagination.
//   - MarketsOrderVolume24hDesc — `<vol_or_blank>:<base>|<quote>`.
//     Empty vol prefix sorts as 0 (so cursor "":<pair> resumes
//     into the null-volume tail). The compare on (vol, pair) is
//     strict-less for vol DESC, strict-greater for pair ASC —
//     the standard keyset tuple-comparison trick is adapted to
//     mixed ordering.
func buildDistinctPairsQuery(since time.Time, cursor string, limit int, order MarketsOrder) (string, []any) {
	const cte = `
        WITH vol_24h AS (
          SELECT base_asset, quote_asset,
                 SUM(volume_usd)::text AS vol_usd
            FROM prices_1m
           WHERE bucket >= NOW() - INTERVAL '24 hours'
             AND volume_usd IS NOT NULL
           GROUP BY base_asset, quote_asset
        )
        SELECT t.base_asset, t.quote_asset,
               MAX(t.ts) AS last_trade_at,
               count(*) FILTER (WHERE t.ts > NOW() - INTERVAL '24 hours') AS count_24h,
               (SELECT vol_usd FROM vol_24h v
                 WHERE v.base_asset = t.base_asset
                   AND v.quote_asset = t.quote_asset) AS vol_24h_usd
          FROM trades t
         WHERE t.ts >= $1
    `
	switch order {
	case MarketsOrderVolume24hDesc:
		// Cursor: "<vol_or_blank>:<base>|<quote>". Two-tuple keyset
		// against (vol_24h_usd::numeric, base|quote). Use
		// COALESCE-to-zero on the column so NULLS LAST + the
		// numeric comparison agree (NULL < 0 in numeric is false,
		// so we map NULL → 0 for ordering purposes; same effect
		// as NULLS LAST since real volumes are positive).
		//
		// HAVING (vol, pair) `<` (cursor_vol, cursor_pair) won't
		// work directly because the "next page" relation is
		// `(v < cv) OR (v = cv AND pair > cpair)` — DESC on the
		// first key flips the comparator. Encoded explicitly.
		const tail = `
		 GROUP BY t.base_asset, t.quote_asset
		HAVING $2 = ''
		    OR COALESCE((SELECT vol_usd FROM vol_24h v
		                  WHERE v.base_asset = t.base_asset
		                    AND v.quote_asset = t.quote_asset)::numeric, 0)
		         <  CAST(NULLIF(split_part($2, ':', 1), '') AS numeric)
		    OR (
		         COALESCE((SELECT vol_usd FROM vol_24h v
		                    WHERE v.base_asset = t.base_asset
		                      AND v.quote_asset = t.quote_asset)::numeric, 0)
		         =  CAST(COALESCE(NULLIF(split_part($2, ':', 1), ''), '0') AS numeric)
		         AND (t.base_asset || '|' || t.quote_asset)
		             > substring($2 from position(':' in $2) + 1)
		       )
		 ORDER BY COALESCE((SELECT vol_usd FROM vol_24h v
		                     WHERE v.base_asset = t.base_asset
		                       AND v.quote_asset = t.quote_asset)::numeric, 0) DESC,
		          (t.base_asset || '|' || t.quote_asset) ASC
		 LIMIT $3
		`
		return cte + tail, []any{since, cursor, limit + 1}
	default: // MarketsOrderPair
		const tail = `
		   AND ($2 = '' OR (t.base_asset || '|' || t.quote_asset) > $2)
		 GROUP BY t.base_asset, t.quote_asset
		 ORDER BY (t.base_asset || '|' || t.quote_asset) ASC
		 LIMIT $3
		`
		return cte + tail, []any{since, cursor, limit + 1}
	}
}

// PairMarket returns the activity summary for a single (base, quote)
// pair. The bool result is false when the pair hasn't traded inside
// MarketsRecencyWindow; callers translate that to an empty list
// (200 OK) per the /v1/pairs envelope contract — not a 404 — to
// match the "array of MarketRow" spec shape.
//
// Recency window: scoped to the last MarketsRecencyWindow so chunk
// pruning bounds I/O on a hypertable with hundreds of millions of
// trades, and so the result is consistent with DistinctPairs (a
// pair that DistinctPairs hides should also be hidden here).
func (s *Store) PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error) {
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	const q = `
        SELECT MAX(t.ts) AS last_trade_at,
               count(*) FILTER (WHERE t.ts > NOW() - INTERVAL '24 hours') AS count_24h,
               (SELECT SUM(volume_usd)::text FROM prices_1m
                 WHERE base_asset = $2 AND quote_asset = $3
                   AND bucket >= NOW() - INTERVAL '24 hours'
                   AND volume_usd IS NOT NULL) AS vol_24h_usd
          FROM trades t
         WHERE t.ts >= $1
           AND t.base_asset = $2 AND t.quote_asset = $3
    `
	var (
		lastAt    *time.Time
		count24h  *int64
		vol24hUSD sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, q, since, base.String(), quote.String()).Scan(&lastAt, &count24h, &vol24hUSD); err != nil {
		return Market{}, false, fmt.Errorf("timescale: PairMarket: %w", err)
	}
	if lastAt == nil {
		return Market{}, false, nil
	}
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		return Market{}, false, fmt.Errorf("timescale: PairMarket pair: %w", err)
	}
	var n int64
	if count24h != nil {
		n = *count24h
	}
	m := Market{
		Pair:          pair,
		LastTradeAt:   lastAt.UTC(),
		TradeCount24h: n,
	}
	if vol24hUSD.Valid && vol24hUSD.String != "" && vol24hUSD.String != "0" {
		v := vol24hUSD.String
		m.Volume24hUSD = &v
	}
	return m, true, nil
}
