package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// MarketsReader is the storage-side interface for /v1/markets
// and /v1/pairs lookups. Implementations: *timescale.Store
// (DistinctPairs + PairMarket), in-memory stubs for tests.
type MarketsReader interface {
	// DistinctPairs returns one page of (base, quote) pairs present
	// in the trades store, each annotated with a recency + activity
	// stat. Cursor opaque; empty starts at page 1.
	DistinctPairs(ctx context.Context, cursor string, limit int) ([]Market, string, error)

	// PairMarket returns the activity summary for a single (base,
	// quote) pair. The bool is false when the pair has no trades —
	// the /v1/pairs handler translates that to an empty 200 OK array,
	// not a 404, so the wire shape stays consistent with the
	// PairsEnvelope contract.
	PairMarket(ctx context.Context, base, quote canonical.Asset) (Market, bool, error)
}

// Market is the wire shape for /v1/markets entries.
//
// TradeCount24h may be zero even when LastTradeAt is recent — they
// measure different windows (activity vs most-recent event). The
// fields are designed to let clients sort markets by "current"
// activity vs total history.
//
// Volume24hUSD is the trailing-24h USD volume summed from
// prices_1m's per-bucket volume_usd. Pointer + omitempty so a
// pair with no USD-equivalent trades emits null instead of "0"
// — important for client-side sorting (treat null as "unknown",
// 0 as "definitely zero").
type Market struct {
	Base          string    `json:"base"`
	Quote         string    `json:"quote"`
	LastTradeAt   time.Time `json:"last_trade_at"`
	TradeCount24h int64     `json:"trade_count_24h"`
	Volume24hUSD  *string   `json:"volume_24h_usd,omitempty"`
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
	// Defensive nil-to-empty: OpenAPI's MarketsEnvelope.data is
	// `type: array`, which means "data": null violates the schema.
	// Mirrors the handleAssetList guard.
	if rows == nil {
		rows = []Market{}
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
