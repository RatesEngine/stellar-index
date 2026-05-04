package timescale

import (
	"context"
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Market is one distinct (base, quote) pair summary with activity
// statistics. Returned by [Store.DistinctPairs].
type Market struct {
	Pair          canonical.Pair
	LastTradeAt   time.Time
	TradeCount24h int64
}

// MarketsRecencyWindow bounds the trades scanned by DistinctPairs.
// Pairs that haven't traded inside the window are excluded from the
// `/v1/markets` listing — the public contract is "active markets",
// not "every pair ever observed". The window is exposed as a var so
// tests can override it without changing the public function signature.
//
// Empirical sizing on r1 (441M trades, 1100+ chunks):
//   - 14 days: ~540 ms cold, ~50 ms warm — the chosen default
//   - 30 days: ~9 s with JIT, ~3 s without — too slow for a hot path
//   - 90 days: ~16-19 s — exceeded the 30s client deadline
//
// 14 days passes the "active markets" intuition (a market that
// hasn't traded in two weeks isn't really active) and the
// COMPRESSED chunks at the boundary fit in postgres's shared
// buffers. A future materialised market_catalogue would let us
// drop the recency bound entirely.
var MarketsRecencyWindow = 14 * 24 * time.Hour

// DistinctPairs returns one page of recently-active (base, quote)
// pairs from the trades hypertable, each with its most-recent trade
// timestamp and a 24h trade count. Cursor-based pagination keyed on
// the pair's canonical "<base>|<quote>" string; empty cursor starts
// from the beginning.
//
// limit clamps to [1, 500] — matching DistinctAssets for consistency.
//
// Recency window: the query scans only chunks within the last
// MarketsRecencyWindow (default 90d) so chunk pruning bounds I/O on
// a hypertable with hundreds of millions of trades. Pairs that
// haven't traded in that window do not appear. This matches the
// public contract — "/v1/markets" lists active markets, not every
// pair ever observed; an asset that hasn't traded in 90 days isn't
// usefully described as having a market.
//
// Future direction: a materialised market_catalogue populated by the
// indexer would let us drop the recency bound entirely (see the
// long-standing DistinctAssets perf note — same future migration).
func (s *Store) DistinctPairs(ctx context.Context, cursor string, limit int) ([]Market, string, error) {
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
	const q = `
        SELECT base_asset, quote_asset,
               MAX(ts) AS last_trade_at,
               count(*) FILTER (WHERE ts > NOW() - INTERVAL '24 hours') AS count_24h
          FROM trades
         WHERE ts >= $1
           AND ($2 = '' OR (base_asset || '|' || quote_asset) > $2)
         GROUP BY base_asset, quote_asset
         ORDER BY (base_asset || '|' || quote_asset) ASC
         LIMIT $3
    `
	rows, err := s.db.QueryContext(ctx, q, since, cursor, limit+1)
	if err != nil {
		return nil, "", fmt.Errorf("timescale: DistinctPairs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Market, 0, limit)
	n := 0
	hasMore := false
	for rows.Next() {
		var (
			baseRaw, quoteRaw string
			lastAt            time.Time
			count24h          int64
		)
		if err := rows.Scan(&baseRaw, &quoteRaw, &lastAt, &count24h); err != nil {
			return nil, "", fmt.Errorf("timescale: DistinctPairs scan: %w", err)
		}
		n++
		if n > limit {
			hasMore = true
			break
		}
		base, err := canonical.ParseAsset(baseRaw)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: DistinctPairs base %q: %w", baseRaw, err)
		}
		quote, err := canonical.ParseAsset(quoteRaw)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: DistinctPairs quote %q: %w", quoteRaw, err)
		}
		pair, err := canonical.NewPair(base, quote)
		if err != nil {
			return nil, "", fmt.Errorf("timescale: DistinctPairs pair: %w", err)
		}
		out = append(out, Market{
			Pair:          pair,
			LastTradeAt:   lastAt.UTC(),
			TradeCount24h: count24h,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("timescale: DistinctPairs rows: %w", err)
	}

	nextCursor := ""
	if hasMore && len(out) > 0 {
		// Cursor points at the LAST row IN the returned page — next
		// call resumes at "strictly greater than this pair".
		last := out[len(out)-1]
		nextCursor = last.Pair.Base.String() + "|" + last.Pair.Quote.String()
	}
	return out, nextCursor, nil
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
        SELECT MAX(ts) AS last_trade_at,
               count(*) FILTER (WHERE ts > NOW() - INTERVAL '24 hours') AS count_24h
          FROM trades
         WHERE ts >= $1
           AND base_asset = $2 AND quote_asset = $3
    `
	var (
		lastAt   *time.Time
		count24h *int64
	)
	if err := s.db.QueryRowContext(ctx, q, since, base.String(), quote.String()).Scan(&lastAt, &count24h); err != nil {
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
	return Market{
		Pair:          pair,
		LastTradeAt:   lastAt.UTC(),
		TradeCount24h: n,
	}, true, nil
}
