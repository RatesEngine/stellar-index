package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// CoinsReader is the seam the /v1/coins handler reads through.
// timescale.Store satisfies it via ListCoins.
type CoinsReader interface {
	ListCoins(ctx context.Context, limit int, issuer, cursor string) ([]timescale.CoinRow, error)
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

	rows, err := s.coins.ListCoins(r.Context(), limit, issuer, cursor)
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
		out[i] = Coin{
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
		}
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
