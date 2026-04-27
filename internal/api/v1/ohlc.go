package v1

import (
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// OHLCBar is the wire shape for /v1/ohlc entries. All prices are
// decimal strings (ADR-0003). volume fields are in the asset's
// smallest unit (stroop-equivalent).
//
// Truncated signals the window had more trades than the server's
// per-request cap — High/Low may not reflect the actual extreme
// in the window (only in the chronologically-first N trades). See
// VWAPResult.Truncated for the same semantics.
type OHLCBar struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Open        string    `json:"open"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	BaseVolume  string    `json:"base_volume"`
	QuoteVolume string    `json:"quote_volume"`
	TradeCount  int       `json:"trade_count"`
	Truncated   bool      `json:"truncated"`
}

// ohlcPriceDigits is how many fractional digits the wire OHLC
// prices carry. Ten is generous enough to represent sub-stroop
// prices without being absurd — consistent with the /v1/history
// price field.
const ohlcPriceDigits = 10

// handleOHLC serves GET /v1/ohlc?base=...&quote=...&from=...&to=...
//
// Single-bar response for the window [from, to). Interval-series
// support (N bars, each interval-seconds wide) lands in a follow-up.
//
// Defaults match /v1/history:
//   - from: to - 1h
//   - to:   now
func (s *Server) handleOHLC(w http.ResponseWriter, r *http.Request) {
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/ohlc-unavailable",
			"OHLC serving not configured", http.StatusServiceUnavailable,
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
			"Invalid pair", http.StatusBadRequest, err.Error())
		return
	}

	// Clamped to a closed-bucket boundary when `to` defaults to "now"
	// per ADR-0015.
	from, to, _, ok := parseFromToClamped(w, r)
	if !ok {
		return
	}

	// Pull all trades in window (capped at the handler's ceiling —
	// if the window has more trades than that, the bar will under-
	// count. Aggregator-persisted CAGGs will replace this raw-scan
	// path once they're live.)
	const maxTradesForOHLC = 10000
	trades, err := reader.TradesInRange(r.Context(), pair, from, to, maxTradesForOHLC)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("TradesInRange failed for OHLC",
			"err", err,
			"base", base.String(), "quote", quote.String(),
			"from", from, "to", to)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	bar, err := aggregate.ComputeOHLC(trades)
	if errors.Is(err, aggregate.ErrNoTrades) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/no-trades",
			"No trades in window", http.StatusNotFound,
			"no trades observed for "+pair.Base.String()+"/"+pair.Quote.String()+
				" between "+from.Format(time.RFC3339)+" and "+to.Format(time.RFC3339))
		return
	}
	if err != nil {
		s.logger.Error("ComputeOHLC failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	writeJSON(w, OHLCBar{
		From:        from,
		To:          to,
		Open:        ratToDecimal(bar.Open, ohlcPriceDigits),
		High:        ratToDecimal(bar.High, ohlcPriceDigits),
		Low:         ratToDecimal(bar.Low, ohlcPriceDigits),
		Close:       ratToDecimal(bar.Close, ohlcPriceDigits),
		BaseVolume:  bar.BaseVolume.String(),
		QuoteVolume: bar.QuoteVolume.String(),
		TradeCount:  bar.TradeCount,
		Truncated:   len(trades) == maxTradesForOHLC,
	}, Flags{})
}

// parseFromTo parses the from/to query params, applying the same
// 1-hour default as /v1/history. Writes problem + returns ok=false
// on failure.
//
// `to` defaults to the request's wall-clock now. Use this for
// /v1/history where the client wants exactly the trades in the
// stated range — the API mustn't quietly snap their range to a
// boundary. For aggregated rate endpoints (VWAP/TWAP/OHLC) use
// [parseFromToClamped] instead, per ADR-0015.
func parseFromTo(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	to = time.Now().UTC()
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-time",
				"Invalid `to` timestamp", http.StatusBadRequest,
				"to must be RFC 3339")
			return time.Time{}, time.Time{}, false
		}
		to = parsed.UTC()
	}
	from = to.Add(-time.Hour)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-time",
				"Invalid `from` timestamp", http.StatusBadRequest,
				"from must be RFC 3339")
			return time.Time{}, time.Time{}, false
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-time",
			"`from` must be before `to`", http.StatusBadRequest, "")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// closedBucketWindow is the boundary granularity used by
// [parseFromToClamped] when `to` defaults to "now". It's the smallest
// window the aggregator's CAGG ladder will eventually expose
// (matching `prices_30s`'s eventual chunk size); 30 s also matches
// the Freighter ≤30 s freshness SLA.
//
// Per ADR-0015, snapping the implicit "now" to this boundary is what
// makes "all 3 regions return the same rate" a real property: a
// request landing at 12:00:01.234 across two regions both clamp to
// 12:00:00.000 and answer over the identical [from, 12:00:00.000)
// window — same trades, same result, same JSON bytes once
// replication has carried the trades to both regions.
const closedBucketWindow = 30 * time.Second

// parseFromToClamped is the rate-endpoint flavour of [parseFromTo]:
// when the client did NOT specify `to`, clamp the default-now value
// to the previous [closedBucketWindow] boundary. When the client
// DID specify `to`, use it verbatim — they're explicitly asking
// about a specific historical range, not "now", and snapping their
// timestamp would be surprising.
//
// Sets the *clamped flag so callers can surface "this response
// reflects a closed-bucket window" in their wire output if they
// want to (today the rate handlers don't expose it explicitly,
// but the From/To fields they return already carry the snapped
// values).
func parseFromToClamped(w http.ResponseWriter, r *http.Request) (from, to time.Time, clamped, ok bool) {
	toExplicit := r.URL.Query().Get("to") != ""
	from, to, ok = parseFromTo(w, r)
	if !ok {
		return time.Time{}, time.Time{}, false, false
	}
	if !toExplicit {
		// Snap to the previous boundary. time.Truncate rounds down to
		// a multiple of the duration since the zero time (for UTC
		// instants this is the Unix epoch's whole-second alignment),
		// which is exactly the alignment we want for ADR-0015's "all
		// regions agree" property.
		clampedTo := to.Truncate(closedBucketWindow)
		// If `from` was also defaulted (= to - 1h), shift it by the
		// same delta so the window length stays 1h. If `from` was
		// explicit, leave it alone — the client's range is preserved
		// up to the new (closed) right edge.
		fromExplicit := r.URL.Query().Get("from") != ""
		if !fromExplicit {
			from = from.Add(clampedTo.Sub(to))
		}
		to = clampedTo
		clamped = true
	}
	if !from.Before(to) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-time",
			"`from` must be before `to` after closed-bucket clamp",
			http.StatusBadRequest,
			"the requested range collapsed below a single closed window — widen `from` or specify an explicit `to`")
		return time.Time{}, time.Time{}, false, false
	}
	return from, to, clamped, true
}

// ratToDecimal renders a *big.Rat as a fixed-width decimal string
// with `digits` fractional places, truncating (floors) — matching
// priceRatioDecimal's rounding choice for consistency across
// /v1/price and /v1/history.
//
// Returns "0" for nil input.
func ratToDecimal(r *big.Rat, digits int) string { //nolint:unparam // digits kept for API flexibility; currently always ohlcPriceDigits
	if r == nil {
		return "0"
	}
	if digits < 0 {
		digits = 0
	}
	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())

	// Scale numerator by 10^digits, divide, format.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	num.Mul(num, scale)
	integer, _ := new(big.Int).DivMod(num, den, new(big.Int))

	// Preserve sign explicitly — Int.DivMod handles negatives but the
	// format below is easier to reason about without it.
	sign := ""
	if integer.Sign() < 0 {
		sign = "-"
		integer.Abs(integer)
	}

	s := integer.String()
	if digits == 0 {
		return sign + s
	}
	if len(s) <= digits {
		pad := digits - len(s) + 1
		s = leftPad(s, pad, '0')
	}
	split := len(s) - digits
	return sign + s[:split] + "." + s[split:]
}
