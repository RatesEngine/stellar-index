package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ChartSeries is the wire shape for /v1/chart. Mirrors the OpenAPI
// ChartEnvelope.data shape. See ADR-0020 for the contract decision.
type ChartSeries struct {
	AssetID     string             `json:"asset_id"`
	Quote       string             `json:"quote"`
	Timeframe   string             `json:"timeframe"`
	Granularity string             `json:"granularity"`
	PriceType   string             `json:"price_type"` // "vwap" today; "twap" reserved
	Points      []HistoryPointWire `json:"points"`
}

// chartTimeframeSpec captures what each RFP-prescribed timeframe
// translates to: a window duration and a default granularity.
// `all` has zero duration → no lower bound (since-inception).
type chartTimeframeSpec struct {
	Duration       time.Duration
	DefaultGranule string
}

// chartTimeframes is the canonical timeframe → spec table per
// ADR-0020. Adding a new timeframe is a one-line change here plus
// an OpenAPI enum update.
var chartTimeframes = map[string]chartTimeframeSpec{
	"1h":  {Duration: time.Hour, DefaultGranule: "1m"},
	"24h": {Duration: 24 * time.Hour, DefaultGranule: "15m"},
	"1w":  {Duration: 7 * 24 * time.Hour, DefaultGranule: "1h"},
	"1mo": {Duration: 30 * 24 * time.Hour, DefaultGranule: "4h"},
	"1y":  {Duration: 365 * 24 * time.Hour, DefaultGranule: "1d"},
	"all": {Duration: 0, DefaultGranule: "1d"},
}

// handleChart serves
// GET /v1/chart?asset=<id>&quote=<id>&timeframe=<tf>&granularity=<g>&price_type=<pt>
//
// Defaults: quote=USD, timeframe=24h, granularity=(per timeframe
// table), price_type=vwap. Response is a CAGG-served series of
// CLOSED buckets (ADR-0015) within the timeframe window.
//
// price_type=twap returns 400 — reserved for forward compat but
// not yet served (see ADR-0020).
func (s *Server) handleChart(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/history-unavailable",
			"History serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	pair, ok := parseChartPair(w, r)
	if !ok {
		return
	}
	tfRaw, tf, gran, priceType, ok := parseChartParams(w, r)
	if !ok {
		return
	}

	var from time.Time
	if tf.Duration > 0 {
		from = time.Now().Add(-tf.Duration).UTC()
	}

	// 8s ceiling on the chart query + downstream stablecoin
	// fallback. Same pattern as #1082 / #1099 / #1100 / #1101.
	// The chart's prices_1m / prices_5m / prices_1h scan can take
	// 5–10s on a cold cache for long timeframes (`?timeframe=1y`
	// + `granularity=1h` is ~8 760 buckets).
	chartCtx, chartCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer chartCancel()
	points, err := s.history.HistoryPointsInRange(chartCtx, pair, gran, from, time.Time{}, historyMaxPoints)
	if errors.Is(err, ErrUnknownGranularity) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-granularity",
			"Invalid granularity", http.StatusBadRequest,
			fmt.Sprintf("granularity must be one of: 1m, 15m, 1h, 4h, 1d, 1w, 1mo (got %q)", gran))
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("HistoryPointsInRange deadline exceeded",
				"asset", pair.Base.String(), "quote", pair.Quote.String(),
				"timeframe", tfRaw, "granularity", gran)
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/chart-timeout",
				"Chart query timed out", http.StatusServiceUnavailable,
				"the underlying prices_1m / prices_5m / prices_1h scan didn't return in 8s; cache may still be warming. Retry in a few seconds.")
			return
		}
		s.logger.Error("HistoryPointsInRange failed",
			"err", err, "asset", pair.Base.String(), "quote", pair.Quote.String(),
			"timeframe", tfRaw, "granularity", gran)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	triangulated := false
	if len(points) == 0 {
		// Stablecoin fallback inherits chartCtx so the 8s ceiling
		// covers the proxy retry too — without that, an empty
		// literal pair could spend another 8s on each pegged
		// alternative (10+ pegs × 8s each).
		if fp, ok := s.chartStablecoinFallback(chartCtx, pair, gran, from); ok {
			points = fp
			triangulated = true
		}
	}

	wire := make([]HistoryPointWire, len(points))
	for i, p := range points {
		wire[i] = HistoryPointWire{T: p.Bucket, P: p.VWAP, VUSD: p.VolumeUSD}
	}

	writeJSON(w, ChartSeries{
		AssetID:     pair.Base.String(),
		Quote:       pair.Quote.String(),
		Timeframe:   tfRaw,
		Granularity: gran,
		PriceType:   priceType,
		Points:      wire,
	}, Flags{Triangulated: triangulated})
}

