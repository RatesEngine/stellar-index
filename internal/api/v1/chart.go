package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// ChartSeries is the wire shape for /v1/chart. Mirrors the OpenAPI
// ChartEnvelope.data shape. See ADR-0020 for the contract decision.
//
// `truncated` + `data_starts_at` signal that the requested timeframe
// extends beyond the deployment's actual retention. R1 today only
// has ~7 days of high-resolution history but still accepts
// `?timeframe=1y` — without these fields a consumer can't tell
// whether the returned 7 daily points are "the last 7 days of a
// long history" or "all the history this deployment has". R-013 in
// `docs/review-2026-05-10.md`.
type ChartSeries struct {
	AssetID       string             `json:"asset_id"`
	Quote         string             `json:"quote"`
	Timeframe     string             `json:"timeframe"`
	Granularity   string             `json:"granularity"`
	PriceType     string             `json:"price_type"` // "vwap" today; "twap" reserved
	Points        []HistoryPointWire `json:"points"`
	Truncated     bool               `json:"truncated"`                // true when the requested window starts before the earliest available data
	DataStartsAt  *time.Time         `json:"data_starts_at,omitempty"` // earliest bucket timestamp present in the result; only populated when Truncated
	RequestedFrom *time.Time         `json:"requested_from,omitempty"` // window start the consumer asked for; only populated when Truncated
}

// chartTimeframeSpec captures what each prescribed timeframe
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
			"https://api.stellarindex.io/errors/history-unavailable",
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

	// Dispatch to specialised handlers when the request shape calls
	// for it; fall through to the default vwap-on-prices_1m path
	// when no specialisation matches.
	if s.dispatchSpecialisedChart(w, r, pair, tfRaw, gran, priceType, from) {
		return
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
			"https://api.stellarindex.io/errors/invalid-granularity",
			"Invalid granularity", http.StatusBadRequest,
			fmt.Sprintf("granularity must be one of: 1m, 15m, 1h, 4h, 1d, 1w, 1mo (got %q)", gran))
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(chartCtx, err) {
			s.logger.Warn("HistoryPointsInRange deadline exceeded",
				"asset", pair.Base.String(), "quote", pair.Quote.String(),
				"timeframe", tfRaw, "granularity", gran)
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/chart-timeout",
				"Chart query timed out", http.StatusServiceUnavailable,
				"the underlying prices_1m / prices_5m / prices_1h scan didn't return in 8s; cache may still be warming. Retry in a few seconds.")
			return
		}
		s.logger.Error("HistoryPointsInRange failed",
			"err", err, "asset", pair.Base.String(), "quote", pair.Quote.String(),
			"timeframe", tfRaw, "granularity", gran)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/internal",
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

	series := ChartSeries{
		AssetID:     pair.Base.String(),
		Quote:       pair.Quote.String(),
		Timeframe:   tfRaw,
		Granularity: gran,
		PriceType:   priceType,
		Points:      wire,
	}
	// Retention-truncation signal. We treat the response as truncated
	// when the consumer asked for a bounded window AND the earliest
	// returned bucket starts more than one granularity unit after
	// `from` — that's the difference between "the last 7 days are
	// flat" and "this deployment only has 7 days of data". R-013.
	//
	// `timeframe=all` (from.IsZero()) intentionally never trips the
	// flag — that timeframe explicitly means "everything you have",
	// so a short result IS the full result.
	if !from.IsZero() && len(points) > 0 {
		if grace := chartGranularityGrace(gran); points[0].Bucket.Sub(from) > grace {
			startsAt := points[0].Bucket
			requested := from
			series.Truncated = true
			series.DataStartsAt = &startsAt
			series.RequestedFrom = &requested
		}
	}

	writeJSON(w, series, Flags{Triangulated: triangulated})
}

