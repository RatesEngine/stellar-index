package v1

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// VWAPResult is the wire shape for /v1/vwap responses.
//
// Price is the volume-weighted mean as a decimal string (10-digit
// precision, consistent with /v1/history + /v1/ohlc). Volumes are
// raw integer strings in the asset's smallest unit.
//
// OutliersFiltered reports how many trades the sigma filter
// removed before the VWAP computation — zero when outlier_sigma=0
// was passed or there weren't enough samples for the filter.
type VWAPResult struct {
	From             time.Time `json:"from"`
	To               time.Time `json:"to"`
	Price            string    `json:"price"`
	BaseVolume       string    `json:"base_volume"`
	QuoteVolume      string    `json:"quote_volume"`
	TradeCount       int       `json:"trade_count"`
	OutliersFiltered int       `json:"outliers_filtered"`
}

// handleVWAP serves GET /v1/vwap?base=...&quote=...&from=...&to=...&outlier_sigma=...
//
// Defaults match /v1/history (1-hour window ending now). outlier_sigma
// defaults to 0 (no filtering) — the aggregator's config-default of
// 4σ is a different layer's decision.
func (s *Server) handleVWAP(w http.ResponseWriter, r *http.Request) {
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/vwap-unavailable",
			"VWAP serving not configured", http.StatusServiceUnavailable,
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

	sigma := 0.0
	if raw := r.URL.Query().Get("outlier_sigma"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		// NaN comparisons are always false, so `v < 0` doesn't catch
		// ParseFloat("NaN"). Also reject ±Inf explicitly.
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-sigma",
				"Invalid outlier_sigma", http.StatusBadRequest,
				"outlier_sigma must be a non-negative finite number; omit or 0 disables filtering")
			return
		}
		sigma = v
	}

	const maxTrades = 10000
	trades, err := reader.TradesInRange(r.Context(), pair, from, to, maxTrades)
	if err != nil {
		s.logger.Error("TradesInRange failed for VWAP",
			"err", err, "base", base.String(), "quote", quote.String(),
			"from", from, "to", to)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	pre := len(trades)
	if sigma > 0 {
		trades = aggregate.FilterOutliers(trades, sigma)
	}
	outliersFiltered := pre - len(trades)

	price, err := aggregate.VWAP(trades)
	if errors.Is(err, aggregate.ErrNoTrades) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/no-trades",
			"No trades in window", http.StatusNotFound,
			"no trades observed for "+pair.Base.String()+"/"+pair.Quote.String()+
				" between "+from.Format(time.RFC3339)+" and "+to.Format(time.RFC3339))
		return
	}
	if err != nil {
		s.logger.Error("VWAP failed", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	writeJSON(w, VWAPResult{
		From:             from,
		To:               to,
		Price:            ratToDecimal(price, ohlcPriceDigits),
		BaseVolume:       aggregate.TotalBaseVolume(trades).String(),
		QuoteVolume:      aggregate.TotalQuoteVolume(trades).String(),
		TradeCount:       len(trades),
		OutliersFiltered: outliersFiltered,
	}, Flags{})
}
