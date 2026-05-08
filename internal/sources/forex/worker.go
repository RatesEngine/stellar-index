package forex

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// FXQuoteWriter is the write seam between the worker and persistent
// storage. nil-able — when nil the worker still functions (cache-only
// mode, useful in tests or pre-migration deploys).
type FXQuoteWriter interface {
	InsertFXQuoteBatch(ctx context.Context, quotes []FXQuote) error
}

// FXQuote is the storage-layer record the worker writes per refresh.
// Mirrors the timescale.FXQuote shape but lives in this package so
// the forex worker doesn't import internal/storage/timescale (which
// imports forex types via the v1 API package; the dependency would
// cycle).
type FXQuote struct {
	Bucket     time.Time
	Ticker     string
	RateUSD    float64
	InverseUSD float64
	Source     string
}

// Worker periodically fetches the upstream rates + names and
// installs the result into a [Cache]. Designed to run as a
// goroutine for the lifetime of the API process.
type Worker struct {
	client      *Client
	cache       *Cache
	writer      FXQuoteWriter
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

// WithWriter attaches a persistent quote writer. When set, every
// successful refreshOnce also persists the latest rates + history
// to the fx_quotes hypertable. nil writer keeps the worker in
// cache-only mode (the pre-fx_quotes behaviour).
func (w *Worker) WithWriter(writer FXQuoteWriter) *Worker {
	w.writer = writer
	return w
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

	w.persistSnapshot(ctx, snap)
}

// persistSnapshot writes the latest rates + history to fx_quotes if
// a writer is attached. Safe to call with a nil writer (no-op).
//
// Two writes happen:
//  1. Today's row per ticker, from `snap.Currencies` — the canonical
//     "current" rate. Re-running upserts on the (ticker, today) PK so
//     repeated refreshes within the same day idempotently update the
//     row.
//  2. Trailing-7d rows from `snap.History7d` — these only differ on
//     the first install of each day (the worker's gap-detector
//     short-circuits unchanged history).
//
// Errors get logged at warn level; persistence is best-effort
// alongside the in-memory cache, never a crash condition.
func (w *Worker) persistSnapshot(ctx context.Context, snap *Snapshot) {
	if w.writer == nil || snap == nil {
		return
	}
	const sourceTag = "massive"
	today := snap.PublishedAt.UTC().Truncate(24 * time.Hour)

	batch := make([]FXQuote, 0, len(snap.Currencies)+len(snap.History7d)*7)
	for _, c := range snap.Currencies {
		if c.RateUSD <= 0 {
			continue
		}
		batch = append(batch, FXQuote{
			Bucket:     today,
			Ticker:     c.Ticker,
			RateUSD:    c.RateUSD,
			InverseUSD: 1.0 / c.RateUSD,
			Source:     sourceTag,
		})
	}
	for ticker, points := range snap.History7d {
		for _, p := range points {
			if p.RateUSD <= 0 {
				continue
			}
			batch = append(batch, FXQuote{
				Bucket:     p.Date.UTC().Truncate(24 * time.Hour),
				Ticker:     ticker,
				RateUSD:    p.RateUSD,
				InverseUSD: 1.0 / p.RateUSD,
				Source:     sourceTag,
			})
		}
	}

	if err := w.writer.InsertFXQuoteBatch(ctx, batch); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		w.logger.Warn("forex: fx_quotes persist failed",
			"rows", len(batch), "err", err)
		return
	}
	w.logger.Info("forex: fx_quotes persisted", "rows", len(batch))
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
