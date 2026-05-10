// scripts/ops/fx-history-backfill — one-off historical FX backfill.
//
// Walks Massive's grouped-daily endpoint day-by-day for an arbitrary
// window (defaults: trailing 10y) and writes rows into the
// fx_quotes hypertable. Idempotent on the (ticker, bucket) primary
// key — re-running on the same range upserts identical values.
//
// Why a separate binary not a worker hook: Massive bills per
// historical request and this fetches ~3,650 days × N currencies of
// data. Doing it as a one-shot lets the operator schedule it
// (cron / weekend run) and observe progress in the script's own
// stderr stream rather than burying it in the API server's logs.
//
// Usage:
//
//	export MASSIVE_API_KEY=...
//	export DATABASE_URL=postgres://...
//	go run ./scripts/ops/fx-history-backfill \
//	    --years=10 --concurrency=4
//
// Flags:
//
//	--years=N              window depth (default 10)
//	--from=YYYY-MM-DD      override window start (overrides --years)
//	--to=YYYY-MM-DD        override window end (default today UTC)
//	--concurrency=N        parallel daily fetches (default 4 — Massive
//	                       rate-limits ~5 req/s for paid tier)
//	--dry-run              fetch + print row counts but skip the DB write
//	--ticker=USD,EUR,...   restrict to a subset (default: all from
//	                       the upstream's response)
//
// The script logs one line per day (date, n_rows, status) to stderr
// so progress is visible. Final summary writes total days, total
// rows, elapsed time.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RatesEngine/rates-engine/internal/sources/forex"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

func main() {
	cfg, logger := parseFlags()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := forex.NewClient(cfg.apiKey)

	var store *timescale.Store
	if !cfg.dryRun {
		var err error
		store, err = timescale.Open(ctx, cfg.dsn)
		if err != nil {
			logger.Error("open timescale", "err", err)
			os.Exit(1)
		}
		defer func() { _ = store.Close() }()
	}

	logger.Info("fx-history-backfill: start",
		"from", cfg.from.Format("2006-01-02"),
		"to", cfg.to.Format("2006-01-02"),
		"concurrency", cfg.concurrency,
		"dry_run", cfg.dryRun)

	totalDays := int(cfg.to.Sub(cfg.from).Hours()/24) + 1
	daysOK, daysErr, totalRows, elapsed := runBackfill(ctx, logger, client, store, cfg)

	logger.Info("fx-history-backfill: done",
		"days_total", totalDays,
		"days_ok", daysOK,
		"days_err", daysErr,
		"rows", totalRows,
		"elapsed", elapsed)

	if daysErr > 0 {
		os.Exit(2) // partial success
	}
}

type backfillConfig struct {
	apiKey       string
	dsn          string
	from         time.Time
	to           time.Time
	concurrency  int
	dryRun       bool
	tickerFilter map[string]struct{}
}

// parseFlags pulls all CLI + env config and validates it. Splits
// off the main flow so cognitive-complexity stays manageable.
// Exits the process on validation failure.
func parseFlags() (backfillConfig, *slog.Logger) {
	var (
		years       int
		fromStr     string
		toStr       string
		concurrency int
		dryRun      bool
		tickerCSV   string
	)
	flag.IntVar(&years, "years", 10, "trailing window depth in years (default 10)")
	flag.StringVar(&fromStr, "from", "", "window start date YYYY-MM-DD (overrides --years)")
	flag.StringVar(&toStr, "to", "", "window end date YYYY-MM-DD (default: today UTC)")
	flag.IntVar(&concurrency, "concurrency", 4, "parallel daily fetches (default 4)")
	flag.BoolVar(&dryRun, "dry-run", false, "skip DB writes; report row counts only")
	flag.StringVar(&tickerCSV, "ticker", "", "comma-separated ticker subset (default: all)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	apiKey := os.Getenv("MASSIVE_API_KEY")
	if apiKey == "" {
		logger.Error("MASSIVE_API_KEY environment variable is required")
		os.Exit(1)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" && !dryRun {
		logger.Error("DATABASE_URL environment variable is required (use --dry-run to skip the DB)")
		os.Exit(1)
	}

	to := time.Now().UTC().Truncate(24 * time.Hour)
	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err != nil {
			logger.Error("invalid --to", "err", err)
			os.Exit(1)
		}
		to = t.UTC()
	}
	from := to.AddDate(-years, 0, 0)
	if fromStr != "" {
		f, err := time.Parse("2006-01-02", fromStr)
		if err != nil {
			logger.Error("invalid --from", "err", err)
			os.Exit(1)
		}
		from = f.UTC()
	}
	if !from.Before(to) {
		logger.Error("--from must be before --to", "from", from, "to", to)
		os.Exit(1)
	}

	tickerFilter := map[string]struct{}{}
	if tickerCSV != "" {
		for _, t := range strings.Split(tickerCSV, ",") {
			tickerFilter[strings.ToUpper(strings.TrimSpace(t))] = struct{}{}
		}
	}

	return backfillConfig{
		apiKey:       apiKey,
		dsn:          dsn,
		from:         from,
		to:           to,
		concurrency:  concurrency,
		dryRun:       dryRun,
		tickerFilter: tickerFilter,
	}, logger
}

