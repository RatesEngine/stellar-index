// Binary ratesengine-aggregator computes VWAP over the ingested
// canonical trade stream and writes pre-aggregated results to Redis
// so API requests serve from cache rather than recomputing on every
// query.
//
// v1 scope (PR 182):
//
//   - Rolling-window VWAP per configured pair, written to Redis
//     keys `vwap:<base>:<quote>:<window-seconds>` on a 30 s cadence
//     (configurable).
//   - Passthrough single-source aggregation: every trade in the
//     window contributes regardless of source class. The API
//     already computes VWAP on-query; this binary moves the
//     computation off the hot path.
//
// Deferred to follow-up PRs (each drops into orchestrator.Config
// without shape change):
//
//   - CAGG refresh driver (Timescale's background job handles it
//     today; manual triggering lands when we want tight refresh
//     guarantees).
//   - Triangulation worker (XLM/USD × USD/EUR = XLM/EUR).
//   - Class-filtered VWAP (only ClassExchange contributes).
//   - Stablecoin → fiat proxy (USDT→USD, USDC→USD …).
//   - Divergence detector (flags aggregator-class drift).
//   - Outlier filter wrap on the raw-trade fetch.
//
// Flags:
//
//	-config PATH    TOML config file (required).
//	-dry-run        Load config, open connections, validate, exit.
//
// Graceful shutdown: SIGINT + SIGTERM cancel the root context;
// the orchestrator's Tick unwinds on the next iteration.
//
// ⚠ CAGG TWAP CAVEAT ⚠
//
// Migration 0002 defines a `twap` column in prices_1m / _15m / _1h /
// _4h / _1d / _1w / _1mo as `avg(quote_amount / base_amount)` — the
// arithmetic mean of observed trade prices, NOT a time-weighted
// average. True TWAP needs inter-trade durations that the CAGG
// definitions don't capture. The v1 orchestrator sidesteps this by
// computing VWAP (not TWAP) from raw trades; TWAP-via-CAGG lands
// with either internal/aggregate/twap.go (Go-side) or a corrected
// CAGG that stores per-bucket duration.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate/orchestrator"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	var (
		cfgPath = flag.String("config", "", "Path to TOML config file (required)")
		dryRun  = flag.Bool("dry-run", false, "Load config + open connections + exit without running the ticker")
	)
	flag.Parse()

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-aggregator: -config is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*cfgPath, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "ratesengine-aggregator: %v\n", err)
		os.Exit(1)
	}
}

func run(cfgPath string, dryRun bool) error {
	cfg, err := config.LoadWithEnv(cfgPath)
	if err != nil {
		return err
	}

	logger := mkLogger(cfg.Obs)
	logger.Info("starting",
		"version", version.String(),
		"region", cfg.Region.ID,
		"dry_run", dryRun,
	)

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ─── Storage ─────────────────────────────────────────────────
	store, err := timescale.Open(rootCtx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("storage close", "err", err)
		}
	}()
	logger.Info("storage connected")

	// ─── Redis ───────────────────────────────────────────────────
	// Required for the aggregator — no useful pre-compute without
	// a cache to write to. Dry-run pings explicitly so config
	// drift surfaces at startup rather than at first tick.
	if cfg.Storage.RedisAddr == "" {
		return errors.New("storage.redis_addr is required — aggregator writes VWAP to Redis")
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Storage.RedisAddr,
		Password: cfg.Storage.RedisPassword,
	})
	defer func() { _ = rdb.Close() }()
	if dryRun {
		pingCtx, cancelPing := context.WithTimeout(rootCtx, 5*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancelPing()
		if err != nil {
			return fmt.Errorf("redis: ping: %w", err)
		}
		logger.Info("redis reachable", "addr", cfg.Storage.RedisAddr)
	}

	// ─── Pair list ───────────────────────────────────────────────
	// v1 uses a built-in coverage set (crypto × fiat). Operator
	// override via config is a follow-up once the aggregator is
	// exercised in ops.
	pairs := defaultPairs()
	logger.Info("aggregator pair set resolved", "count", len(pairs))

	orch := orchestrator.New(store, rdb, orchestrator.Config{
		Pairs:  pairs,
		Logger: logger,
	})

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	// ─── Run ─────────────────────────────────────────────────────
	logger.Info("orchestrator starting")
	if err := orch.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("orchestrator: %w", err)
	}
	logger.Info("orchestrator stopped", "stats", orch.Stats())
	return nil
}

// defaultPairs is the v1 aggregator coverage set. XLM/BTC/ETH across
// USD/EUR/GBP gives the RFP's major-pair coverage without per-
// operator tuning. Parallel to cmd/ratesengine-indexer's
// defaultAggregatorPairs (kept per-binary so each can evolve
// independently).
func defaultPairs() []canonical.Pair {
	cryptos := []string{"XLM", "BTC", "ETH"}
	fiats := []string{"USD", "EUR", "GBP"}
	var out []canonical.Pair
	for _, c := range cryptos {
		ca, err := canonical.NewCryptoAsset(c)
		if err != nil {
			continue
		}
		for _, f := range fiats {
			fa, err := canonical.NewFiatAsset(f)
			if err != nil {
				continue
			}
			p, err := canonical.NewPair(ca, fa)
			if err != nil {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// mkLogger builds the structured logger with the configured format /
// level. Parallel to cmd/ratesengine-indexer.
func mkLogger(cfg config.ObsConfig) *slog.Logger {
	lvl := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if cfg.LogFormat == "console" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(h)
}