// dispatchSpecialisedChart routes to a non-default chart handler
// when the request matches a specialised shape: market_cap series,
// fiat:fiat pairs (which live in fx_quotes, not prices_1m). Returns
// true when a specialised handler took the request (caller bails);
// false to let the default path proceed.
func (s *Server) dispatchSpecialisedChart(
	w http.ResponseWriter,
	r *http.Request,
	pair canonical.Pair,
	tfRaw, gran, priceType string,
	from time.Time,
) bool {
	if priceType == "market_cap" {
		s.handleChartMarketCap(w, r, pair, tfRaw, gran, from)
		return true
	}
	if pair.Base.Type == canonical.AssetFiat && pair.Quote.Type == canonical.AssetFiat {
		s.handleChartFiat(w, r, pair, tfRaw, gran, priceType, from)
		return true
	}
	return false
}

// handleChartFiat serves /v1/chart for fiat:fiat pairs out of the
// fx_quotes hypertable. Frankfurter (and historically Massive) writes
// daily ECB reference rates into fx_quotes — so any sub-daily
// granularity (1m / 15m / 1h / 4h) just gets the daily bar replicated
// to the consumer's chosen grain (front-end renders flat candles).
//
// Pair conventions:
//   - fiat:CCY/fiat:USD  → reader returns rate (1 CCY = N USD); use InverseUSD
//   - fiat:USD/fiat:CCY  → reader returns inverse (1 USD = N CCY); use RateUSD
//   - fiat:CCY1/fiat:CCY2 (cross) → not yet supported; returns empty
//     series + a non-fatal "triangulated=false". The explorer falls
//     back to "no data for this window"; a follow-up can implement
//     cross-currency triangulation on read.
func (s *Server) handleChartFiat(
	w http.ResponseWriter,
	r *http.Request,
	pair canonical.Pair,
	tfRaw, gran, priceType string,
	from time.Time,
) {
	series := ChartSeries{
		AssetID:     pair.Base.String(),
		Quote:       pair.Quote.String(),
		Timeframe:   tfRaw,
		Granularity: gran,
		PriceType:   priceType,
		Points:      []HistoryPointWire{},
	}

	if s.fxHistory == nil {
		writeJSON(w, series, Flags{})
		return
	}

	// Identify the non-USD ticker + which side it's on.
	var ticker string
	var useInverse bool
	switch {
	case pair.Base.Code == "USD" && pair.Quote.Code != "USD":
		ticker, useInverse = pair.Quote.Code, false
	case pair.Quote.Code == "USD" && pair.Base.Code != "USD":
		ticker, useInverse = pair.Base.Code, true
	default:
		// Cross-fiat (e.g. EUR/JPY) or USD/USD — neither supported here.
		writeJSON(w, series, Flags{})
		return
	}

	// Default window: trailing 1y when timeframe=all (open-ended would
	// hammer Postgres for 25y on every request; the chart consumer
	// only renders one screen anyway).
	to := time.Now().UTC().Truncate(24 * time.Hour)
	queryFrom := from
	if queryFrom.IsZero() {
		queryFrom = to.AddDate(-25, 0, 0) // ECB inception
	}

	fxCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	points, err := s.fxHistory.ListFXHistory(fxCtx, ticker, queryFrom, to)
	if err != nil {
		s.logger.Warn("chart fiat fx_quotes fetch failed",
			"ticker", ticker, "err", err)
		writeJSON(w, series, Flags{})
		return
	}

	wire := make([]HistoryPointWire, 0, len(points))
	for _, p := range points {
		rate := p.RateUSD
		if useInverse {
			rate = p.InverseUSD
		}
		if rate <= 0 {
			continue
		}
		wire = append(wire, HistoryPointWire{
			T: p.Bucket,
			P: fmt.Sprintf("%.10f", rate),
			// FX rates have no volume — omit v_usd entirely.
		})
	}
	series.Points = wire

	// Retention-truncation signal — same shape as the crypto path.
	if !from.IsZero() && len(wire) > 0 {
		if grace := chartGranularityGrace(gran); wire[0].T.Sub(from) > grace {
			startsAt := wire[0].T
			requested := from
			series.Truncated = true
			series.DataStartsAt = &startsAt
			series.RequestedFrom = &requested
		}
	}

	writeJSON(w, series, Flags{})
}

