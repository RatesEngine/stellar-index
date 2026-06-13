package v1

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/StellarIndex/stellar-index/internal/aggregate"
	"github.com/StellarIndex/stellar-index/internal/canonical"
)

// ohlcDefaultOutlierSigma is the default σ threshold for the outlier
// filter applied before ComputeOHLC. Unlike VWAP — which is volume-
// weighted and naturally dampens dust trades — OHLC's High/Low have
// no statistical robustness: a single 1-stroop ↔ 1-stroop SDEX dust
// trade lands at price=1 and pegs the High of an entire bar. (R-007
// in `docs/review-2026-05-10.md` — XLM/USD bar showed High=$1 from
// one such trade.)
//
// 4.0 matches the aggregator orchestrator's default
// (cfg.OutlierSigmaThreshold). Caller can override via
// ?outlier_sigma=N, including 0 to disable for raw inspection.
const ohlcDefaultOutlierSigma = 4.0

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
// Two modes share this route:
//
//  1. Single-bar (default — back-compat): no `interval` query
//     param. Returns one [OHLCBar] for the window [from, to)
//     computed from raw trades via [aggregate.ComputeOHLC]. This is
//     the original /v1/ohlc semantics.
//  2. Multi-bar series (F-0071, CG/CMC parity): `interval` is one
//     of 1m / 5m / 15m / 30m / 1h / 4h / 1d / 1w. Returns
//     [OHLCSeriesResponse.Intervals] — up to `limit` (default 100,
//     max 1000) closed bars, oldest first, sourced from the
//     prices_<n> continuous aggregates.
//
// Defaults (single-bar mode) match /v1/history:
//   - from: to - 1h
//   - to:   now (clamped to the previous closed-bucket boundary)
//
// Defaults (series mode) are interval-aware:
//   - to:   now snapped DOWN to interval boundary
//   - from: to - limit*interval
func (s *Server) handleOHLC(w http.ResponseWriter, r *http.Request) {
	reader := s.history
	if reader == nil {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/ohlc-unavailable",
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
			"https://api.stellarindex.io/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest, err.Error())
		return
	}

	// Branch to the multi-bar series handler when `interval` is
	// supplied. Invalid intervals 400 before any other work.
	if raw := r.URL.Query().Get("interval"); raw != "" {
		interval, ok := parseOHLCInterval(w, r, raw)
		if !ok {
			return
		}
		s.handleOHLCSeries(w, r, pair, interval)
		return
	}

	// Clamped to a closed-bucket boundary when `to` defaults to "now"
	// per ADR-0015.
	from, to, _, ok := parseFromToClamped(w, r)
	if !ok {
		return
	}

	sigma, ok := parseOHLCOutlierSigma(w, r)
	if !ok {
		return
	}

	// Pull all trades in window (capped at the handler's ceiling —
	// if the window has more trades than that, the bar will under-
	// count. Aggregator-persisted CAGGs will replace this raw-scan
	// path once they're live.)
	const maxTradesForOHLC = 10000
	trades, triangulated, err := s.ohlcTradesWithStablecoinFallback(r.Context(), pair, from, to, maxTradesForOHLC)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("TradesInRange failed for OHLC",
			"err", err,
			"base", base.String(), "quote", quote.String(),
			"from", from, "to", to)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// Capture the pre-filter length so Truncated reflects whether the
	// WINDOW hit the cap — not whether the post-outlier-filter slice
	// happens to equal it. Mirrors vwap.go; computing it after
	// FilterOutliers would yield false negatives whenever the filter
	// dropped any trade. See G2-05.
	preFilter := len(trades)
	if sigma > 0 {
		trades = aggregate.FilterOutliers(trades, sigma)
	}

	bar, err := aggregate.ComputeOHLC(trades)
	if errors.Is(err, aggregate.ErrNoTrades) {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/no-trades",
			"No trades in window", http.StatusNotFound,
			"no trades observed for "+pair.Base.String()+"/"+pair.Quote.String()+
				" between "+from.Format(time.RFC3339)+" and "+to.Format(time.RFC3339))
		return
	}
	if err != nil {
		s.logger.Error("ComputeOHLC failed", "err", err)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/internal",
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
		Truncated:   preFilter == maxTradesForOHLC,
	}, Flags{Triangulated: triangulated})
}

// parseOHLCOutlierSigma parses the optional ?outlier_sigma=N query
// parameter, defaulting to [ohlcDefaultOutlierSigma]. Mirrors
// /v1/vwap's parser but with a non-zero default — see the constant
// docs for why OHLC needs the floor.
//
// `outlier_sigma=0` is the explicit opt-out: callers who want raw
// per-trade extremes (e.g. the explorer's "show every print" view)
// pass it to disable filtering entirely.
//
// Reports ok=false after writing a problem+json on parse failure.
func parseOHLCOutlierSigma(w http.ResponseWriter, r *http.Request) (float64, bool) {
	raw := r.URL.Query().Get("outlier_sigma")
	if raw == "" {
		return ohlcDefaultOutlierSigma, true
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-sigma",
			"Invalid outlier_sigma", http.StatusBadRequest,
			"outlier_sigma must be a non-negative finite number; omit for the default ("+strconv.FormatFloat(ohlcDefaultOutlierSigma, 'f', -1, 64)+") or 0 to disable filtering")
		return 0, false
	}
	return v, true
}

