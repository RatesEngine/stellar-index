package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

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

// Pool is one (source, base, quote) tuple — same shape as Market
// but with the source dimension preserved. Returned by [Store.AllPools]
// to back the /v1/pools listing where the same pair traded on
// multiple venues becomes multiple rows.
type Pool struct {
	Source        string
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
	return s.distinctPairsCommon(ctx, "", cursor, limit, order)
}

// SourceMarkets returns one page of (base, quote) pairs the given
// source observed in the trailing MarketsRecencyWindow. Same shape
// as DistinctPairsExt but with `t.source = $source` filter applied
// before the GROUP BY — gives a per-DEX pool list with per-pool
// 24h volume + trade count + last-trade timestamp.
func (s *Store) SourceMarkets(ctx context.Context, source, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
	return s.distinctPairsCommon(ctx, source, cursor, limit, order)
}

// AllPools returns every (source, base, quote) tuple observed in
// the trailing MarketsRecencyWindow. Distinct from DistinctPairsExt
// which collapses across sources — same physical pair traded on
// soroswap + sdex returns ONE row from DistinctPairsExt and TWO
// rows from AllPools. Backs /v1/pools.
//
// `sources`, when non-empty, restricts the result to rows where
// t.source = ANY(sources). Empty means "all sources".
//
// Cursor format: "<vol_or_blank>:<source>|<base>|<quote>" for
// volume-desc; "<source>|<base>|<quote>" for pair-asc. Same
// keyset-pagination shape as DistinctPairsExt with the source
// dimension prepended.
func (s *Store) AllPools(ctx context.Context, sources []string, cursor string, limit int, order MarketsOrder) ([]Pool, string, error) { //nolint:gocognit // limit clamp + order branch + scan loop are linear; splitting would scatter the request lifecycle
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	q, args := buildPoolsQuery(since, sources, cursor, limit, order)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("timescale: AllPools: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Pool, 0, limit)
	hasMore := false
	n := 0
	for rows.Next() {
		var (
			source            string
			baseRaw, quoteRaw string
			lastAt            time.Time
			count24h          int64
			vol24hUSD         sql.NullString
		)
		if err := rows.Scan(&source, &baseRaw, &quoteRaw, &lastAt, &count24h, &vol24hUSD); err != nil {
			return nil, "", fmt.Errorf("timescale: AllPools scan: %w", err)
		}
		n++
		if n > limit {
			hasMore = true
			break
		}
		base, err := canonical.ParseAsset(baseRaw)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: AllPools base %q: %w", baseRaw, err)
		}
		quote, err := canonical.ParseAsset(quoteRaw)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: AllPools quote %q: %w", quoteRaw, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: AllPools pair: %w", err)
		}
		p := Pool{
			Source:        source,
			Pair:          pair,
			LastTradeAt:   lastAt.UTC(),
			TradeCount24h: count24h,
		}
		if vol24hUSD.Valid && vol24hUSD.String != "" && vol24hUSD.String != "0" {
			v := vol24hUSD.String
			p.Volume24hUSD = &v
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("timescale: AllPools rows: %w", err)
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		key := last.Source + "|" + last.Pair.Base.String() + "|" + last.Pair.Quote.String()
		if order == MarketsOrderVolume24hDesc {
			vol := ""
			if last.Volume24hUSD != nil {
				vol = *last.Volume24hUSD
			}
			nextCursor = vol + ":" + key
		} else {
			nextCursor = key
		}
	}
	return out, nextCursor, nil
}

