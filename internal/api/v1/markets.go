package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// MarketsReader is the storage-side interface for /v1/markets
// lookups. Implementations: *timescale.Store (DistinctPairs),
// in-memory stubs for tests.
type MarketsReader interface {
	// DistinctPairs returns one page of (base, quote) pairs present
	// in the trades store, each annotated with a recency + activity
	// stat. Cursor opaque; empty starts at page 1.
	DistinctPairs(ctx context.Context, cursor string, limit int) ([]Market, string, error)
}

// Market is the wire shape for /v1/markets entries.
//
// TradeCount24h may be zero even when LastTradeAt is recent — they
// measure different windows (activity vs most-recent event). The
// fields are designed to let clients sort markets by "current"
// activity vs total history.
type Market struct {
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
}

// handleMarkets serves GET /v1/markets.
//
// Query params:
//   - cursor (optional): opaque, from a prior response's pagination.next.
//   - limit  (optional): integer 1-500, default 100.
func (s *Server) handleMarkets(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("cursor")
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 500]")
			return
		}
		limit = parsed
	}

	reader := s.markets
	if reader == nil {
		// Feature not wired — empty list is consistent with the
		// contract and doesn't force a 503. Mirrors the /v1/assets
		// degradation pattern.
		writeJSON(w, []Market{}, Flags{})
		return
	}

	rows, next, err := reader.DistinctPairs(r.Context(), cursor, limit)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("DistinctPairs failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	env := Envelope{
		Data:  rows,
		Flags: Flags{},
	}
	if next != "" {
		env.Pagination = &Pagination{Next: next}
	}
	writeEnvelope(w, env)
}