// chartStablecoinFallback handles the X/fiat:USD → X/<peg> retry
// path. The literal pair query never has rows in prices_1m for
// fiat:USD because the synthetic stablecoin → USD mapping is
// applied at /v1/coins read time, not at write time. When the
// literal pair returned 0 points and the quote is fiat:USD, walk
// the operator-declared USD-pegged classics and return the first
// non-empty result. ok=false when no fallback fires (caller keeps
// the empty result + leaves triangulated=false).
//
// Extracted to keep handleChart under the gocognit ceiling.
func (s *Server) chartStablecoinFallback(
	ctx context.Context, pair canonical.Pair, gran string, from time.Time,
) ([]HistoryPoint, bool) {
	if pair.Quote.Type != canonical.AssetFiat || pair.Quote.Code != "USD" {
		return nil, false
	}
	for _, peg := range s.usdPeggedClassics {
		if peg.Equal(pair.Base) {
			continue
		}
		proxied, err := canonical.NewPair(pair.Base, peg)
		if err != nil {
			continue
		}
		pp, err := s.history.HistoryPointsInRange(ctx, proxied, gran, from, time.Time{}, historyMaxPoints)
		if err != nil || len(pp) == 0 {
			continue
		}
		return pp, true
	}
	return nil, false
}

// parseChartPair builds the canonical Pair from query params,
// rejecting identity pairs. ok=false on any error (problem written).
func parseChartPair(w http.ResponseWriter, r *http.Request) (canonical.Pair, bool) {
	asset, quote, ok := parseChartAssetQuote(w, r)
	if !ok {
		return canonical.Pair{}, false
	}
	if asset.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-pair",
			"Asset is the quote", http.StatusBadRequest,
			"asset and quote must differ")
		return canonical.Pair{}, false
	}
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest, err.Error())
		return canonical.Pair{}, false
	}
	return pair, true
}

// parseChartParams resolves timeframe, granularity, and price_type
// — applying ADR-0020 defaults and rejecting unsupported values.
// Returns (raw timeframe, timeframe spec, granularity, price_type,
// ok). ok=false on any validation failure (problem written).
func parseChartParams(w http.ResponseWriter, r *http.Request) (string, chartTimeframeSpec, string, string, bool) {
	tfRaw := r.URL.Query().Get("timeframe")
	if tfRaw == "" {
		tfRaw = "24h"
	}
	tf, ok := chartTimeframes[tfRaw]
	if !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-timeframe",
			"Invalid timeframe", http.StatusBadRequest,
			fmt.Sprintf("timeframe must be one of: 1h, 24h, 1w, 1mo, 1y, all (got %q)", tfRaw))
		return "", chartTimeframeSpec{}, "", "", false
	}
	gran := r.URL.Query().Get("granularity")
	if gran == "" {
		gran = tf.DefaultGranule
	}
	priceType := r.URL.Query().Get("price_type")
	if priceType == "" {
		priceType = "vwap"
	}
	if priceType == "twap" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-type-not-supported",
			"price_type=twap deferred to post-launch", http.StatusBadRequest,
			"the chart endpoint accepts price_type=vwap today; multi-bar TWAP charts are deferred to L7.8 in the launch-readiness backlog (single-bar TWAP is available now via /v1/twap). The deferral is documented in ADR-0020 §price_type handling: shipping on-the-fly TWAP from the 1m CAGG today would create a one-time consumer-visible math shift when the proper TWAP CAGG ships later, so we'd rather defer than ship-and-rotate")
		return "", chartTimeframeSpec{}, "", "", false
	}
	if priceType != "vwap" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-price-type",
			"Invalid price_type", http.StatusBadRequest,
			fmt.Sprintf("price_type must be one of: vwap, twap (got %q)", priceType))
		return "", chartTimeframeSpec{}, "", "", false
	}
	return tfRaw, tf, gran, priceType, true
}

// parseChartAssetQuote pulls `asset` (required) + `quote` (default
// fiat:USD per defaultPriceQuote) from the chart request. Returns
// ok=false after writing a problem response on any parse error.
func parseChartAssetQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
	rawAsset := r.URL.Query().Get("asset")
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest, err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}
	quote := defaultPriceQuote
	if rawQuote := r.URL.Query().Get("quote"); rawQuote != "" {
		q, err := canonical.ParseAsset(rawQuote)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest, err.Error())
			return canonical.Asset{}, canonical.Asset{}, false
		}
		quote = q
	}
	return asset, quote, true
}
