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

	from, to, ok := parseFromTo(w, r)
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

// ratToDecimal renders a *big.Rat as a fixed-width decimal string
// with `digits` fractional places, truncating (floors) — matching
// priceRatioDecimal's rounding choice for consistency across
// /v1/price and /v1/history.
//
// Returns "0" for nil input.
func ratToDecimal(r *big.Rat, digits int) string {
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
