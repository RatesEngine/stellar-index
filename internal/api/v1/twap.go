package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// TWAPResult is the wire shape for /v1/twap responses.
//
// Price is the time-weighted mean as a decimal string (10-digit
// precision, consistent with VWAP / OHLC). TradeCount is the number
// of trades that contributed to the weighting. Truncated signals
// the window had more trades than the server's per-request cap;
// see VWAPResult.Truncated for the same semantics.
type TWAPResult struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Price      string    `json:"price"`
	TradeCount int       `json:"trade_count"`
	Truncated  bool      `json:"truncated"`
}

// handleTWAP serves GET /v1/twap?base=...&quote=...&from=...&to=...
//
// Defaults match /v1/history (1-hour window ending now). TWAP
// weights each trade's price by the duration until the next trade
// (or windowEnd for the final trade); see internal/aggregate/twap.go
// for the formula.
//
// No outlier_sigma param on TWAP — time-weighting is itself a form
// of outlier resistance (a single spurious print that corrects
// 1 second later has 1-second weight, not a full window's worth).
func (s *Server) handleTWAP(w http.ResponseWriter, r *http.Request) {
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/twap-unavailable",
			"TWAP serving not configured", http.StatusServiceUnavailable,
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

	const maxTrades = 10000
	trades, err := reader.TradesInRange(r.Context(), pair, from, to, maxTrades)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("TradesInRange failed for TWAP",
			"err", err, "base", base.String(), "quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	price, err := aggregate.TWAP(trades, to)
	if errors.Is(err, aggregate.ErrNoTrades) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/no-trades",
			"No trades in window", http.StatusNotFound,
			"no trades observed for "+pair.Base.String()+"/"+pair.Quote.String()+
				" between "+from.Format(time.RFC3339)+" and "+to.Format(time.RFC3339))
		return
	}
	if err != nil {
		s.logger.Error("TWAP failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	writeJSON(w, TWAPResult{
		From:       from,
		To:         to,
		Price:      ratToDecimal(price, ohlcPriceDigits),
		TradeCount: len(trades),
		Truncated:  len(trades) == maxTrades,
	}, Flags{})
}