// chartGranularityGrace is the gap (in time) between `from` and the
// first returned bucket above which we consider the response
// truncated by retention. Picks one granularity period — anything
// less is "the first bucket happens to be empty"; anything more
// means the underlying CAGG simply doesn't have data going that far
// back. Unknown granularity strings fall through with a generous
// 1-day grace so we don't false-positive.
func chartGranularityGrace(gran string) time.Duration {
	switch gran {
	case "1m":
		return time.Minute
	case "15m":
		return 15 * time.Minute
	case "1h":
		return time.Hour
	case "4h":
		return 4 * time.Hour
	case "1d":
		return 24 * time.Hour
	case "1w":
		return 7 * 24 * time.Hour
	case "1mo":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
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
			"https://api.stellarindex.io/errors/identity-pair",
			"Asset is the quote", http.StatusBadRequest,
			"asset and quote must differ")
		return canonical.Pair{}, false
	}
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-pair",
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
			"https://api.stellarindex.io/errors/invalid-timeframe",
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
			"https://api.stellarindex.io/errors/price-type-not-supported",
			"price_type=twap deferred to post-launch", http.StatusBadRequest,
			"the chart endpoint accepts price_type=vwap today; multi-bar TWAP charts are deferred to L7.8 in the launch-readiness backlog (single-bar TWAP is available now via /v1/twap). The deferral is documented in ADR-0020 §price_type handling: shipping on-the-fly TWAP from the 1m CAGG today would create a one-time consumer-visible math shift when the proper TWAP CAGG ships later, so we'd rather defer than ship-and-rotate")
		return "", chartTimeframeSpec{}, "", "", false
	}
	if priceType == "market_cap" {
		// price_type=market_cap is a separate compute path — the
		// handler dispatches to handleChartMarketCap before falling
		// through to the vwap-path. parseChartParams just accepts the
		// token here.
		return tfRaw, tf, gran, priceType, true
	}
	if priceType != "vwap" {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-price-type",
			"Invalid price_type", http.StatusBadRequest,
			fmt.Sprintf("price_type must be one of: vwap, twap, market_cap (got %q)", priceType))
		return "", chartTimeframeSpec{}, "", "", false
	}
	return tfRaw, tf, gran, priceType, true
}

// parseChartAssetQuote pulls `asset` (required) + `quote` (default
// fiat:USD per defaultPriceQuote) from the chart request. Returns
// ok=false after writing a problem response on any parse error.
func parseChartAssetQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
	rawAsset, ok := resolveAssetOrBaseParam(w, r)
	if !ok {
		return canonical.Asset{}, canonical.Asset{}, false
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest, err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}
	quote := defaultPriceQuote
	if rawQuote := r.URL.Query().Get("quote"); rawQuote != "" {
		q, err := canonical.ParseAsset(rawQuote)
		if err != nil {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest, err.Error())
			return canonical.Asset{}, canonical.Asset{}, false
		}
		quote = q
	}
	return asset, quote, true
}

