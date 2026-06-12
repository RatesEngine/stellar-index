package v1

import (
	"net/http"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// handlePairs serves GET /v1/pairs?base=<id>&quote=<id>.
//
// Returns a [Market] array containing zero or one entries:
// the activity summary for the requested pair if any trade has
// been seen, otherwise an empty array. The 0-or-1-element array
// shape matches the OpenAPI PairsEnvelope (data: array of
// MarketRow) — a missing pair is NOT a 404, it's an empty list,
// so clients can distinguish "no such pair" from a malformed
// request without branching on status code.
//
// Reuses the [MarketsReader] interface — /v1/pairs and
// /v1/markets share the same storage shape, just with different
// access patterns (full pageable scan vs single-pair lookup).
func (s *Server) handlePairs(w http.ResponseWriter, r *http.Request) {
	rawBase := r.URL.Query().Get("base")
	if rawBase == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-base",
			"Missing base parameter", http.StatusBadRequest,
			"base query parameter is required")
		return
	}
	base, err := canonical.ParseAsset(rawBase)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid base identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	rawQuote := r.URL.Query().Get("quote")
	if rawQuote == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-quote",
			"Missing quote parameter", http.StatusBadRequest,
			"quote query parameter is required")
		return
	}
	quote, err := canonical.ParseAsset(rawQuote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	if base.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-pair",
			"Base and quote are the same", http.StatusBadRequest,
			"a pair must have distinct base and quote assets")
		return
	}

	reader := s.markets
	if reader == nil {
		// Mirror /v1/markets's degradation: empty list instead of 503
		// so clients can integrate against the wire contract before a
		// reader is wired.
		writeJSON(w, []Market{}, Flags{})
		return
	}

	market, found, err := reader.PairMarket(r.Context(), base, quote)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("PairMarket failed",
			"err", err,
			"base", base.String(),
			"quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	out := []Market{}
	if found {
		out = append(out, market)
	}
	writeJSON(w, out, Flags{})
}