func buildPoolsQuery(since time.Time, sources []string, cursor string, limit int, order MarketsOrder) (string, []any) {
	// vol_24h derives per-(source, base, quote) USD volume directly
	// from the trades hypertable rather than reading prices_1m's
	// per-(base, quote) totals. Two reasons:
	//
	//   1. Per-source attribution. Two DEXes trading the same (base,
	//      quote) pair (rare in practice — sdex uses classic credit
	//      assets, Soroban DEXes use SAC contract addresses — but
	//      possible) get their own slice rather than the cross-source
	//      sum.
	//   2. XLM-side fallback for Soroban trades. Phoenix / Aquarius /
	//      Comet trades against the XLM SAC wrapper
	//      (CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA)
	//      have NULL `usd_volume` because the operator's USD-pegged
	//      allow-list (Phase 1 of the L2.2 path) doesn't include XLM
	//      itself. We derive USD volume from base_amount × XLM/USD
	//      when XLM is on the base side, or quote_amount × XLM/USD
	//      when XLM is on the quote side. The XLM/USD price comes
	//      from the same on-chain XLM/USDC vwap that powers
	//      coins.go's xlm_usd CTE — single source of truth for the
	//      stablecoin-proxy policy.
	//
	// Trades that are neither already-priced (Phase 1) nor have an
	// XLM leg stay NULL in vol_usd — pure SEP-41/SEP-41 token
	// swaps need real per-token USD oracles, which is a separate
	// piece of work (see CHANGELOG #72).
	cte := `
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
        ),
        vol_24h AS (
          SELECT t.source, t.base_asset, t.quote_asset,
                 SUM(
                   CASE
                     WHEN t.usd_volume IS NOT NULL
                       THEN t.usd_volume::numeric
                     -- XLM SAC or native on the base side: use
                     -- base_amount × XLM/USD (classic 7-decimal
                     -- scale; SEP-41 token decimals only matter
                     -- for the OTHER side, which we're not pricing).
                     WHEN t.base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
                       THEN (t.base_amount / 1e7) * (SELECT vwap FROM xlm_usd)
                     -- XLM SAC or native on the quote side: use
                     -- quote_amount × XLM/USD.
                     WHEN t.quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
                       THEN (t.quote_amount / 1e7) * (SELECT vwap FROM xlm_usd)
                     ELSE NULL
                   END
                 )::text AS vol_usd
            FROM trades t
           WHERE t.ts >= NOW() - INTERVAL '24 hours'
           GROUP BY t.source, t.base_asset, t.quote_asset
        )
        SELECT t.source, t.base_asset, t.quote_asset,
               MAX(t.ts) AS last_trade_at,
               count(*) FILTER (WHERE t.ts > NOW() - INTERVAL '24 hours') AS count_24h,
               v.vol_usd AS vol_24h_usd
          FROM trades t
          LEFT JOIN vol_24h v
            ON v.source = t.source
           AND v.base_asset = t.base_asset
           AND v.quote_asset = t.quote_asset
         WHERE t.ts >= $1
    `
	if len(sources) > 0 {
		// `t.source = ANY($4)` constrains the trades scan before
		// the GROUP BY. Backed by trades_source_ledger_idx for
		// efficient chunk pruning when the source list is small.
		cte += ` AND t.source = ANY($4) `
	}
	if order == MarketsOrderVolume24hDesc {
		const tail = `
		 GROUP BY t.source, t.base_asset, t.quote_asset, v.vol_usd
		HAVING $2 = ''
		    OR COALESCE(v.vol_usd::numeric, 0)
		         <  CAST(NULLIF(split_part($2, ':', 1), '') AS numeric)
		    OR (
		         COALESCE(v.vol_usd::numeric, 0)
		         =  CAST(COALESCE(NULLIF(split_part($2, ':', 1), ''), '0') AS numeric)
		         AND (t.source || '|' || t.base_asset || '|' || t.quote_asset)
		             > substring($2 from position(':' in $2) + 1)
		       )
		 ORDER BY COALESCE(v.vol_usd::numeric, 0) DESC,
		          (t.source || '|' || t.base_asset || '|' || t.quote_asset) ASC
		 LIMIT $3
		`
		args := []any{since, cursor, limit + 1}
		if len(sources) > 0 {
			args = append(args, pq.Array(sources))
		}
		return cte + tail, args
	}
	const tail = `
	   AND ($2 = '' OR (t.source || '|' || t.base_asset || '|' || t.quote_asset) > $2)
	 GROUP BY t.source, t.base_asset, t.quote_asset, v.vol_usd
	 ORDER BY (t.source || '|' || t.base_asset || '|' || t.quote_asset) ASC
	 LIMIT $3
	`
	args := []any{since, cursor, limit + 1}
	if len(sources) > 0 {
		args = append(args, pq.Array(sources))
	}
	return cte + tail, args
}

