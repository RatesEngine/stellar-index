package timescale

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// Market is one distinct (base, quote) pair summary with activity
// statistics. Returned by [Store.DistinctPairs].
//
// Volume24hUSD is the trailing-24h USD volume summed from the
// prices_1m hypertable (which has per-bucket volume_usd
// computed by the aggregator). Pointer + nil-when-zero so a
// pair with no USD-equivalent trades emits JSON null rather
// than "0" — important for downstream filtering.
//
// Field semantics:
//
//   - LastTradeAt — minute-precise start of the most recent
//     prices_1m bucket that observed a trade in this pair, within
//     the 24h window. Falls back to BucketCloseAt for pairs that
//     traded 14d-active-but-24h-idle (no minute-precise signal in
//     the 24h scan window).
//   - BucketCloseAt — start-of-day UTC of the prices_1d bucket the
//     pair was last active in. Always populated. Aligns to UTC
//     midnight by construction (`time_bucket('1 day', ts)`); pre
//     2026-05-27 this was misnamed `last_trade_at`, but most pairs
//     surfaced exactly-midnight values and clients computing
//     freshness against `now()` saw spuriously-large staleness
//     (F-0065).
type Market struct {
	Pair          canonical.Pair
	LastTradeAt   time.Time
	BucketCloseAt time.Time
	TradeCount24h int64
	Volume24hUSD  *string
	// LastPrice is the last quote-per-base price observed in
	// prices_1m for this pair. nil when no recent bucket has a
	// non-null `last_price` (cold pair, freshly-ingested fixture,
	// etc.). Numeric-stringified for precision parity.
	LastPrice *string
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
	// LastPrice is the last quote-per-base price for this
	// (source, base, quote) tuple — same wire shape as
	// Market.LastPrice but per-pool.
	LastPrice *string
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
	return s.distinctPairsCommon(ctx, "", "", cursor, limit, order)
}

// SourceMarkets returns one page of (base, quote) pairs the given
// source observed in the trailing MarketsRecencyWindow. Same shape
// as DistinctPairsExt but with `t.source = $source` filter applied
// before the GROUP BY — gives a per-DEX pool list with per-pool
// 24h volume + trade count + last-trade timestamp.
func (s *Store) SourceMarkets(ctx context.Context, source, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
	return s.distinctPairsCommon(ctx, source, "", cursor, limit, order)
}

// AssetMarkets returns one page of (base, quote) pairs where the
// given canonical asset_id appears on either side of the pair —
// `t.base_asset = $asset OR t.quote_asset = $asset`. Backs the
// `/v1/markets?asset=<asset_id>` query that the explorer's
// /assets/{slug} Markets tab uses to surface every market the
// asset participates in without paying for a 500-row global scan
// + client-side filter (the previous shape).
func (s *Store) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
	return s.distinctPairsCommon(ctx, "", asset, cursor, limit, order)
}

// PoolsFilter narrows AllPools by venue and/or pair. Zero-value
// fields mean "no filter on this dimension"; an unfiltered call
// uses PoolsFilter{}.
//
// Pair filter (Base + Quote both non-empty) is the canonical
// per-pair source-contribution query — used by the pair detail
// page to render "which venues moved this pair in the last 24h".
// Single-side filters (Base only / Quote only) are accepted but
// uncommon.
//
// Asset filter is the OR-shape — base = X OR quote = X. Used by
// asset-detail surfaces ("every pool touching this asset")
// without forcing the caller to fire two parallel `?base=` +
// `?quote=` requests and merge client-side. Mutually exclusive
// with Base/Quote at the handler layer (the storage layer
// accepts the combination, but its semantics aren't
// well-defined: an asset-OR + base-AND would land on the
// intersection or the union depending on interpretation).
type PoolsFilter struct {
	Sources []string
	Base    string
	Quote   string
	Asset   string
}