// ohlcTradesWithStablecoinFallback wraps HistoryReader.TradesInRange
// with the same X/fiat:USD → X/<peg> retry shape used by the chart
// handler (chartStablecoinFallback) and the price handlers
// (tryStablecoinFiatProxy). When the literal pair has zero trades
// AND quote is fiat:USD AND the operator declared classic USD pegs
// in `[trades].usd_pegged_classic_assets`, retries against each
// peg in priority order; first non-empty result wins. triangulated=true
// when the fallback fired so the handler can stamp flags.triangulated.
//
// Without this, /v1/ohlc?base=native&quote=fiat:USD 404s with "no
// trades in window" out-of-the-box on every fresh deployment — same
// root cause as #1217 (/v1/price), #1218 (/v1/price/tip), #1015
// (/v1/chart). Freighter RFP §3 names /v1/ohlc as a launch-blocker
// for the asset-detail surface.
func (s *Server) ohlcTradesWithStablecoinFallback(
	ctx context.Context, pair canonical.Pair, from, to time.Time, maxTrades int,
) ([]canonical.Trade, bool, error) {
	trades, err := s.history.TradesInRange(ctx, pair, from, to, maxTrades)
	if err != nil {
		return nil, false, err
	}
	if len(trades) > 0 {
		return trades, false, nil
	}
	if pair.Quote.Type != canonical.AssetFiat || pair.Quote.Code != "USD" {
		return trades, false, nil
	}
	for _, peg := range s.usdPeggedClassics {
		if peg.Equal(pair.Base) {
			continue
		}
		proxied, err := canonical.NewPair(pair.Base, peg)
		if err != nil {
			continue
		}
		pp, err := s.history.TradesInRange(ctx, proxied, from, to, maxTrades)
		if err != nil || len(pp) == 0 {
			continue
		}
		return pp, true, nil
	}
	return trades, false, nil
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
//
// `window` is an optional convenience for CG-style customers who
// don't want to compute `from = now - duration` themselves. Accepted
// formats follow [time.ParseDuration] (ns/us/ms/s/m/h) plus a
// trailing-`d` shortcut for days (e.g. `7d`). When supplied, `from`
// is set to `to - window`. Combining `window` with an explicit
// `from` is a 400 — they're conflicting controls for the same value;
// rejecting it loudly catches the F-0072 "I asked for 24h and got a
// 1h default" surprise. Combining `window` with an explicit `to` is
// fine — gives an arbitrary-anchored window of the requested length.
func parseFromTo(w http.ResponseWriter, r *http.Request) (from, to time.Time, ok bool) {
	to = time.Now().UTC()
	if raw := r.URL.Query().Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-time",
				"Invalid `to` timestamp", http.StatusBadRequest,
				"to must be RFC 3339")
			return time.Time{}, time.Time{}, false
		}
		to = parsed.UTC()
	}
	from = to.Add(-time.Hour)
	windowRaw := r.URL.Query().Get("window")
	fromRaw := r.URL.Query().Get("from")
	if windowRaw != "" {
		if fromRaw != "" {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-time",
				"`window` and `from` are mutually exclusive", http.StatusBadRequest,
				"pass one or the other — `window=24h` is shorthand for `from=to-24h`")
			return time.Time{}, time.Time{}, false
		}
		d, err := parseWindowDuration(windowRaw)
		if err != nil {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-time",
				"Invalid `window` duration", http.StatusBadRequest,
				err.Error())
			return time.Time{}, time.Time{}, false
		}
		if d <= 0 {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-time",
				"`window` must be positive", http.StatusBadRequest,
				"got "+windowRaw)
			return time.Time{}, time.Time{}, false
		}
		from = to.Add(-d)
	} else if fromRaw != "" {
		parsed, err := time.Parse(time.RFC3339, fromRaw)
		if err != nil {
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/invalid-time",
				"Invalid `from` timestamp", http.StatusBadRequest,
				"from must be RFC 3339")
			return time.Time{}, time.Time{}, false
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-time",
			"`from` must be before `to`", http.StatusBadRequest, "")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

// parseWindowDuration accepts the same units as [time.ParseDuration]
// (ns/us/ms/s/m/h) and additionally a trailing-`d` shortcut for days
// (e.g. `7d` = 168h). Multiple units in one string are NOT supported
// for the `d` shortcut (`1d12h` is rejected) — that ambiguity is
// best avoided in user-facing query params; clients wanting odd
// durations can express them in hours.
func parseWindowDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if last := s[len(s)-1]; last == 'd' || last == 'D' {
		days, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
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
			"https://api.stellarindex.io/errors/invalid-time",
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
