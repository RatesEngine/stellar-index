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
// timescale.Store satisfies it via ListCoinsExt + GetCoinBySlug.
type CoinsReader interface {
	ListCoinsExt(ctx context.Context, opts timescale.ListCoinsOptions) ([]timescale.CoinRow, error)
	GetCoinBySlug(ctx context.Context, slug string) (timescale.CoinRow, error)
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
	// Change24hPct is the trailing-24h price change as a signed
	// percentage with two fractional digits (e.g. "+1.27",
	// "-0.05", "0.00"). Null when no current price exists or
	// when the 24h-ago bucket is missing in prices_1m.
	Change24hPct *string `json:"change_24h_pct,omitempty"`
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

	rows, err := s.coins.ListCoinsExt(r.Context(), timescale.ListCoinsOptions{
		Limit: limit, Issuer: issuer, Cursor: cursor, Q: q,
	})
	if err != nil {
		s.logger.Warn("coins list", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/coins-error",
			"Coins listing failed", http.StatusInternalServerError,
			"Storage layer returned an error.")
		return
	}

	out := make([]Coin, len(rows))
	for i, row := range rows {
		out[i] = coinFromRow(row)
	}

	// Compute next-cursor only when the page came back full —
	// any short page means "no more rows". Encodes the last
	// row's (observation_count, asset_id) tuple.
	var nextCursor string
	if len(rows) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		nextCursor = timescale.EncodeCoinCursor(last.ObservationCount, last.AssetID)
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

	row, err := s.coins.GetCoinBySlug(r.Context(), slug)
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

	writeJSON(w, coinFromRow(row), Flags{})
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
		Change24hPct:      row.Change24hPct,
	}
}
