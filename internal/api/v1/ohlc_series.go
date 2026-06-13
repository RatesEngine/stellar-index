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

// OHLCSeriesBar is one bar in the multi-bar /v1/ohlc?interval=...
// response. Wire field names are short (`t/o/h/l/c/v_base/v_quote/n`)
// to keep payloads compact at the 1000-bar maximum — CG/CMC use the
// same convention.
//
// `T` is the bucket-start timestamp aligned to UTC interval
// boundaries (1m → :00, 1h → top of hour, 1d → 00:00 UTC, 1w →
// Monday 00:00 UTC). Bucket end = `T + interval`.
type OHLCSeriesBar struct {
	T         time.Time `json:"t"`
	O         string    `json:"o"`
	H         string    `json:"h"`
	L         string    `json:"l"`
	C         string    `json:"c"`
	VBase     string    `json:"v_base"`
	VQuote    string    `json:"v_quote"`
	N         int64     `json:"n"`
	Truncated bool      `json:"truncated,omitempty"`
}

// OHLCSeriesResponse is the wire envelope for /v1/ohlc?interval=...
// — distinct from the single-bar [OHLCBar] response. CG/CMC clients
// expect a series shape (`[{t,o,h,l,c,v},...]`); this is the
// CG-parity payload (F-0071).
type OHLCSeriesResponse struct {
	Base      string          `json:"base"`
	Quote     string          `json:"quote"`
	Interval  string          `json:"interval"`
	From      time.Time       `json:"from"`
	To        time.Time       `json:"to"`
	Intervals []OHLCSeriesBar `json:"intervals"`
}

// ohlcSeriesDefaultLimit / ohlcSeriesMaxLimit are the bar-count
// envelope on a single /v1/ohlc?interval= request. The max matches
// CoinGecko's `/ohlc` cap (1000 bars). The default is large enough
// to cover a day of 1m bars (1440 capped to 100 → callers explicitly
// opt in to the larger window) without being absurd.
const (
	ohlcSeriesDefaultLimit = 100
	ohlcSeriesMaxLimit     = 1000
)

// ohlcInterval is the validated interval enum for the multi-bar
// /v1/ohlc mode. Stable wire values matching the CAGG ladder.
type ohlcInterval string

const (
	ohlcInterval1m  ohlcInterval = "1m"
	ohlcInterval5m  ohlcInterval = "5m"
	ohlcInterval15m ohlcInterval = "15m"
	ohlcInterval30m ohlcInterval = "30m"
	ohlcInterval1h  ohlcInterval = "1h"
	ohlcInterval4h  ohlcInterval = "4h"
	ohlcInterval1d  ohlcInterval = "1d"
	ohlcInterval1w  ohlcInterval = "1w"
)

// duration returns the Go [time.Duration] equivalent of an
// interval. Used for default-window sizing (defaults to N × interval
// when neither `from` nor `to` are supplied).
//
// 1w is approximated as 7*24h — DST is irrelevant for UTC alignment.
func (i ohlcInterval) duration() time.Duration {
	switch i {
	case ohlcInterval1m:
		return 1 * time.Minute
	case ohlcInterval5m:
		return 5 * time.Minute
	case ohlcInterval15m:
		return 15 * time.Minute
	case ohlcInterval30m:
		return 30 * time.Minute
	case ohlcInterval1h:
		return 1 * time.Hour
	case ohlcInterval4h:
		return 4 * time.Hour
	case ohlcInterval1d:
		return 24 * time.Hour
	case ohlcInterval1w:
		return 7 * 24 * time.Hour
	}
	return 0
}

// parseOHLCInterval validates the `interval` query param.
// Returns ok=true with the parsed enum when valid; ok=false (after
// writing a problem+json) when invalid. Empty raw → ok=false +
// zero-value: caller distinguishes "no interval supplied" from
// "interval was supplied but invalid" via raw != "".
func parseOHLCInterval(w http.ResponseWriter, r *http.Request, raw string) (ohlcInterval, bool) {
	switch raw {
	case "1m":
		return ohlcInterval1m, true
	case "5m":
		return ohlcInterval5m, true
	case "15m":
		return ohlcInterval15m, true
	case "30m":
		return ohlcInterval30m, true
	case "1h":
		return ohlcInterval1h, true
	case "4h":
		return ohlcInterval4h, true
	case "1d":
		return ohlcInterval1d, true
	case "1w":
		return ohlcInterval1w, true
	}
	writeProblem(w, r,
		"https://api.stellarindex.io/errors/invalid-interval",
		"Invalid interval", http.StatusBadRequest,
		"interval must be one of: 1m, 5m, 15m, 30m, 1h, 4h, 1d, 1w (got "+strconv.Quote(raw)+")")
	return "", false
}

// parseOHLCSeriesLimit parses the `?limit=` query param for the
// series mode. Defaults to [ohlcSeriesDefaultLimit], caps at
// [ohlcSeriesMaxLimit]. Returns ok=false (after writing a
// problem+json) on parse error / out-of-range.
func parseOHLCSeriesLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return ohlcSeriesDefaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > ohlcSeriesMaxLimit {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/limit-too-large",
			"Invalid limit", http.StatusBadRequest,
			fmt.Sprintf("limit must be an integer in [1, %d]", ohlcSeriesMaxLimit))
		return 0, false
	}
	return n, true
}