func (s *Store) distinctPairsCommon(ctx context.Context, source, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
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
	// Overfetch by one (LIMIT $N = limit+1) to detect "more pages
	// exist". The extra row isn't returned to the caller; its only
	// purpose is to toggle whether we emit a nextCursor.
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	q, args := buildDistinctPairsQuery(since, source, cursor, limit, order)
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
func buildDistinctPairsQuery(since time.Time, source, cursor string, limit int, order MarketsOrder) (string, []any) {
	// LEFT JOIN against vol_24h once instead of correlating the
	// subquery into SELECT + HAVING + ORDER BY. The previous
	// shape had the planner evaluate
	//   (SELECT vol_usd FROM vol_24h WHERE base = t.base AND quote = t.quote)
	// up to four times per output row (SELECT + 2× HAVING + ORDER BY)
	// and that compounded with the trades-side group, producing
	// 30s+ cold-cache p99. With a single LEFT JOIN the planner
	// resolves vol once per (base, quote) tuple; warm cache stays
	// at <100ms but cold cache drops by >10×.
	//
	// When source != "" the inner trades scan is also filtered to
	// `t.source = $source` and the vol_24h CTE narrows to the same
	// source's prices_1m rows so per-source volume is comparable
	// to the per-source trade count.
	cte := `
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
               v.vol_usd AS vol_24h_usd
          FROM trades t
          LEFT JOIN vol_24h v
            ON v.base_asset = t.base_asset
           AND v.quote_asset = t.quote_asset
         WHERE t.ts >= $1
    `
	if source != "" {
		// Source filter binds to a fixed argument index (always $4)
		// regardless of which order branch we take below — the cursor
		// branches use $1/$2/$3 for since/cursor/limit; appending
		// source as $4 keeps the existing predicates unchanged.
		cte += ` AND t.source = $4 `
	}
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
		 GROUP BY t.base_asset, t.quote_asset, v.vol_usd
		HAVING $2 = ''
		    OR COALESCE(v.vol_usd::numeric, 0)
		         <  CAST(NULLIF(split_part($2, ':', 1), '') AS numeric)
		    OR (
		         COALESCE(v.vol_usd::numeric, 0)
		         =  CAST(COALESCE(NULLIF(split_part($2, ':', 1), ''), '0') AS numeric)
		         AND (t.base_asset || '|' || t.quote_asset)
		             > substring($2 from position(':' in $2) + 1)
		       )
		 ORDER BY COALESCE(v.vol_usd::numeric, 0) DESC,
		          (t.base_asset || '|' || t.quote_asset) ASC
		 LIMIT $3
		`
		args := []any{since, cursor, limit + 1}
		if source != "" {
			args = append(args, source)
		}
		return cte + tail, args
	default: // MarketsOrderPair
		const tail = `
		   AND ($2 = '' OR (t.base_asset || '|' || t.quote_asset) > $2)
		 GROUP BY t.base_asset, t.quote_asset, v.vol_usd
		 ORDER BY (t.base_asset || '|' || t.quote_asset) ASC
		 LIMIT $3
		`
		args := []any{since, cursor, limit + 1}
		if source != "" {
			args = append(args, source)
		}
		return cte + tail, args
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

// PairVolumePoint is one hourly USD-volume sample for a single
// (base, quote) pair, used by the per-pair sparkline endpoint.
// Hour is the bucket start (UTC); VolumeUSD is numeric-stringified
// for full precision through the JSON boundary.
type PairVolumePoint struct {
	Hour      time.Time
	VolumeUSD string
}

// pairKey is the lookup key for the batched per-pair volume map.
// Wire shape stays string-based (`<base>|<quote>`) so the v1 API
// adapter doesn't have to import canonical.Pair.
type pairKey = string

// GetPairsVolumeHistory24hBatch returns per-(base, quote) hourly
// USD-volume buckets for the trailing 24h, suitable for the
// /markets / /v1/markets sparkline column. Single CTE pass keyed
// by ANY($1) on the pair tuple text.
//
// Unlike the per-source variant, this query reads volume_usd
// directly from prices_1m — pairs aggregated across all sources
// match the wire shape /v1/markets already returns. Holes are
// zero-filled so each per-pair series always has 24 entries
// oldest → newest.
func (s *Store) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[pairKey][]PairVolumePoint, error) {
	if len(pairs) == 0 {
		return map[pairKey][]PairVolumePoint{}, nil
	}
	keys := make([]string, len(pairs))
	for i, p := range pairs {
		keys[i] = p[0] + "|" + p[1]
	}
	const q = `
		WITH hours AS (
		  SELECT generate_series(
		    date_trunc('hour', now() - INTERVAL '23 hours'),
		    date_trunc('hour', now()),
		    INTERVAL '1 hour'
		  ) AS bucket
		),
		want AS (
		  SELECT split_part(k, '|', 1) AS base_asset,
		         split_part(k, '|', 2) AS quote_asset,
		         k                       AS pair_key
		    FROM unnest($1::text[]) k
		),
		per_hour AS (
		  SELECT base_asset || '|' || quote_asset AS pair_key,
		         date_trunc('hour', bucket)        AS h,
		         SUM(volume_usd)::text             AS vol
		    FROM prices_1m
		   WHERE bucket >= date_trunc('hour', now() - INTERVAL '23 hours')
		     AND volume_usd IS NOT NULL
		     AND (base_asset || '|' || quote_asset) = ANY($1)
		   GROUP BY pair_key, h
		)
		SELECT w.pair_key,
		       to_char(hours.bucket, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS t,
		       COALESCE(p.vol, '0') AS v
		  FROM want w
		  CROSS JOIN hours
		  LEFT JOIN per_hour p ON p.pair_key = w.pair_key AND p.h = hours.bucket
		 ORDER BY w.pair_key, hours.bucket ASC
	`
	rows, err := s.db.QueryContext(ctx, q, pq.Array(keys))
	if err != nil {
		return nil, fmt.Errorf("timescale: GetPairsVolumeHistory24hBatch: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[pairKey][]PairVolumePoint, len(pairs))
	for rows.Next() {
		var key, ts, vol string
		if err := rows.Scan(&key, &ts, &vol); err != nil {
			return nil, fmt.Errorf("timescale: GetPairsVolumeHistory24hBatch scan: %w", err)
		}
		hour, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			continue
		}
		out[key] = append(out[key], PairVolumePoint{Hour: hour, VolumeUSD: vol})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: GetPairsVolumeHistory24hBatch rows: %w", err)
	}
	return out, nil
}