// handleChartMarketCap serves /v1/chart?price_type=market_cap.
//
// Phase 1 supports fiat assets only:
//
//	asset=fiat:CNY&quote=fiat:USD&price_type=market_cap
//
// Output: daily market-cap series = M2 (verified-currency catalogue)
// × inverse_usd (fx_quotes daily snapshot of 1 CCY → N USD). Each
// bucket gets the M2 figure multiplied by the day's FX rate.
//
// Crypto assets return 501 — the market_cap_1d CAGG (supply×price
// join over time) is the proper implementation; this commit ships
// the fiat fast-path to close the explorer's "market cap over time"
// gap for the currencies surface.
func (s *Server) handleChartMarketCap(
	w http.ResponseWriter,
	r *http.Request,
	pair canonical.Pair,
	tfRaw, gran string,
	from time.Time,
) {
	// Quote must be fiat:USD — market cap is USD-denominated.
	if pair.Quote.Type != canonical.AssetFiat || pair.Quote.Code != "USD" {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-market-cap-quote",
			"market_cap requires quote=fiat:USD", http.StatusBadRequest,
			"the chart's price_type=market_cap series is always USD-denominated; pass quote=fiat:USD")
		return
	}

	// Fiat fast-path. Crypto assets return 501.
	if pair.Base.Type != canonical.AssetFiat {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/market-cap-deferred",
			"market_cap for non-fiat assets deferred", http.StatusNotImplemented,
			"price_type=market_cap is implemented for fiat:* base assets today (M2 × FX rate via fx_quotes). Crypto market-cap-over-time requires a market_cap_1d CAGG joining supply_1d × price_1d; tracked as the supply-CAGG follow-up.")
		return
	}

	if s.verifiedCurrencies == nil || s.fxHistory == nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/market-cap-unavailable",
			"market_cap not configured", http.StatusServiceUnavailable,
			"this deployment hasn't wired the verified-currency catalogue and/or fx_quotes reader")
		return
	}

	vc, ok := s.verifiedCurrencies.LookupByTicker(pair.Base.Code)
	if !ok || vc.CirculatingSupply == "" {
		writeJSON(w, emptyMarketCapSeries(pair, tfRaw, gran, from), Flags{})
		return
	}
	m2, err := parseSupply(vc.CirculatingSupply, vc.SupplyDecimals)
	if err != nil {
		s.logger.Warn("market_cap: bad catalogue supply",
			"ticker", vc.Ticker, "err", err)
		writeJSON(w, emptyMarketCapSeries(pair, tfRaw, gran, from), Flags{})
		return
	}

	// Default window: trailing 1y when timeframe=all (open-ended
	// would hammer Postgres + the catalogue M2 doesn't change over
	// time anyway, so 25y of "same number × per-day FX" is just
	// noise).
	to := time.Now().UTC().Truncate(24 * time.Hour)
	queryFrom := from
	if queryFrom.IsZero() {
		queryFrom = to.AddDate(-25, 0, 0)
	}

	fxCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	points, err := s.fxHistory.ListFXHistory(fxCtx, pair.Base.Code, queryFrom, to)
	if err != nil {
		s.logger.Warn("market_cap: fx_quotes fetch failed",
			"ticker", pair.Base.Code, "err", err)
		writeJSON(w, emptyMarketCapSeries(pair, tfRaw, gran, from), Flags{})
		return
	}

	wire := make([]HistoryPointWire, 0, len(points))
	for _, p := range points {
		if p.InverseUSD <= 0 {
			continue
		}
		mcap := m2 * p.InverseUSD
		wire = append(wire, HistoryPointWire{
			T: p.Bucket,
			P: fmt.Sprintf("%.2f", mcap),
		})
	}

	series := ChartSeries{
		AssetID:     pair.Base.String(),
		Quote:       pair.Quote.String(),
		Timeframe:   tfRaw,
		Granularity: gran,
		PriceType:   "market_cap",
		Points:      wire,
	}
	if !from.IsZero() && len(wire) > 0 {
		if grace := chartGranularityGrace(gran); wire[0].T.Sub(from) > grace {
			startsAt := wire[0].T
			requested := from
			series.Truncated = true
			series.DataStartsAt = &startsAt
			series.RequestedFrom = &requested
		}
	}
	writeJSON(w, series, Flags{})
}

// emptyMarketCapSeries is the no-data response shape used when the
// catalogue doesn't carry a supply for the asset or the FX feed has
// no rows for the requested window. Keeping it as a helper means
// every error path emits the same wire shape (empty points array,
// not null).
func emptyMarketCapSeries(pair canonical.Pair, tfRaw, gran string, _ time.Time) ChartSeries {
	return ChartSeries{
		AssetID:     pair.Base.String(),
		Quote:       pair.Quote.String(),
		Timeframe:   tfRaw,
		Granularity: gran,
		PriceType:   "market_cap",
		Points:      []HistoryPointWire{},
	}
}

// parseSupply converts the catalogue's (supply, decimals) tuple into
// a float64. The catalogue stores supplies as decimal strings in the
// asset's smallest integer unit (per the seed.yaml convention),
// alongside a decimals exponent. For fiat M2 the decimals are 0 so
// the supply is already in major units (e.g. "21700000000000" =
// $21.7T). For tokens decimals would be 7 / 18 / etc; we divide.
func parseSupply(supplyStr string, decimals int) (float64, error) {
	v, err := strconv.ParseFloat(supplyStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse supply %q: %w", supplyStr, err)
	}
	for i := 0; i < decimals; i++ {
		v /= 10
	}
	return v, nil
}
