package forex

import (
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Currency is one entry in the snapshot — ticker, display name,
// and price-of-1-USD-in-this-currency. Stable across the lifetime
// of a snapshot; the Cache replaces snapshots atomically.
type Currency struct {
	Ticker   string  // upper-case ISO-4217 (USD, EUR, JPY, …)
	Name     string  // display name ("United States Dollar")
	RateUSD  float64 // 1 USD = N units of this currency
	UpdateAt time.Time
}

// Snapshot is the immutable rates+metadata bundle the Cache holds.
// Replaced atomically by the worker; readers always see a
// consistent view (no torn reads).
//
// History7d is the per-ticker daily series: 7 entries (oldest →
// newest) showing the price of 1 USD in that currency on each day.
// Empty when the worker hasn't completed its history backfill yet
// (or when the upstream rejected the historical fetches —
// currency-api occasionally 404s for weekends / holidays for
// less-tracked tickers).
type Snapshot struct {
	Currencies  []Currency
	PublishedAt time.Time
	FetchedAt   time.Time
	History7d   map[string][]HistoryPoint
}

// HistoryPoint is one daily rate datum for the 7d series.
type HistoryPoint struct {
	Date    time.Time // YYYY-MM-DD UTC
	RateUSD float64   // 1 USD = N units on that date
}

// Cache holds the latest forex snapshot. Safe for concurrent use;
// readers go through atomic.Pointer so there's no lock contention
// on the hot read path. Initial state is nil — readers MUST handle
// the no-snapshot-yet case (typically: 503 or "warming up" UX).
type Cache struct {
	snapshot atomic.Pointer[Snapshot]
}

// NewCache returns an empty Cache. The worker populates it on
// first successful fetch.
func NewCache() *Cache {
	return &Cache{}
}

// Latest returns the most recent snapshot, or nil if no fetch has
// completed yet. Callers MUST nil-check.
func (c *Cache) Latest() *Snapshot {
	return c.snapshot.Load()
}

// Set installs a new snapshot atomically. Old snapshot is GC-able
// once no readers retain it.
func (c *Cache) Set(s *Snapshot) {
	c.snapshot.Store(s)
}

// buildSnapshot merges a raw rates map + names map into the public
// Currency slice. Excludes USD itself from the per-currency rate
// list (rate=1.0 is implied), excludes any code without a name,
// and sorts alphabetically by ticker for deterministic output.
//
// `history` is the per-ticker 7d series the worker assembled — pass
// nil if no historical backfill has run; the snapshot still
// installs cleanly with an empty History7d map.
func buildSnapshot(rates map[string]float64, names map[string]string, publishedAt, fetchedAt time.Time, history map[string][]HistoryPoint) *Snapshot {
	out := make([]Currency, 0, len(rates)+1)
	// Always-include USD as the base. Rate is 1.0 by definition;
	// the name comes from the names map (fallback to "US Dollar").
	usdName := names["usd"]
	if usdName == "" {
		usdName = "US Dollar"
	}
	out = append(out, Currency{
		Ticker:   "USD",
		Name:     toTitle(usdName),
		RateUSD:  1.0,
		UpdateAt: publishedAt,
	})
	for code, rate := range rates {
		if rate <= 0 || !isFiniteFloat(rate) {
			continue
		}
		name := names[code]
		if name == "" {
			continue
		}
		ticker := strings.ToUpper(code)
		if ticker == "USD" {
			continue
		}
		out = append(out, Currency{
			Ticker:   ticker,
			Name:     toTitle(name),
			RateUSD:  rate,
			UpdateAt: publishedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ticker < out[j].Ticker })
	if history == nil {
		history = map[string][]HistoryPoint{}
	}
	return &Snapshot{
		Currencies:  out,
		PublishedAt: publishedAt,
		FetchedAt:   fetchedAt,
		History7d:   history,
	}
}

func isFiniteFloat(f float64) bool {
	return f == f && f != f+1 // NaN check + crude inf check
}

// toTitle returns "United States Dollar" from "united states dollar".
// Upstream names are typically already title-cased but normalising
// keeps the output deterministic across snapshots.
func toTitle(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		if p == "" {
			continue
		}
		if len(p) <= 2 {
			parts[i] = strings.ToUpper(p)
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, " ")
}
