package v1

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CurrenciesReader is the seam for /v1/currencies. The forex
// package's *Cache implements via Latest. Defined as an interface
// so this package doesn't import internal/sources/forex (the
// dependency goes the other way at the binary).
type CurrenciesReader interface {
	// Latest returns the most recent forex snapshot, or nil if no
	// fetch has completed yet (warming up).
	Latest() *CurrenciesSnapshot
}

// FXHistoryReader serves long-form persisted history from the
// fx_quotes hypertable. Distinct from the in-memory History7d on
// the snapshot, which is bounded to one week. Optional — when nil
// the handler simply omits the long-form history field.
type FXHistoryReader interface {
	ListFXHistory(ctx context.Context, ticker string, from, to time.Time) ([]FXQuotePoint, error)
}

// FXQuotePoint is the storage-layer-projected history datum.
// Mirrors timescale.FXQuote field-for-field with the date axis as
// `Bucket`. Wire shape uses CurrencyHistoryPoint downstream.
type FXQuotePoint struct {
	Bucket     time.Time
	RateUSD    float64
	InverseUSD float64
}

// CurrenciesSnapshot is the v1-side projection of the forex cache.
// Mirrors forex.Snapshot field-for-field; defined here so the
// binding adapter in cmd/ratesengine-api can convert without this
// package importing the source package.
type CurrenciesSnapshot struct {
	Currencies  []CurrencyEntry
	PublishedAt time.Time
	FetchedAt   time.Time
	History7d   map[string][]CurrencyHistoryRaw
}

// CurrencyHistoryRaw is the per-ticker daily series the adapter
// passes through. Date is UTC; RateUSD is "1 USD = N units of
// ticker". Inverse is computed in the handler so the wire shape
// stays minimal.
type CurrencyHistoryRaw struct {
	Date    time.Time
	RateUSD float64
}

