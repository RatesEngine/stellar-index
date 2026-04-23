package v1

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// HistoryReader is the storage-side interface for /v1/history
// lookups.
type HistoryReader interface {
	// TradesInRange returns trades for pair whose close-time is in
	// [from, to). Ordered chronologically (ts ASC). Used by the
	// aggregation endpoints (/v1/vwap, /v1/twap, /v1/ohlc) which
	// consume the whole window at once.
	TradesInRange(ctx context.Context, pair canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error)

	// TradesInRangeAfter is TradesInRange with a `(ts, ledger)`
	// cursor. Rows are filtered to (ts, ledger) > (afterTs,
	// afterLedger). afterTs = zero time disables the cursor.
	// Used by /v1/history's cursor pagination.
	TradesInRangeAfter(ctx context.Context, pair canonical.Pair, from, to, afterTs time.Time, afterLedger uint32, limit int) ([]canonical.Trade, error)
}

// TradeRow is the wire shape for /v1/history entries.
//
// Numeric amounts ship as decimal strings (ADR-0003). Price is a
// pre-computed decimal for consumer convenience — the storage layer
// never persists a derived price, so we compute at response time.
type TradeRow struct {
	Source      string    `json:"source"`
	Ledger      uint32    `json:"ledger"`
	TxHash      string    `json:"tx_hash"`
	OpIndex     uint32    `json:"op_index"`
	Timestamp   time.Time `json:"ts"`
	BaseAsset   string    `json:"base_asset"`
	QuoteAsset  string    `json:"quote_asset"`
	BaseAmount  string    `json:"base_amount"`
	QuoteAmount string    `json:"quote_amount"`
	Price       string    `json:"price"` // quote/base as decimal
}

// tradeRowFrom converts canonical.Trade → wire shape. Price is
// computed at `decimals` fractional digits (default 10 — generous
// enough for sub-stroop precision without being absurd).
func tradeRowFrom(t canonical.Trade, decimals int) TradeRow {
	if decimals <= 0 {
		decimals = 10
	}
	return TradeRow{
		Source:      t.Source,
		Ledger:      t.Ledger,
		TxHash:      t.TxHash,
		OpIndex:     t.OpIndex,
		Timestamp:   t.Timestamp,
		BaseAsset:   t.Pair.Base.String(),
		QuoteAsset:  t.Pair.Quote.String(),
		BaseAmount:  t.BaseAmount.String(),
		QuoteAmount: t.QuoteAmount.String(),
		Price:       priceRatioDecimal(t, decimals),
	}
}

// ─── Handler ──────────────────────────────────────────────────────

// handleHistory serves GET /v1/history?base=<id>&quote=<id>&from=<rfc3339>&to=<rfc3339>&limit=<int>.
//
// Defaults:
//   - from: to - 1h (1-hour window rolling back from `to`)
//   - to:   now
//   - limit: 1000 (server clamps to ≤ 10000)
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/history-unavailable",
			"History serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	base, quote, ok := parseBaseQuote(w, r)
	if !ok {
		return
	}
	pair, err := canonical.NewPair(base, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest,
			err.Error())
		return
	}

	from, to, ok := parseFromTo(w, r)
	if !ok {
		return
	}

	limit := 1000
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 10000 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 10000]")
			return
		}
		limit = parsed
	}

	// Optional cursor (opaque to clients; base64 of "<ts>:<ledger>").
	// Shadows `from` when present — otherwise paginating callers
	// would re-request duplicate rows on each page.
	var afterTs time.Time
	var afterLedger uint32
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		ts, ledger, err := decodeHistoryCursor(raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-cursor",
				"Invalid cursor", http.StatusBadRequest, err.Error())
			return
		}
		afterTs = ts
		afterLedger = ledger
	}

	trades, err := reader.TradesInRangeAfter(r.Context(), pair, from, to, afterTs, afterLedger, limit)
	if err != nil {
		s.logger.Error("TradesInRangeAfter failed",
			"err", err,
			"base", base.String(), "quote", quote.String(),
			"from", from, "to", to)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	rows := make([]TradeRow, len(trades))
	for i, t := range trades {
		rows[i] = tradeRowFrom(t, 10)
	}

	// If the page is full, emit a next-cursor pointing at the last
	// row we returned. Clients just re-issue the same request with
	// ?cursor=<next> to drain subsequent pages. When len < limit, the
	// window is exhausted — no cursor, no next.
	env := Envelope{Data: rows, Flags: Flags{}}
	if len(trades) == limit {
		last := trades[len(trades)-1]
		env.Pagination = &Pagination{
			Next: encodeHistoryCursor(last.Timestamp, last.Ledger),
		}
	}
	writeEnvelope(w, env)
}

// encodeHistoryCursor / decodeHistoryCursor are the opaque
// over-the-wire form of a (ts, ledger) pair. Base64 keeps the
// cursor URL-safe without needing client-side URL encoding.
//
// Format inside the base64: "<unix_nanos>:<ledger>" — time is
// nanosecond-precision because a ledger's close time can be
// finer than 1s if the source ever hands us sub-second timestamps.
func encodeHistoryCursor(ts time.Time, ledger uint32) string {
	raw := fmt.Sprintf("%d:%d", ts.UnixNano(), ledger)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeHistoryCursor(s string) (time.Time, uint32, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("cursor base64: %w", err)
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return time.Time{}, 0, fmt.Errorf("cursor must be <ts_ns>:<ledger>")
	}
	tsNano, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("cursor ts: %w", err)
	}
	ledger, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("cursor ledger: %w", err)
	}
	return time.Unix(0, tsNano).UTC(), uint32(ledger), nil
}

// parseBaseQuote extracts + validates base/quote from the request.
// Returns (base, quote, true) on success; writes a problem response
// and returns ok=false on failure.
func parseBaseQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
	rawBase := r.URL.Query().Get("base")
	if rawBase == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-base",
			"Missing base parameter", http.StatusBadRequest,
			"base query parameter is required")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	base, err := canonical.ParseAsset(rawBase)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid base identifier", http.StatusBadRequest,
			err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}

	rawQuote := r.URL.Query().Get("quote")
	if rawQuote == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-quote",
			"Missing quote parameter", http.StatusBadRequest,
			"quote query parameter is required")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	quote, err := canonical.ParseAsset(rawQuote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}
	return base, quote, true
}

