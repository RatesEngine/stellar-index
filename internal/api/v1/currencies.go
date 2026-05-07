package v1

import (
	"net/http"
	"strconv"
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

// CurrenciesSnapshot is the v1-side projection of the forex cache.
// Mirrors forex.Snapshot field-for-field; defined here so the
// binding adapter in cmd/ratesengine-api can convert without this
// package importing the source package.
type CurrenciesSnapshot struct {
	Currencies  []CurrencyEntry
	PublishedAt time.Time
	FetchedAt   time.Time
}

// CurrencyEntry is one wire-shape currency row.
type CurrencyEntry struct {
	Ticker      string    `json:"ticker"`
	Name        string    `json:"name"`
	RateUSD     float64   `json:"rate_usd"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
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
func (s *Server) handleCurrencies(w http.ResponseWriter, r *http.Request) {
	reader := s.currencies
	if reader == nil {
		writeJSON(w, CurrenciesPayload{Source: "currency-api"}, Flags{})
		return
	}
	snap := reader.Latest()
	if snap == nil {
		writeJSON(w, CurrenciesPayload{Source: "currency-api"}, Flags{})
		return
	}
	rows := snap.Currencies
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n < len(rows) {
			rows = rows[:n]
		}
	}
	writeJSON(w, CurrenciesPayload{
		Currencies:  rows,
		PublishedAt: snap.PublishedAt,
		FetchedAt:   snap.FetchedAt,
		Source:      "currency-api",
	}, Flags{})
}
