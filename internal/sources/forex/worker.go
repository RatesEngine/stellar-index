package forex

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Worker periodically fetches the upstream rates + names and
// installs the result into a [Cache]. Designed to run as a
// goroutine for the lifetime of the API process.
type Worker struct {
	client      *Client
	cache       *Cache
	logger      *slog.Logger
	interval    time.Duration
	circulation map[string]CirculationEntry // loaded once at startup
}

// NewWorker constructs the worker. interval is the refresh
// cadence — Massive's hourly grain means anything < 15 min is
// wasted fetches; 1h is a reasonable default that keeps the
// cache fresh across operator restarts.
//
// The curated monetary-base CSV is loaded once at construction
// (lives in internal/sources/forex/circulation_data.csv). Parse
// errors per row are non-fatal: rows that parse install, the
// rest are logged as a warning. The map is then attached to
// every snapshot built by refreshOnce.
func NewWorker(client *Client, cache *Cache, logger *slog.Logger, interval time.Duration) *Worker {
	if interval <= 0 {
		interval = time.Hour
	}
	circulation, err := loadCirculationTable()
	if err != nil {
		logger.Warn("forex: circulation csv parsed with skipped rows", "err", err)
	}
	logger.Info("forex: circulation table loaded", "entries", len(circulation))
	return &Worker{
		client:      client,
		cache:       cache,
		logger:      logger,
		interval:    interval,
		circulation: circulation,
	}
}

// Run blocks until ctx is cancelled. Fetches once immediately so
// the cache is populated before the first /v1/currencies request
// (subject to the upstream's response time), then refreshes every
// interval. Failures are logged but never crash the worker — the
// cache holds the prior snapshot until a refresh succeeds.
func (w *Worker) Run(ctx context.Context) error {
	w.refreshOnce(ctx)

	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			w.refreshOnce(ctx)
		}
	}
}

// refreshOnce performs a single fetch+install cycle. Errors get
// logged at warn level (not error — a stale cache is degraded
// service, not a crash condition).
func (w *Worker) refreshOnce(ctx context.Context) {
	rates, publishedAt, err := w.client.LatestUSDRates(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Warn("forex: rates fetch failed", "err", err)
		return
	}
	names, err := w.client.CurrencyNames(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Warn("forex: names fetch failed", "err", err)
		return
	}

	// Carry forward the prior snapshot's history while we backfill.
	// The first install on a fresh worker has no history yet — the
	// per-currency page renders the sparkline panel as "—" until a
	// later refresh fills it in. We re-fetch history once a day
	// (cheap on the upstream's CDN — 7 dated URLs, all cached).
	var history map[string][]HistoryPoint
	if prev := w.cache.Latest(); prev != nil {
		history = prev.History7d
	}
	if w.shouldRefreshHistory(history, publishedAt) {
		history = w.fetchHistory(ctx, names, publishedAt)
	}

	snap := buildSnapshot(rates, names, publishedAt, time.Now().UTC(), history, w.circulation)
	w.cache.Set(snap)
	w.logger.Info("forex: snapshot installed",
		"currencies", len(snap.Currencies),
		"history_currencies", len(snap.History7d),
		"published_at", publishedAt,
	)
}

// shouldRefreshHistory returns true when the worker should re-pull
// the 7-day historical series. Fires on first install (history nil
// or empty) and once per day thereafter (the published_at date
// rolling forward indicates the upstream snapshot rolled too).
func (w *Worker) shouldRefreshHistory(prevHistory map[string][]HistoryPoint, publishedAt time.Time) bool {
	if len(prevHistory) == 0 {
		return true
	}
	// Sample any one ticker's most-recent date — they all share the
	// same upstream date roll.
	for _, points := range prevHistory {
		if len(points) == 0 {
			continue
		}
		newest := points[len(points)-1].Date
		return newest.Before(publishedAt.Truncate(24 * time.Hour))
	}
	return true
}

// fetchHistory pulls the trailing-7d daily snapshots from the
// upstream and assembles a per-ticker series. Days that 404 (e.g.
// weekends for some tickers) are skipped silently — the caller
// gets a series of length ≤ 7 for each ticker.
func (w *Worker) fetchHistory(ctx context.Context, names map[string]string, latest time.Time) map[string][]HistoryPoint {
	if latest.IsZero() {
		latest = time.Now().UTC()
	}
	const window = 7
	out := map[string][]HistoryPoint{}
	// Walk oldest → newest so out[ticker] is sorted ascending.
	for i := window - 1; i >= 0; i-- {
		date := latest.AddDate(0, 0, -i).UTC()
		dateStr := date.Format("2006-01-02")
		rates, _, err := w.client.HistoricalUSDRates(ctx, dateStr)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return out
			}
			w.logger.Debug("forex: historical fetch missed",
				"date", dateStr, "err", err)
			continue
		}
		for code, rate := range rates {
			if _, named := names[code]; !named {
				continue
			}
			if rate <= 0 || !isFiniteFloat(rate) {
				continue
			}
			ticker := upper(code)
			out[ticker] = append(out[ticker], HistoryPoint{
				Date:    date,
				RateUSD: rate,
			})
		}
	}
	return out
}

// upper is local to avoid pulling strings into the worker file.
func upper(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
