package v1

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CoinsReader is the seam the /v1/coins handlers read through.
// timescale.Store satisfies it via ListCoinsExt + GetCoinBySlug
// + GetNativeCoinRow + GetCoinTopMarkets + GetCoinPriceHistory24h
// + GetCoinMarketsCount.
type CoinsReader interface {
	ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error)
	GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error)
	GetNativeCoinRow(ctx context.Context) (timescale.CoinRow, error)
	GetCoinTopMarkets(ctx context.Context, assetID string, limit int) ([]timescale.CoinTopMarket, error)
	GetCoinPriceHistory24h(ctx context.Context, assetID string) ([]timescale.CoinPricePoint, error)
	GetCoinMarketsCount(ctx context.Context, assetID string) (int64, error)
}

// Coin is the wire shape of one entry in the /v1/coins response.
//
// PriceUSD / Volume24hUSD / MarketCapUSD / CirculatingSupply are
// nullable strings — emitted as `null` in JSON when the
// aggregator hasn't yet produced a value (newly-observed asset,
// no off-chain peg, etc.). All numeric fields are strings to
// preserve precision per ADR-0003.
type Coin struct {
	Slug              string  `json:"slug"`
	AssetID           string  `json:"asset_id"`
	Code              string  `json:"code"`
	Issuer            string  `json:"issuer"`
	FirstSeenLedger   uint32  `json:"first_seen_ledger"`
	LastSeenLedger    uint32  `json:"last_seen_ledger"`
	ObservationCount  int64   `json:"observation_count"`
	PriceUSD          *string `json:"price_usd,omitempty"`
	Volume24hUSD      *string `json:"volume_24h_usd,omitempty"`
	MarketCapUSD      *string `json:"market_cap_usd,omitempty"`
	CirculatingSupply *string `json:"circulating_supply,omitempty"`
	// Change1hPct / Change24hPct / Change7dPct are the trailing
	// price changes for those windows, signed percentages with
	// two fractional digits (e.g. "+1.27", "-0.05", "0.00"). Null
	// when the asset has no current price or when no past-bucket
	// snapshot exists in prices_1m within the window-specific
	// tolerance (±5min for 1h, ±30min for 24h, ±2h for 7d).
	Change1hPct  *string `json:"change_1h_pct,omitempty"`
	Change24hPct *string `json:"change_24h_pct,omitempty"`
	Change7dPct  *string `json:"change_7d_pct,omitempty"`
	// TopMarkets is a preview of the asset's top markets by 24h
	// USD volume. Populated only on /v1/coins/{slug} (the
	// listing endpoint omits it to keep payload sizes manageable).
	TopMarkets []CoinTopMarket `json:"top_markets,omitempty"`
	// PriceHistory24h is 24 hourly USD-price samples (oldest
	// first) covering the trailing 24h. Populated only on
	// /v1/coins/{slug}. Each entry: {t: RFC3339 bucket end,
	// p: rounded-to-10dp USD price or null when no trades that
	// hour}. Powers asset detail sparkline + chart preview.
	PriceHistory24h []CoinPricePoint `json:"price_history_24h,omitempty"`
	// MarketsCount is the count of distinct (base, quote) pairs
	// the asset participated in over the trailing 24h. Populated
	// only on /v1/coins/{slug}. Pointer so 0 (asset went silent
	// in the last 24h) is distinguishable from "not computed"
	// (lookup error) — both render as "—" in the UI but behave
	// differently in alerting.
	MarketsCount *int64 `json:"markets_count,omitempty"`
}

// CoinTopMarket is one entry in the per-asset top-markets preview.
// `Counterparty` is the OTHER side of the pair (the asset that's
// not the one being queried). `Side` is "base" or "quote"
// depending on which side the queried asset took.
type CoinTopMarket struct {
	Counterparty  string  `json:"counterparty"`
	Side          string  `json:"side"`
	Volume24hUSD  *string `json:"volume_24h_usd,omitempty"`
	TradeCount24h int64   `json:"trade_count_24h"`
}

// CoinPricePoint is one hourly USD-price sample in
// `Coin.PriceHistory24h`. `T` is the bucket end as an RFC3339
// timestamp; `P` is the rounded-to-10dp USD price or nil when no
// trades that hour produced a VWAP.
type CoinPricePoint struct {
	T string  `json:"t"`
	P *string `json:"p,omitempty"`
}

