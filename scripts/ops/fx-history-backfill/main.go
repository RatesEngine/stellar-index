// scripts/ops/fx-history-backfill — one-off historical FX backfill.
//
// Fetches ECB daily reference rates from Frankfurter (frankfurter.dev)
// and writes them into the fx_quotes hypertable. Frankfurter publishes
// back to 1999-01-04 for ~32 currencies; one HTTP request returns the
// entire range, so a 25-year backfill is a single (large) JSON
// response, not 9,000 paid Massive requests.
//
// Idempotent on the (ticker, bucket) primary key — re-running on
// the same range upserts identical values.
//
// Why a separate binary not a worker hook: a 25-year backfill is a
// one-shot operator concern (we do it once on a fresh deployment),
// distinct from the forex worker's continuous ~5min refresh loop.
// Keeping it out-of-band makes progress observable on the operator's
// stderr stream instead of the API server log.
//
// Usage:
//
//	export DATABASE_URL=postgres://...
//	go run ./scripts/ops/fx-history-backfill \
//	    --years=25
//
// Flags:
//
//	--years=N              window depth (default 25 — covers ECB
//	                       inception in 1999)
//	--from=YYYY-MM-DD      override window start (overrides --years)
//	--to=YYYY-MM-DD        override window end (default today UTC)
//	--chunk-years=N        split the fetch into N-year chunks
//	                       (default 5; reduces peak memory + lets the
//	                       operator see progress every chunk)
//	--dry-run              fetch + print row counts but skip the DB write
//	--ticker=USD,EUR,...   restrict to a subset (default: all
//	                       Frankfurter-supported currencies)
//
// The script logs one line per chunk to stderr; final summary writes
// total rows + elapsed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/frankfurter"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
)

func main() {
	cfg, logger := parseFlags()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client := frankfurter.NewClient()

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
		"source", "frankfurter",
		"from", cfg.from.Format("2006-01-02"),
		"to", cfg.to.Format("2006-01-02"),
		"chunk_years", cfg.chunkYears,
		"dry_run", cfg.dryRun)

	totalRows, chunks, elapsed := runBackfill(ctx, logger, client, store, cfg)

	logger.Info("fx-history-backfill: done",
		"chunks", chunks,
		"rows", totalRows,
		"elapsed", elapsed)
}

type backfillConfig struct {
	dsn          string
	from         time.Time
	to           time.Time
	chunkYears   int
	dryRun       bool
	tickerFilter map[string]struct{}
}

// parseFlags pulls all CLI + env config and validates it. Exits the
// process on validation failure.
func parseFlags() (backfillConfig, *slog.Logger) {
	var (
		years      int
		fromStr    string
		toStr      string
		chunkYears int
		dryRun     bool
		tickerCSV  string
	)
	flag.IntVar(&years, "years", 25, "trailing window depth in years (default 25)")
	flag.StringVar(&fromStr, "from", "", "window start date YYYY-MM-DD (overrides --years)")
	flag.StringVar(&toStr, "to", "", "window end date YYYY-MM-DD (default: today UTC)")
	flag.IntVar(&chunkYears, "chunk-years", 5, "split the fetch into N-year chunks (default 5)")
	flag.BoolVar(&dryRun, "dry-run", false, "skip DB writes; report row counts only")
	flag.StringVar(&tickerCSV, "ticker", "", "comma-separated ticker subset (default: all)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	if chunkYears < 1 {
		chunkYears = 1
	}

	tickerFilter := map[string]struct{}{}
	if tickerCSV != "" {
		for _, t := range strings.Split(tickerCSV, ",") {
			tickerFilter[strings.ToUpper(strings.TrimSpace(t))] = struct{}{}
		}
	}

	return backfillConfig{
		dsn:          dsn,
		from:         from,
		to:           to,
		chunkYears:   chunkYears,
		dryRun:       dryRun,
		tickerFilter: tickerFilter,
	}, logger
}

// runBackfill walks the window in chunkYears-sized segments. Sequential,
// not parallel — Frankfurter is a free service and one operator-side
// backfill doesn't justify hammering it. Each chunk is one HTTP
// request returning every day for every currency in that window.
func runBackfill(
	ctx context.Context,
	logger *slog.Logger,
	client *frankfurter.Client,
	store *timescale.Store,
	cfg backfillConfig,
) (totalRows, chunks int, elapsed time.Duration) {
	started := time.Now()
	chunkStart := cfg.from
	for chunkStart.Before(cfg.to) {
		select {
		case <-ctx.Done():
			return totalRows, chunks, time.Since(started).Round(time.Second)
		default:
		}
		chunkEnd := chunkStart.AddDate(cfg.chunkYears, 0, 0)
		if chunkEnd.After(cfg.to) {
			chunkEnd = cfg.to
		}
		rows, err := fetchAndPersist(ctx, client, store, chunkStart, chunkEnd, cfg.tickerFilter, cfg.dryRun)
		if err != nil {
			logger.Warn("chunk failed",
				"from", chunkStart.Format("2006-01-02"),
				"to", chunkEnd.Format("2006-01-02"),
				"err", err)
		} else {
			totalRows += rows
			logger.Info("chunk done",
				"from", chunkStart.Format("2006-01-02"),
				"to", chunkEnd.Format("2006-01-02"),
				"rows", rows)
		}
		chunks++
		chunkStart = chunkEnd.AddDate(0, 0, 1)
	}
	return totalRows, chunks, time.Since(started).Round(time.Second)
}

// fetchAndPersist pulls one chunk of daily snapshots and projects each
// (date × ticker × rate) tuple into an fx_quotes row.
func fetchAndPersist(
	ctx context.Context,
	client *frankfurter.Client,
	store *timescale.Store,
	from, to time.Time,
	tickerFilter map[string]struct{},
	dryRun bool,
) (int, error) {
	days, err := client.RangeUSDRates(ctx, from, to)
	if err != nil {
		return 0, err
	}
	if len(days) == 0 {
		return 0, nil
	}

	rows := make([]timescale.FXQuote, 0, len(days)*16)
	for _, day := range days {
		bucket := day.Date.UTC().Truncate(24 * time.Hour)
		for code, rate := range day.Rates {
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
				Source:     "frankfurter-historical",
			})
		}
	}
	if dryRun || store == nil {
		return len(rows), nil
	}
	if err := store.InsertFXQuoteBatch(ctx, rows); err != nil {
		return 0, fmt.Errorf("insert %s..%s: %w", from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}
	return len(rows), nil
}