// CurrencyEntry is one wire-shape currency row.
//
// Change7dPct is the percent change in 1-USD-in-this-currency over
// the 7d window: ((today - 7d-ago) / 7d-ago) × 100. Pointer +
// omitempty so callers can distinguish "we don't have history yet"
// (cold cache) from "rate hasn't moved" (0.0). Computed server-side
// so every consumer agrees on the math.
//
// History7dRates is the per-day inverse-rate series (1 unit of
// ticker in USD). Populated only when the request includes
// `sparkline` (e.g. `?include=sparkline`); otherwise omitted to
// keep the default list payload lean.
type CurrencyEntry struct {
	Ticker  string  `json:"ticker"`
	Name    string  `json:"name"`
	RateUSD float64 `json:"rate_usd"`
	// Change24hPct is yesterday-to-today % change in
	// 1-unit-in-USD terms — daily-grain feed so the resolution is
	// "previous publish" vs "latest publish". Pointer +
	// omitempty so callers distinguish "no prior day" (cold cache)
	// from "rate hasn't moved" (0.0).
	Change24hPct   *float64  `json:"change_24h_pct,omitempty"`
	Change7dPct    *float64  `json:"change_7d_pct,omitempty"`
	History7dRates []float64 `json:"history_7d_rates,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
	// CirculatingSupply is the central-bank monetary aggregate
	// (typically M2) in the currency's natural local unit.
	// Sourced from the curated CSV in
	// internal/sources/forex/circulation_data.csv. Null for
	// currencies not present in the table.
	CirculatingSupply *float64 `json:"circulating_supply,omitempty"`
	// MarketCapUSD is CirculatingSupply / RateUSD — the
	// USD-denominated value of the monetary aggregate. Same
	// nullability semantics as CirculatingSupply.
	MarketCapUSD *float64 `json:"market_cap_usd,omitempty"`
	// CirculationAsOf is the date the central bank stamped on
	// the underlying release (RFC-3339 date). Drives "as of YYYY-MM"
	// provenance on the UI.
	CirculationAsOf string `json:"circulation_as_of,omitempty"`
	// CirculationSource is the central-bank series identifier
	// (e.g. "FRED:M2SL", "ECB:BSI.M2") that produced the row.
	CirculationSource string `json:"circulation_source,omitempty"`
}

// CurrenciesPayload is the wire envelope for /v1/currencies.
//
// PublishedAt is the date the upstream stamped on the rates set
// (currency-api updates daily; this lets clients display "rates
// from 2026-05-07" rather than guessing freshness from the
// envelope's as_of). FetchedAt is when this binary last pulled the
// snapshot.
type CurrenciesPayload struct {
	Currencies  []CurrencyEntry `json:"currencies"`
	PublishedAt time.Time       `json:"published_at,omitempty"`
	FetchedAt   time.Time       `json:"fetched_at,omitempty"`
	Source      string          `json:"source"`
}

// handleCurrencies serves GET /v1/currencies.
//
// Returns the latest USD-base rates snapshot. 200 + empty
// currencies list when the cache hasn't completed its first fetch
// yet ("warming up"); the wire shape stays consistent so clients
// don't need separate empty / error code paths. The
// fetched_at / published_at timestamps make staleness explicit.
//
// Query params:
//   - limit (optional): cap the returned currencies. Useful for
//     home-page strips. Default = no cap (~200 rows).
//   - include (optional): comma-separated; `sparkline` attaches the
//     per-ticker 7d series of inverse-USD rates so listings can
//     render mini charts without a follow-up per-ticker fetch.
func (s *Server) handleCurrencies(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo // include parsing + per-row enrich + limit clamp are linear; splitting would scatter the request lifecycle
	reader := s.currencies
	if reader == nil {
		writeJSON(w, CurrenciesPayload{Source: "massive"}, Flags{})
		return
	}
	snap := reader.Latest()
	if snap == nil {
		writeJSON(w, CurrenciesPayload{Source: "massive"}, Flags{})
		return
	}

	includeSparkline := false
	for _, f := range strings.Split(r.URL.Query().Get("include"), ",") {
		if strings.TrimSpace(f) == "sparkline" {
			includeSparkline = true
		}
	}

	// Enrich every entry with its 7d change + optional sparkline.
	// Both are derived from the History7d map the worker populates.
	enriched := make([]CurrencyEntry, len(snap.Currencies))
	for i, c := range snap.Currencies {
		enriched[i] = c
		hist := snap.History7d[c.Ticker]
		if len(hist) >= 2 {
			first := hist[0].RateUSD
			last := hist[len(hist)-1].RateUSD
			// change_7d_pct is in inverse-USD terms (the "1 unit of
			// ticker = $X" axis users care about). RateUSD is "1 USD
			// = N units"; flip both ends before dividing.
			if first > 0 && last > 0 {
				firstInv := 1.0 / first
				lastInv := 1.0 / last
				if firstInv > 0 {
					change := ((lastInv - firstInv) / firstInv) * 100
					enriched[i].Change7dPct = &change
				}
			}
			// change_24h_pct uses the most-recent two daily samples.
			// Same inverse-USD basis as change_7d_pct so the two
			// percentages share a sign convention (positive = ticker
			// strengthened against USD).
			yest := hist[len(hist)-2].RateUSD
			today := last
			if yest > 0 && today > 0 {
				yestInv := 1.0 / yest
				todayInv := 1.0 / today
				if yestInv > 0 {
					change24 := ((todayInv - yestInv) / yestInv) * 100
					enriched[i].Change24hPct = &change24
				}
			}
		}
		if includeSparkline && len(hist) > 0 {
			out := make([]float64, len(hist))
			for j, p := range hist {
				if p.RateUSD > 0 {
					out[j] = 1.0 / p.RateUSD
				}
			}
			enriched[i].History7dRates = out
		}
	}

	if raw := r.URL.Query().Get("limit"); raw != "" {
		// Validate strictly — the rest of the v1 surface (/v1/coins,
		// /v1/markets, /v1/pools) returns 400 invalid-limit on bad
		// input. /v1/currencies used to silently ignore it (zero,
		// negative, garbage all returned the full list). Bringing
		// the contract in line so a typo doesn't masquerade as
		// success. Cap at 500 to match the other listings.
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-limit",
				"Invalid limit", http.StatusBadRequest,
				"limit must be an integer in [1, 500]")
			return
		}
		if n < len(enriched) {
			enriched = enriched[:n]
		}
	}
	writeJSON(w, CurrenciesPayload{
		Currencies:  enriched,
		PublishedAt: snap.PublishedAt,
		FetchedAt:   snap.FetchedAt,
		Source:      "massive",
	}, Flags{})
}

// CurrencyDetail is the wire shape for /v1/currencies/{ticker}.
//
// CrossRates is the per-target-currency rate (1 unit of `ticker`
// in the target currency). Derived from the USD-base snapshot:
// rate(A→B) = rate(USD→B) / rate(USD→A). Useful for the
// converter widget on the explorer's per-currency page (no need
// to call the API for every pair the user might compare against).
//
// History7d is the last 7 daily snapshots — empty when the worker
// hasn't completed its history backfill yet (or upstream rejected
// the fetches for less-tracked tickers).
type CurrencyDetail struct {
	Ticker     string             `json:"ticker"`
	Name       string             `json:"name"`
	RateUSD    float64            `json:"rate_usd"`
	InverseUSD float64            `json:"inverse_usd"` // 1 unit of ticker = $X
	CrossRates map[string]float64 `json:"cross_rates"`
	// Change24hPct / Change7dPct are inverse-USD percent moves
	// (positive = ticker strengthened against USD), same basis as
	// the listing endpoint. Pointer + omitempty so callers can
	// distinguish "no prior history" (cold cache) from "rate
	// hasn't moved" (0.0). Resolution is daily-grain via the
	// currency-api shim.
	Change24hPct *float64               `json:"change_24h_pct,omitempty"`
	Change7dPct  *float64               `json:"change_7d_pct,omitempty"`
	History7d    []CurrencyHistoryPoint `json:"history_7d,omitempty"`
	// HistoryRange is the time window the History field covers, in
	// the request's `range` syntax: "30d", "1y", "all". Empty when
	// only the in-memory History7d is populated (e.g. when the
	// fx_quotes reader is nil or returned no rows).
	HistoryRange string `json:"history_range,omitempty"`
	// History is the long-form persisted daily series from
	// fx_quotes — a superset of History7d when available. Frontend
	// charts render from History when present, falling back to
	// History7d for the warming-up case.
	History []CurrencyHistoryPoint `json:"history,omitempty"`
	// Same circulation fields as the listing's CurrencyEntry.
	// Sourced from the curated CSV at
	// internal/sources/forex/circulation_data.csv (joined into the
	// snapshot by the forex worker).
	CirculatingSupply *float64  `json:"circulating_supply,omitempty"`
	MarketCapUSD      *float64  `json:"market_cap_usd,omitempty"`
	CirculationAsOf   string    `json:"circulation_as_of,omitempty"`
	CirculationSource string    `json:"circulation_source,omitempty"`
	PublishedAt       time.Time `json:"published_at,omitempty"`
	FetchedAt         time.Time `json:"fetched_at,omitempty"`
	Source            string    `json:"source"`
}

// CurrencyHistoryPoint is one daily rate datum.
type CurrencyHistoryPoint struct {
	Date       time.Time `json:"date"`
	RateUSD    float64   `json:"rate_usd"`    // 1 USD = N units of ticker
	InverseUSD float64   `json:"inverse_usd"` // 1 unit of ticker = $X
}

// handleCurrencyDetail serves GET /v1/currencies/{ticker}.
//
// 404 if the ticker isn't in the snapshot (typo / not covered by
// the upstream feed). 200 + warming-up payload if the cache is
// empty.
func (s *Server) handleCurrencyDetail(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo,funlen // input validation + history projection + change derivation are linear; splitting would scatter the request lifecycle
	ticker := strings.ToUpper(strings.TrimSpace(r.PathValue("ticker")))
	if ticker == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-ticker",
			"Missing ticker", http.StatusBadRequest,
			"path must be /v1/currencies/{ticker} (e.g. /v1/currencies/EUR)")
		return
	}
	reader := s.currencies
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/currencies-unavailable",
			"Currencies feature not configured", http.StatusServiceUnavailable, "")
		return
	}
	snap := reader.Latest()
	if snap == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/currencies-warming",
			"Currencies cache is warming up", http.StatusServiceUnavailable,
			"the forex worker hasn't completed its first fetch yet — retry in a few seconds")
		return
	}

	// Find the requested currency. Linear scan over ~200 entries
	// is microseconds; not worth indexing.
	var target *CurrencyEntry
	for i := range snap.Currencies {
		if snap.Currencies[i].Ticker == ticker {
			target = &snap.Currencies[i]
			break
		}
	}
	if target == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/currency-not-found",
			"Currency not found", http.StatusNotFound,
			"ticker "+ticker+" is not in the current rates snapshot — check /v1/currencies for the supported list")
		return
	}

	// Build cross-rates: rate(target→other) = rate(USD→other) / rate(USD→target).
	// Skip the self-rate (always 1.0) for compactness; clients can
	// trivially synthesise it.
	cross := make(map[string]float64, len(snap.Currencies)-1)
	for _, c := range snap.Currencies {
		if c.Ticker == target.Ticker {
			continue
		}
		if target.RateUSD <= 0 {
			continue
		}
		cross[c.Ticker] = c.RateUSD / target.RateUSD
	}

	inverse := 0.0
	if target.RateUSD > 0 {
		inverse = 1.0 / target.RateUSD
	}

	// Project the per-ticker historical series, computing the
	// inverse alongside so the frontend can render either axis
	// without re-doing the maths per row.
	var history []CurrencyHistoryPoint
	rawHist, hasHist := snap.History7d[target.Ticker]
	if hasHist {
		history = make([]CurrencyHistoryPoint, 0, len(rawHist))
		for _, p := range rawHist {
			inv := 0.0
			if p.RateUSD > 0 {
				inv = 1.0 / p.RateUSD
			}
			history = append(history, CurrencyHistoryPoint{
				Date:       p.Date,
				RateUSD:    p.RateUSD,
				InverseUSD: inv,
			})
		}
	}

	// Compute change_24h_pct and change_7d_pct from the same
	// inverse-USD basis the listing handler uses. Both pointers
	// stay nil when there's not enough history to derive them.
	var change24, change7 *float64
	if len(rawHist) >= 2 {
		first := rawHist[0].RateUSD
		last := rawHist[len(rawHist)-1].RateUSD
		yest := rawHist[len(rawHist)-2].RateUSD
		if first > 0 && last > 0 {
			firstInv, lastInv := 1.0/first, 1.0/last
			if firstInv > 0 {
				v := ((lastInv - firstInv) / firstInv) * 100
				change7 = &v
			}
		}
		if yest > 0 && last > 0 {
			yestInv, lastInv := 1.0/yest, 1.0/last
			if yestInv > 0 {
				v := ((lastInv - yestInv) / yestInv) * 100
				change24 = &v
			}
		}
	}

	// Long-form history (fx_quotes) — only attempt when the reader
	// is wired AND the request explicitly asked. Default behaviour
	// stays unchanged for back-compat.
	//
	// 8s ceiling on the fx_quotes scan: `range=all` spans 11 years
	// and a cold-cache hypertable scan can run several seconds. The
	// in-memory History7d series is already populated by this point,
	// so a deadline-exceed soft-fails into "ship the 7d series"
	// rather than blocking the whole detail response. Same pattern
	// as the cold-path guards in #1082, #1099-#1106.
	var longRange string
	var longHistory []CurrencyHistoryPoint
	rangeRaw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if s.fxHistory != nil && rangeRaw != "" {
		from, to, label, ok := parseFXHistoryRange(rangeRaw, snap.PublishedAt)
		if ok {
			fxCtx, fxCancel := context.WithTimeout(r.Context(), 8*time.Second)
			points, err := s.fxHistory.ListFXHistory(fxCtx, target.Ticker, from, to)
			fxCancel()
			switch {
			case err == nil && len(points) > 0:
				longHistory = make([]CurrencyHistoryPoint, len(points))
				for i, p := range points {
					longHistory[i] = CurrencyHistoryPoint{
						Date:       p.Bucket,
						RateUSD:    p.RateUSD,
						InverseUSD: p.InverseUSD,
					}
				}
				longRange = label
			case err != nil:
				// Soft-fail: warn for operators, fall through to the
				// in-memory History7d already projected above. We don't
				// 503 here because the rest of the response is fully
				// populated — a missing long-form history is a
				// degradation, not a complete failure.
				s.logger.Warn("fx_quotes range fetch", "ticker", target.Ticker, "range", label, "err", err)
			}
		}
	}

	writeJSON(w, CurrencyDetail{
		Ticker:            target.Ticker,
		Name:              target.Name,
		RateUSD:           target.RateUSD,
		InverseUSD:        inverse,
		CrossRates:        cross,
		Change24hPct:      change24,
		Change7dPct:       change7,
		History7d:         history,
		HistoryRange:      longRange,
		History:           longHistory,
		CirculatingSupply: target.CirculatingSupply,
		MarketCapUSD:      target.MarketCapUSD,
		CirculationAsOf:   target.CirculationAsOf,
		CirculationSource: target.CirculationSource,
		PublishedAt:       snap.PublishedAt,
		FetchedAt:         snap.FetchedAt,
		Source:            "massive",
	}, Flags{})
}

// parseFXHistoryRange converts a user-facing range token into a
// [from, to] window anchored on the snapshot's publish date. The
// label echoes the canonical form so the response surfaces what
// was served (e.g. "1y" for an alias like "365d").
func parseFXHistoryRange(raw string, anchor time.Time) (from, to time.Time, label string, ok bool) {
	if anchor.IsZero() {
		anchor = time.Now().UTC()
	}
	to = anchor.UTC().Truncate(24 * time.Hour)
	switch raw {
	case "all", "max":
		// Fixed-floor sentinel; fx_quotes can't have rows older than
		// the worker's first persistent write, but the Massive backfill
		// script can populate up to ~10y. 11y window is generous.
		from = to.AddDate(-11, 0, 0)
		return from, to, "all", true
	case "30d":
		from = to.AddDate(0, 0, -30)
		return from, to, "30d", true
	case "90d", "3m":
		from = to.AddDate(0, 0, -90)
		return from, to, "90d", true
	case "180d", "6m":
		from = to.AddDate(0, 0, -180)
		return from, to, "180d", true
	case "1y", "12m", "365d":
		from = to.AddDate(-1, 0, 0)
		return from, to, "1y", true
	case "5y":
		from = to.AddDate(-5, 0, 0)
		return from, to, "5y", true
	case "10y":
		from = to.AddDate(-10, 0, 0)
		return from, to, "10y", true
	}
	return time.Time{}, time.Time{}, "", false
}