// handleOHLCSeries serves the multi-bar branch of /v1/ohlc when
// `interval` is present. Closed-bucket only (ADR-0015): the
// in-progress bucket is excluded by the storage layer's
// `bucket + interval <= now()` guard. When `from` is unset the
// handler defaults to `to - limit*interval`; when `to` is unset it
// defaults to "now" snapped to the previous interval boundary so
// the same response is byte-identical across regions in the same
// closed-window window.
//
// Single-bar callers (no `interval` query param) are routed at
// [Server.handleOHLC] BEFORE this function is reached — this
// function assumes interval is non-empty and validated.
func (s *Server) handleOHLCSeries(
	w http.ResponseWriter, r *http.Request,
	pair canonical.Pair,
	interval ohlcInterval,
) {
	limit, ok := parseOHLCSeriesLimit(w, r)
	if !ok {
		return
	}

	from, to, ok := parseOHLCSeriesFromTo(w, r, interval, limit)
	if !ok {
		return
	}

	hCtx, hCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer hCancel()
	bars, err := s.ohlcSeriesWithAliases(hCtx, pair, interval, from, to, limit)
	if errors.Is(err, ErrUnknownGranularity) {
		// Shouldn't fire — handler validated the interval — but guard
		// against a future code path that wires the storage layer
		// directly. Translate to 400 for caller clarity.
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-interval",
			"Invalid interval", http.StatusBadRequest,
			"storage layer rejected the interval — file a bug")
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if handlerTimedOut(hCtx, err) {
			s.logger.Warn("OHLCSeries deadline exceeded",
				"base", pair.Base.String(), "quote", pair.Quote.String(),
				"interval", interval, "from", from, "to", to)
			writeProblem(w, r,
				"https://api.stellarindex.io/errors/ohlc-timeout",
				"OHLC query timed out", http.StatusServiceUnavailable,
				"the underlying CAGG didn't return in 8s; cache may still be warming.")
			return
		}
		s.logger.Error("OHLCSeries failed",
			"err", err, "base", pair.Base.String(), "quote", pair.Quote.String(),
			"interval", interval, "from", from, "to", to)
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// Series mode returns 200 + empty `intervals: []` when there are
	// no closed buckets — distinct from the single-bar /v1/ohlc which
	// 404s. Series clients (charts, dashboards) expect a stable shape
	// across pairs/windows. Non-nil slice required so the wire shape
	// is byte-stable.
	if bars == nil {
		bars = []OHLCSeriesBar{}
	}
	resp := OHLCSeriesResponse{
		Base:      pair.Base.String(),
		Quote:     pair.Quote.String(),
		Interval:  string(interval),
		From:      from,
		To:        to,
		Intervals: bars,
	}
	// Fiat-quoted series are combined from USD/EUR-pegged stablecoin
	// constituents (late-bound proxy) — flag it, mirroring the single-bar
	// /v1/ohlc stablecoin-fallback path.
	writeJSON(w, resp, Flags{Triangulated: pair.Quote.Type == canonical.AssetFiat && len(bars) > 0})
}

// parseOHLCSeriesFromTo parses from/to for the series mode with
// interval-aware defaults:
//
//   - `to` unset      → now snapped DOWN to interval boundary
//     (ADR-0015 cross-region stability).
//   - `from` unset    → to - limit*interval (sized to fit `limit`
//     intervals).
//   - both supplied   → used verbatim (UTC).
//
// Returns ok=false (after writing problem+json) on parse error or
// from >= to.
func parseOHLCSeriesFromTo(
	w http.ResponseWriter, r *http.Request,
	interval ohlcInterval, limit int,
) (from, to time.Time, ok bool) {
	intervalDur := interval.duration()
	toExplicit := r.URL.Query().Get("to") != ""
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
	if !toExplicit {
		to = to.Truncate(intervalDur)
	}

	from = to.Add(-time.Duration(limit) * intervalDur)
	if raw := r.URL.Query().Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
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

// ohlcSeriesWithAliases reads the bar series trying each XLM dual-form
// alias pair (rc.89 / F-1340) and returns the FIRST non-empty series.
// The continuous aggregates key bars by the canonical id the
// contributing trades carried — CEX-driven bars live under
// `crypto:XLM`, so `?base=native` read zero bars while
// `?base=crypto:XLM` served a full series. Bars are aggregates;
// cross-alias bar FUSION is a separate design decision — first-hit
// matches the single-bar endpoint's semantics.
func (s *Server) ohlcSeriesWithAliases(
	ctx context.Context,
	pair canonical.Pair,
	interval ohlcInterval,
	from, to time.Time,
	limit int,
) ([]OHLCSeriesBar, error) {
	// Fiat-denominated quotes (fiat:USD, fiat:EUR, …) have no deep
	// trade stream of their own — the multi-year history lives under the
	// USD/EUR-pegged stablecoin pairs. Combine those constituents per
	// bucket (matching the live aggregator's VWAP source set) instead of
	// first-hit, which would serve only the recent direct CEX feed.
	if pair.Quote.Type == canonical.AssetFiat {
		return s.ohlcSeriesFiatCombined(ctx, pair, interval, from, to, limit)
	}
	for _, a := range assetAliases(pair.Base) {
		for _, q := range assetAliases(pair.Quote) {
			ap, perr := canonical.NewPair(a, q)
			if perr != nil {
				continue // degenerate alias combination (identity pair)
			}
			bars, err := s.history.OHLCSeries(ctx, ap, string(interval), from, to, limit)
			if err != nil || len(bars) > 0 {
				return bars, err
			}
		}
	}
	return nil, nil
}