// AllPools returns every (source, base, quote) tuple observed in
// the trailing MarketsRecencyWindow. Distinct from DistinctPairsExt
// which collapses across sources — same physical pair traded on
// soroswap + sdex returns ONE row from DistinctPairsExt and TWO
// rows from AllPools. Backs /v1/pools.
//
// `filter.Sources` constrains to a venue allowlist; `filter.Base` /
// `filter.Quote` constrain by canonical asset_id. Empty fields
// mean no filter.
//
// Cursor format: "<vol_or_blank>:<source>|<base>|<quote>" for
// volume-desc; "<source>|<base>|<quote>" for pair-asc. Same
// keyset-pagination shape as DistinctPairsExt with the source
// dimension prepended.
func (s *Store) AllPools(ctx context.Context, filter PoolsFilter, cursor string, limit int, order MarketsOrder) ([]Pool, string, error) { //nolint:gocognit // limit clamp + order branch + scan loop are linear; splitting would scatter the request lifecycle
	if limit < 1 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	since := time.Now().UTC().Add(-MarketsRecencyWindow)
	q, args := buildPoolsQuery(since, filter, cursor, limit, order)
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
			lastPrice         sql.NullString
		)
		if err := rows.Scan(&source, &baseRaw, &quoteRaw, &lastAt, &count24h, &vol24hUSD, &lastPrice); err != nil {
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
		if lastPrice.Valid && lastPrice.String != "" {
			v := lastPrice.String
			p.LastPrice = &v
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

func buildPoolsQuery(since time.Time, filter PoolsFilter, cursor string, limit int, order MarketsOrder) (string, []any) { //nolint:funlen // CTE + select + 2 ordering branches form one query template; splitting would scatter the SQL across helpers
	// Pre-#25 this query scanned the trades hypertable three times:
	// once for the vol_24h CTE (24h SUM grouped by source+pair),
	// once for the last_px CTE (DISTINCT ON per-source latest price),
	// and once for the outer FROM trades enumeration (14d window
	// LEFT JOINing both CTEs). Measured 8-30s on a populated 2.7B-
	// row hypertable; #23 wrapped the result in stale-while-
	// revalidate so user requests stayed sub-ms warm but cold-fill
	// + the background refresh both paid the full cost.
	//
	// Post-#25 every read collapses into a single scan of the
	// pools_per_source_1h continuous aggregate (migration 0036).
	// One row per (source, base, quote, 1h bucket) holds
	// SUM(usd_volume) split by Phase-1-priced vs needs-XLM-fallback,
	// COUNT(*), and last(quote_amount/base_amount, ts). The
	// `pools` CTE re-aggregates those hourly rows over the 14d
	// window with FILTER clauses pulling the 24h slice for
	// vol_24h_usd + count_24h. Trade-off: last_trade_at lags by up
	// to one refresh interval (5 min) — acceptable for a pools
	// discovery surface.
	//
	// XLM-fallback semantics preserved exactly: priced trades
	// contribute their stored usd_volume; unpriced trades with an
	// XLM leg contribute base_amount × XLM/USD (or quote_amount
	// × XLM/USD); pure-SEP-41/SEP-41 unpriced trades stay 0 (the
	// pre-#25 query returned NULL; the handler scan collapses
	// NULL and "0" identically, so functionally equivalent).
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
        pools AS (
          SELECT
            p.source, p.base_asset, p.quote_asset,
            MAX(p.bucket_last_ts) AS last_trade_at,
            COALESCE(SUM(p.trade_count)
                     FILTER (WHERE p.bucket >= NOW() - INTERVAL '24 hours'), 0) AS count_24h,
            (
              COALESCE(SUM(p.sum_usd_priced)
                       FILTER (WHERE p.bucket >= NOW() - INTERVAL '24 hours'), 0)
              +
              CASE
                WHEN p.base_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
                  THEN COALESCE(SUM(p.sum_base_unpriced)
                                FILTER (WHERE p.bucket >= NOW() - INTERVAL '24 hours'), 0) / 1e7
                       * COALESCE((SELECT vwap FROM xlm_usd), 0)
                WHEN p.quote_asset IN ('native', 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA')
                  THEN COALESCE(SUM(p.sum_quote_unpriced)
                                FILTER (WHERE p.bucket >= NOW() - INTERVAL '24 hours'), 0) / 1e7
                       * COALESCE((SELECT vwap FROM xlm_usd), 0)
                ELSE 0
              END
            )::text AS vol_24h_usd,
            last(p.bucket_last_price, p.bucket_last_ts)::text AS last_price
          FROM pools_per_source_1h p
         WHERE p.bucket >= $1
    `
	// $4 sources, $5 base, $6 quote, $7 asset are always bound;
	// empty values short-circuit each predicate so the planner
	// skips it. Keeps the positional-arg layout stable across
	// filter combinations (extending the slice would require
	// renumbering downstream).
	//
	// $7 asset is the OR-shape (base = X OR quote = X), distinct
	// from $5/$6's AND-shape — used by asset-detail surfaces that
	// want every pool touching an asset on either side without
	// firing two parallel `?base=` + `?quote=` requests and
	// merging client-side.
	cte += `
           AND (cardinality($4::text[]) = 0 OR p.source = ANY($4))
           AND ($5 = '' OR p.base_asset = $5)
           AND ($6 = '' OR p.quote_asset = $6)
           AND ($7 = '' OR p.base_asset = $7 OR p.quote_asset = $7)
         GROUP BY p.source, p.base_asset, p.quote_asset
        )
    `
	if order == MarketsOrderVolume24hDesc {
		const tail = `
		 SELECT source, base_asset, quote_asset, last_trade_at, count_24h, vol_24h_usd, last_price
		   FROM pools
		  WHERE $2 = ''
		     OR COALESCE(vol_24h_usd::numeric, 0)
		          <  CAST(NULLIF(split_part($2, ':', 1), '') AS numeric)
		     OR (
		          COALESCE(vol_24h_usd::numeric, 0)
		          =  CAST(COALESCE(NULLIF(split_part($2, ':', 1), ''), '0') AS numeric)
		          AND (source || '|' || base_asset || '|' || quote_asset)
		              > substring($2 from position(':' in $2) + 1)
		        )
		  ORDER BY COALESCE(vol_24h_usd::numeric, 0) DESC,
		           (source || '|' || base_asset || '|' || quote_asset) ASC
		  LIMIT $3
		`
		args := []any{since, cursor, limit + 1, pq.Array(filter.Sources), filter.Base, filter.Quote, filter.Asset}
		return cte + tail, args
	}
	const tail = `
	 SELECT source, base_asset, quote_asset, last_trade_at, count_24h, vol_24h_usd, last_price
	   FROM pools
	  WHERE ($2 = '' OR (source || '|' || base_asset || '|' || quote_asset) > $2)
	  ORDER BY (source || '|' || base_asset || '|' || quote_asset) ASC
	  LIMIT $3
	`
	// 7 args matching the $1..$7 placeholders. The asset arg was
	// missing pre-fix, causing `pq: got 6 parameters but the
	// statement requires 7` on every order_by=pair request — caught
	// 2026-05-14 live on r1.
	args := []any{since, cursor, limit + 1, pq.Array(filter.Sources), filter.Base, filter.Quote, filter.Asset}
	return cte + tail, args
}

func (s *Store) distinctPairsCommon(ctx context.Context, source, asset, cursor string, limit int, order MarketsOrder) ([]Market, string, error) {
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
	q, args := buildDistinctPairsQuery(since, source, asset, cursor, limit, order)
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
			bucketCloseAt     time.Time
			count24h          int64
			vol24hUSD         sql.NullString
			lastPrice         sql.NullString
		)
		if err := rows.Scan(&baseRaw, &quoteRaw, &lastAt, &bucketCloseAt, &count24h, &vol24hUSD, &lastPrice); err != nil {
			return nil, false, fmt.Errorf("timescale: DistinctPairs scan: %w", err)
		}
		n++
		if n > limit {
			hasMore = true
			break
		}
		m, err := buildMarketRow(baseRaw, quoteRaw, lastAt, bucketCloseAt, count24h, vol24hUSD)
		if err != nil {
			// Skip rows we can't parse rather than failing the whole
			// response. A single malformed trades row (e.g. a manual
			// insert with a non-canonical asset code) used to 500 the
			// entire /v1/markets surface and trip page-tier alerts
			// (2026-06-01 incident). The ingest pipeline only emits
			// canonical asset strings, so reaching this branch means
			// something bypassed the normal write path; surface it as
			// a warning + counter so operators can find and remove it
			// rather than serving a 500 to every consumer.
			obs.MarketsSkippedRowsTotal.Inc()
			slog.Default().Warn("markets: skipping unparseable trades row",
				"base", baseRaw, "quote", quoteRaw, "err", err)
			continue
		}
		if lastPrice.Valid && lastPrice.String != "" {
			v := lastPrice.String
			m.LastPrice = &v
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("timescale: DistinctPairs rows: %w", err)
	}
	return out, hasMore, nil
}

func buildMarketRow(baseRaw, quoteRaw string, lastAt, bucketCloseAt time.Time, count24h int64, vol24hUSD sql.NullString) (Market, error) {
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
		BucketCloseAt: bucketCloseAt.UTC(),
		TradeCount24h: count24h,
	}
	if vol24hUSD.Valid && vol24hUSD.String != "" && vol24hUSD.String != "0" {
		v := vol24hUSD.String
		m.Volume24hUSD = &v
	}
	return m, nil
}

// ValidateMarketsCursor returns an error if `cursor` is non-empty
// but doesn't match the encoded shape that encodeMarketsCursor
// emits for the active order. Empty cursor is always valid (start
// from the first page). Callers should reject invalid cursors at
// the handler boundary with a 400.
//
// Without this guard, a hand-crafted cursor (or a stale link from
// before a pagination format change) silently degrades:
//
//   - MarketsOrderPair: the SQL predicate
//     `(base || '|' || quote) > $cursor` falls through to a
//     lexicographic skip — collation-dependent and almost never
//     what the caller wants.
//   - MarketsOrderVolume24hDesc: the predicate casts
//     `split_part($cursor, ':', 1)::numeric`, which raises a
//     Postgres "invalid input syntax for type numeric" error and
//     a 500. Burns CPU per request.
func ValidateMarketsCursor(cursor string, order MarketsOrder) error {
	if cursor == "" {
		return nil
	}
	pairPart := cursor
	if order == MarketsOrderVolume24hDesc {
		idx := strings.IndexByte(cursor, ':')
		if idx < 0 {
			return fmt.Errorf("missing ':' separator")
		}
		volPart := cursor[:idx]
		// Volume prefix may be empty (last row had a null vol_usd).
		// Otherwise: digits with at most one '.', no leading sign.
		if volPart != "" {
			dot := false
			for j := 0; j < len(volPart); j++ {
				c := volPart[j]
				switch {
				case c >= '0' && c <= '9':
				case c == '.' && !dot:
					dot = true
				default:
					return fmt.Errorf("non-numeric volume prefix")
				}
			}
		}
		pairPart = cursor[idx+1:]
		if pairPart == "" {
			return fmt.Errorf("missing pair suffix")
		}
	}
	pipe := strings.IndexByte(pairPart, '|')
	if pipe < 0 {
		return fmt.Errorf("missing '|' separator in pair")
	}
	if pipe == 0 || pipe == len(pairPart)-1 {
		return fmt.Errorf("missing base or quote in pair")
	}
	return nil
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
func buildDistinctPairsQuery(since time.Time, source, asset, cursor string, limit int, order MarketsOrder) (string, []any) {
	// /v1/markets is a DIRECTORY query ("which pairs are active +
	// their 24h volume / last price"), not a history view. Sourcing
	// it from prices_1m × 14 days was O(~52k pairs × 20,160 1-min
	// buckets); post all-time backfill prices_1m ballooned and the
	// aggregate seq-scans multi-million-row materialized chunks →
	// ~8s+, blowing BOTH the 8s handler ceiling and the prewarm
	// budget, so the cache never warmed and /v1/markets 503'd for
	// real users (#20). The earlier "prices_1m alone, ~0.46s"
	// measurement (2026-05-11) no longer holds at this data scale.
	//
	// Right-granularity rewrite. NO data or precision loss anywhere
	// data is consumed at resolution — prices_1m and every detail
	// endpoint (/history, /ohlc, /chart, /vwap, /twap) are
	// untouched; this changes only which CAGG this one *listing*
	// query reads:
	//   - active-pair set + last_trade_at + last_price (latest
	//     within the 14d window) ← prices_1d   (~14 buckets/pair)
	//   - 24h trade_count + volume_usd          ← prices_1m
	//     RESTRICTED to the trailing 24h. Exact + fresh. A rolling
	//     (non-hour-aligned) 24h window is NOT bucket-additive over
	//     a coarser CAGG: measured on r1, prices_1h understated the
	//     all-pairs 24h volume ~9% vs prices_1m ($3.60B vs $3.97B —
	//     boundary mismatch + prices_1h refresh-lag near the tip).
	//     The #20 perf problem was ONLY the 14d × ~52k-pair
	//     enumeration (now on prices_1d); a 24h prices_1m sum is
	//     chunk-pruned to the last day's chunks → ~160ms all-pairs
	//     on r1 — fast AND exact, so the user-facing 24h figure
	//     stays prices_1m-accurate. (Corrects an earlier prices_1h
	//     variant that shipped a ~9% headline-volume understatement
	//     under a false "Σ-associative → identical" claim.)
	// bucket_close_at rounds to the day (from prices_1d) — immaterial
	// for a directory; the exact ts/price is on the detail
	// endpoints. count_24h is COALESCE'd to 0 for
	// 14d-active-but-24h-idle pairs (more robust than the prior
	// FILTER-SUM, which yielded NULL for that case).
	//
	// last_trade_at is sourced from the SAME 24h prices_1m scan as
	// the volume aggregate (zero added cost) — MAX(bucket) gives
	// minute-precision for in-24h-active pairs. For pairs idle >24h
	// (rare under volume-desc default ordering) it falls back to
	// the daily bucket-start. F-0065 fix (2026-05-27): pre-fix the
	// /v1/markets `last_trade_at` field was the daily bucket-start
	// for ALL pairs, so most rows surfaced midnight UTC and clients
	// computing staleness against `now()` saw spuriously-large
	// values.
	//
	// $1 since(14d) bounds the prices_1d set; $4 source / $5 asset
	// filter BOTH CTEs (empty short-circuits → planner skips); the
	// keyset-cursor ($2) + LIMIT $3 overfetch-by-one shape is
	// byte-for-byte the prior pagination contract.
	const ctes = `
        WITH d AS (
            SELECT p.base_asset, p.quote_asset,
                   MAX(p.bucket) AS bucket_close_at,
                   (array_agg(p.last_price ORDER BY p.bucket DESC)
                      FILTER (WHERE p.last_price IS NOT NULL))[1]::text AS last_price
              FROM prices_1d p
             WHERE p.bucket >= $1
               AND ($4 = '' OR $4 = ANY(p.sources))
               AND ($5 = '' OR p.base_asset = $5 OR p.quote_asset = $5)
             GROUP BY p.base_asset, p.quote_asset
        ),
        h AS (
            SELECT p.base_asset, p.quote_asset,
                   MAX(p.bucket)       AS last_bucket_1m,
                   SUM(p.trade_count)  AS count_24h,
                   SUM(p.volume_usd)   AS vol_24h_num
              FROM prices_1m p
             WHERE p.bucket > NOW() - INTERVAL '24 hours'
               AND ($4 = '' OR $4 = ANY(p.sources))
               AND ($5 = '' OR p.base_asset = $5 OR p.quote_asset = $5)
             GROUP BY p.base_asset, p.quote_asset
        )
        SELECT d.base_asset, d.quote_asset,
               COALESCE(h.last_bucket_1m, d.bucket_close_at) AS last_trade_at,
               d.bucket_close_at,
               COALESCE(h.count_24h, 0)        AS count_24h,
               NULLIF(h.vol_24h_num, 0)::text  AS vol_24h_usd,
               d.last_price
          FROM d
          LEFT JOIN h
            ON h.base_asset = d.base_asset
           AND h.quote_asset = d.quote_asset
    `
	switch order {
	case MarketsOrderVolume24hDesc:
		// Cursor: "<vol_or_blank>:<base>|<quote>". Two-tuple keyset
		// against (vol_24h, base|quote). COALESCE NULL→0 so a
		// 24h-idle pair sorts last under DESC (same effect as
		// NULLS LAST; real volumes are positive). The "next page"
		// relation is (v < cv) OR (v = cv AND pair > cpair) — DESC
		// on the first key flips the comparator, encoded explicitly
		// (identical to the prior HAVING form, now a WHERE since d/h
		// are already grouped).
		const tail = `
         WHERE $2 = ''
            OR COALESCE(h.vol_24h_num, 0)
                 <  CAST(NULLIF(split_part($2, ':', 1), '') AS numeric)
            OR (
                 COALESCE(h.vol_24h_num, 0)
                 =  CAST(COALESCE(NULLIF(split_part($2, ':', 1), ''), '0') AS numeric)
                 AND (d.base_asset || '|' || d.quote_asset)
                     > substring($2 from position(':' in $2) + 1)
               )
         ORDER BY COALESCE(h.vol_24h_num, 0) DESC,
                  (d.base_asset || '|' || d.quote_asset) ASC
         LIMIT $3
        `
		return ctes + tail, []any{since, cursor, limit + 1, source, asset}
	default: // MarketsOrderPair
		const tail = `
         WHERE ($2 = '' OR (d.base_asset || '|' || d.quote_asset) > $2)
         ORDER BY (d.base_asset || '|' || d.quote_asset) ASC
         LIMIT $3
        `
		return ctes + tail, []any{since, cursor, limit + 1, source, asset}
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
	// PairMarket already sources LastTradeAt from MAX(trades.ts) —
	// exact (second-precision) since it scans the trades hypertable
	// directly for one pair. BucketCloseAt is recomputed from the
	// same value via time_bucket('1 day', …) so the wire shape
	// matches /v1/markets (which sources it from prices_1d).
	const q = `
        SELECT MAX(t.ts) AS last_trade_at,
               count(*) FILTER (WHERE t.ts > NOW() - INTERVAL '24 hours') AS count_24h,
               (SELECT SUM(volume_usd)::text FROM prices_1m
                 WHERE base_asset = $2 AND quote_asset = $3
                   AND bucket >= NOW() - INTERVAL '24 hours'
                   AND volume_usd IS NOT NULL) AS vol_24h_usd,
               (SELECT last_price::text FROM prices_1m
                 WHERE base_asset = $2 AND quote_asset = $3
                   AND bucket >= NOW() - INTERVAL '24 hours'
                   AND last_price IS NOT NULL
                 ORDER BY bucket DESC LIMIT 1) AS last_price
          FROM trades t
         WHERE t.ts >= $1
           AND t.base_asset = $2 AND t.quote_asset = $3
    `
	var (
		lastAt    *time.Time
		count24h  *int64
		vol24hUSD sql.NullString
		lastPx    sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, q, since, base.String(), quote.String()).Scan(&lastAt, &count24h, &vol24hUSD, &lastPx); err != nil {
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
	lastAtUTC := lastAt.UTC()
	bucketClose := time.Date(lastAtUTC.Year(), lastAtUTC.Month(), lastAtUTC.Day(), 0, 0, 0, 0, time.UTC)
	m := Market{
		Pair:          pair,
		LastTradeAt:   lastAtUTC,
		BucketCloseAt: bucketClose,
		TradeCount24h: n,
	}
	if vol24hUSD.Valid && vol24hUSD.String != "" && vol24hUSD.String != "0" {
		v := vol24hUSD.String
		m.Volume24hUSD = &v
	}
	if lastPx.Valid && lastPx.String != "" {
		v := lastPx.String
		m.LastPrice = &v
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