// CoinsPage wraps the rows + cursor pagination metadata. The
// `next_cursor` is empty when the last page has been returned;
// clients iterate while it's non-empty.
type CoinsPage struct {
	Coins      []Coin `json:"coins"`
	NextCursor string `json:"next_cursor,omitempty"`
	Limit      int    `json:"limit"`
}

// handleCoins serves GET /v1/coins.
//
// Query parameters:
//
//	limit   1..500 (default 100). Number of rows per page.
//	cursor  opaque keyset cursor; pass back the value from
//	        `next_cursor` in the previous response to fetch
//	        the next page. Empty for the first page.
//	issuer  optional G-strkey filter.
//
// Response shape:
//
//	{
//	  "data": {
//	    "coins": [...],
//	    "next_cursor": "<opaque>",   // empty when no more pages
//	    "limit": 100
//	  },
//	  ...envelope...
//	}
func (s *Server) handleCoins(w http.ResponseWriter, r *http.Request) {
	if s.coins == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-unavailable",
			"Coins listing unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the coins reader yet.")
		return
	}

	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be 1-500")
			return
		}
		limit = n
	}

	issuer := r.URL.Query().Get("issuer")
	cursor := r.URL.Query().Get("cursor")
	q := r.URL.Query().Get("q")
	if len(q) > 64 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-query",
			"Invalid q", http.StatusBadRequest,
			"q must be 64 chars or fewer")
		return
	}
	var order timescale.CoinsOrder
	switch r.URL.Query().Get("order_by") {
	case "", "observation_count_desc":
		order = timescale.CoinsOrderObservationCountDesc
	case "volume_24h_usd_desc":
		order = timescale.CoinsOrderVolume24hUSDDesc
	default:
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-order",
			"Invalid order_by", http.StatusBadRequest,
			"order_by must be 'observation_count_desc' or 'volume_24h_usd_desc'")
		return
	}

	// Native XLM has no classic_assets row but is the most-active
	// asset on the network — prepend it at the top of the first
	// unfiltered page so /v1/coins (and the explorer's home page
	// that consumes it) never silently omits XLM. We fetch
	// `limit-1` rows from the listing in this mode so the page
	// size stays exactly `limit`. When any filter is active the
	// user has narrowed the listing on purpose, so don't inject.
	prependNative := cursor == "" && issuer == "" && q == "" && limit >= 2
	listingLimit := limit
	if prependNative {
		listingLimit = limit - 1
	}

	rows, err := s.coins.ListCoinsExt(r.Context(), timescale.ListCoinsOptions{
		Limit: listingLimit, Issuer: issuer, Cursor: cursor, Q: q, Order: order,
	})
	if err != nil {
		s.logger.Warn("coins list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coins listing failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	// Compute next-cursor from the listing's last real row BEFORE
	// we prepend native — page 2 must resume past the listing
	// tail, never past the synthetic XLM row.
	nextCursor := nextCoinCursor(rows, listingLimit, order)

	out := make([]Coin, 0, limit)
	if prependNative {
		if nativeRow, nErr := s.coins.GetNativeCoinRow(r.Context()); nErr != nil {
			s.logger.Warn("coins list: native row lookup failed; emitting without it",
				"err", nErr)
		} else if nativeRow.AssetID != "" {
			out = append(out, coinFromRow(nativeRow))
		}
	}
	for _, row := range rows {
		out = append(out, coinFromRow(row))
	}

	writeJSON(w, CoinsPage{
		Coins:      out,
		NextCursor: nextCursor,
		Limit:      limit,
	}, Flags{})
}

// handleCoin serves GET /v1/coins/{slug}.
//
// Returns one coin row by slug. The slug is the URL-safe
// identifier from `classic_assets.slug` (or `classic_assets.code`
// when slug is null). Used by the explorer asset-detail page
// (/assets/{slug}) so a deep link doesn't require scanning the
// top-N listing first.
//
// Returns 404 when the slug doesn't match any classic asset.
func (s *Server) handleCoin(w http.ResponseWriter, r *http.Request) {
	if s.coins == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-unavailable",
			"Coins lookup unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the coins reader yet.")
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-slug",
			"Invalid slug", http.StatusBadRequest,
			"slug path parameter is required")
		return
	}

	// Native XLM has no classic_assets row (that table only tracks
	// issued classic assets). Without this intercept the slug "XLM"
	// matches whichever issued token's code happens to be "XLM"
	// wins the disambiguation tiebreak, and "native" 404s outright.
	// The synthetic row is built from the same xlm_usd CTEs that
	// drive triangulated pricing for every other asset.
	var (
		row timescale.CoinRow
		err error
	)
	if slug == "XLM" || slug == "native" {
		row, err = s.coins.GetNativeCoinRow(r.Context())
	} else {
		row, err = s.coins.GetCoinBySlug(r.Context(), slug)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coin-not-found",
			"Coin not found", http.StatusNotFound,
			"No classic asset matches that slug.")
		return
	}
	if err != nil {
		s.logger.Warn("coin by slug", "slug", slug, "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coin lookup failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	out := coinFromRow(row)

	// Populate top_markets + price_history_24h — both soft-fail;
	// the asset detail card still renders without the previews if
	// either lookup errors. A failure here usually means the
	// prices_1m chunk the query needs is being column-store-
	// compressed.
	if topMarkets, mErr := s.coins.GetCoinTopMarkets(r.Context(), row.AssetID, 5); mErr != nil {
		s.logger.Warn("coin top markets", "asset_id", row.AssetID, "err", mErr)
	} else {
		out.TopMarkets = make([]CoinTopMarket, len(topMarkets))
		for i, m := range topMarkets {
			out.TopMarkets[i] = CoinTopMarket{
				Counterparty:  m.Counterparty,
				Side:          m.Side,
				Volume24hUSD:  m.Volume24hUSD,
				TradeCount24h: m.TradeCount24h,
			}
		}
	}
	if history, hErr := s.coins.GetCoinPriceHistory24h(r.Context(), row.AssetID); hErr != nil {
		s.logger.Warn("coin price history", "asset_id", row.AssetID, "err", hErr)
	} else {
		out.PriceHistory24h = make([]CoinPricePoint, len(history))
		for i, p := range history {
			out.PriceHistory24h[i] = CoinPricePoint{T: p.T, P: p.P}
		}
	}
	if n, cErr := s.coins.GetCoinMarketsCount(r.Context(), row.AssetID); cErr != nil {
		s.logger.Warn("coin markets count", "asset_id", row.AssetID, "err", cErr)
	} else {
		out.MarketsCount = &n
	}

	writeJSON(w, out, Flags{})
}

// nextCoinCursor builds the opaque keyset cursor for the next
// page or returns "" when the just-emitted page is the last one.
// Pulled out of the handler so handleCoins stays under the
// gocognit threshold; the cursor format itself is unchanged.
func nextCoinCursor(rows []timescale.CoinRow, pageSize int, order timescale.CoinsOrder) string {
	if len(rows) != pageSize || len(rows) == 0 {
		return ""
	}
	last := rows[len(rows)-1]
	if order == timescale.CoinsOrderVolume24hUSDDesc {
		vol := ""
		if last.Volume24hUSD != nil {
			vol = *last.Volume24hUSD
		}
		return vol + ":" + last.AssetID
	}
	return timescale.EncodeCoinCursor(last.ObservationCount, last.AssetID)
}

// coinFromRow projects a timescale.CoinRow onto the wire Coin
// struct. Pulled out of the two handlers so they stay under the
// funlen threshold and any future field is added in one spot.
func coinFromRow(row timescale.CoinRow) Coin {
	return Coin{
		Slug:              row.Slug,
		AssetID:           row.AssetID,
		Code:              row.Code,
		Issuer:            row.IssuerGStrkey,
		FirstSeenLedger:   row.FirstSeenLedger,
		LastSeenLedger:    row.LastSeenLedger,
		ObservationCount:  row.ObservationCount,
		PriceUSD:          row.PriceUSD,
		Volume24hUSD:      row.Volume24hUSD,
		MarketCapUSD:      row.MarketCapUSD,
		CirculatingSupply: row.CirculatingSupply,
		Change1hPct:       row.Change1hPct,
		Change24hPct:      row.Change24hPct,
		Change7dPct:       row.Change7dPct,
	}
}