// runBackfill fans out per-day fetches across `cfg.concurrency`
// workers and aggregates results. Returns (daysOK, daysErr,
// totalRows, elapsed). ctx-cancel during the dispatch loop stops
// new launches but lets in-flight workers drain.
func runBackfill(
	ctx context.Context,
	logger *slog.Logger,
	client *forex.Client,
	store *timescale.Store,
	cfg backfillConfig,
) (daysOK, daysErr, totalRows int, elapsed time.Duration) {
	dates := make([]time.Time, 0)
	for d := cfg.from; !d.After(cfg.to); d = d.AddDate(0, 0, 1) {
		dates = append(dates, d)
	}

	type result struct {
		date time.Time
		rows int
		err  error
	}

	sem := make(chan struct{}, cfg.concurrency)
	var wg sync.WaitGroup
	results := make(chan result, len(dates))

	started := time.Now()
dispatch:
	for _, date := range dates {
		select {
		case <-ctx.Done():
			break dispatch
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(d time.Time) {
			defer wg.Done()
			defer func() { <-sem }()
			rows, err := fetchAndPersist(ctx, client, store, d, cfg.tickerFilter, cfg.dryRun)
			results <- result{date: d, rows: rows, err: err}
		}(date)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			if errors.Is(r.err, context.Canceled) {
				continue
			}
			daysErr++
			logger.Warn("day failed",
				"date", r.date.Format("2006-01-02"), "err", r.err)
			continue
		}
		daysOK++
		totalRows += r.rows
		if daysOK%50 == 0 {
			logger.Info("progress",
				"days_ok", daysOK, "days_err", daysErr,
				"rows", totalRows, "elapsed", time.Since(started).Round(time.Second))
		}
	}
	return daysOK, daysErr, totalRows, time.Since(started).Round(time.Second)
}

// fetchAndPersist pulls one day's grouped-daily snapshot, projects
// it to fx_quotes rows, and writes them in a single batched insert.
// Returns row count + error. Empty days (weekends, holidays) return
// 0 rows + nil — they're not a failure, just no published bars.
func fetchAndPersist(
	ctx context.Context,
	client *forex.Client,
	store *timescale.Store,
	date time.Time,
	tickerFilter map[string]struct{},
	dryRun bool,
) (int, error) {
	dateStr := date.Format("2006-01-02")
	rates, _, err := client.HistoricalUSDRates(ctx, dateStr)
	if err != nil {
		return 0, fmt.Errorf("massive %s: %w", dateStr, err)
	}
	if len(rates) == 0 {
		return 0, nil
	}

	bucket := date.UTC().Truncate(24 * time.Hour)
	rows := make([]timescale.FXQuote, 0, len(rates))
	for code, rate := range rates {
		if rate <= 0 {
			continue
		}
		ticker := strings.ToUpper(code)
		if len(tickerFilter) > 0 {
			if _, ok := tickerFilter[ticker]; !ok {
				continue
			}
		}
		rows = append(rows, timescale.FXQuote{
			Bucket:     bucket,
			Ticker:     ticker,
			RateUSD:    rate,
			InverseUSD: 1.0 / rate,
			Source:     "massive-historical",
		})
	}
	if dryRun || store == nil {
		return len(rows), nil
	}
	if err := store.InsertFXQuoteBatch(ctx, rows); err != nil {
		return 0, fmt.Errorf("insert %s: %w", dateStr, err)
	}
	return len(rows), nil
}
