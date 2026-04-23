// Binary ratesengine-api is the public REST + SSE API server.
//
// Today: /v1/healthz, /v1/readyz, /v1/version — the infra-facing
// surface. The full endpoint catalogue (/v1/price, /v1/history,
// /v1/ohlc, SSE streams, etc.) lands in follow-up PRs per
// docs/reference/api-design.md §5.
//
// Flags:
//
//	-config PATH    TOML config file (required)
//	-dry-run        Load config, open connections, validate, exit.
//
// Environment overrides for secrets apply on top of the file. See
// internal/config/load.go ApplyEnvOverrides.
//
// Graceful shutdown: SIGINT / SIGTERM cancel the root context;
// the HTTP server drains for up to 30 s before hard-exiting.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/metadata"
	"github.com/RatesEngine/rates-engine/internal/ratelimit"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	var (
		cfgPath = flag.String("config", "", "Path to TOML config file (required)")
		dryRun  = flag.Bool("dry-run", false, "Load config + open connections + exit without serving")
	)
	flag.Parse()

	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-api: -config is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*cfgPath, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "ratesengine-api: %v\n", err)
		os.Exit(1)
	}
}

func run(cfgPath string, dryRun bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	cfg.ApplyEnvOverrides()

	logger := mkLogger(cfg.Obs)
	logger.Info("starting",
		"version", version.String(),
		"region", cfg.Region.ID,
		"listen", cfg.API.ListenAddr,
		"external_url", cfg.API.ExternalBaseURL,
		"dry_run", dryRun,
	)

	rootCtx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Storage — required. API reads from Timescale (+ Redis cache
	// in a follow-up PR).
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

	// Redis — optional at the API layer. When reachable, it backs
	// the SEP-1 metadata cache; when not, the resolver falls
	// through to upstream fetches on every request (slow but
	// correct). We don't block startup on Redis — the readiness
	// probe reflects the truth.
	var rdb *redis.Client
	if cfg.Storage.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.Storage.RedisAddr,
			Password: cfg.Storage.RedisPassword,
		})
		defer func() { _ = rdb.Close() }()
	}

	// Build readiness-check set. Each implements v1.ReadyChecker.
	checks := []v1.ReadyChecker{
		storeChecker{s: store},
	}
	if rdb != nil {
		checks = append(checks, redisChecker{rdb: rdb})
	}

	// SEP-1 resolver + cache. AllowPrivateIPs left false — production
	// must refuse private/loopback dials. Cache is tolerant of a nil
	// rdb (falls through to the resolver every call).
	sep1Resolver := metadata.NewResolver(metadata.Options{
		Timeout: 8 * time.Second,
	})
	sep1Cache := metadata.NewCache(sep1Resolver, rdb)

	// Rate limit — per-IP anonymous bucket only for now. Per-API-key
	// buckets arrive with SEP-10 / apikey auth (see docs/reference/
	// api-design.md §6). When Redis is unavailable, no bucket is
	// constructed — the middleware is omitted and the stack runs
	// uncapped. An operator who cares will see this in readyz.
	var rateLimit middleware.Middleware
	if rdb != nil && cfg.API.AnonRateLimitPerMin > 0 {
		bucket := ratelimit.New(rdb, cfg.API.AnonRateLimitPerMin, time.Minute)
		rateLimit = middleware.RateLimit(
			bucket,
			nil, // default KeyFn — resolveRemoteIP from Logger middleware
			middleware.SkipHealthAndMetrics,
			logger.With("component", "ratelimit"),
		)
	}

	apiSrv := v1.New(v1.Options{
		Logger:      logger.With("component", "api"),
		ReadyChecks: checks,
		Assets:      storeAssetReader{s: store},
		Prices:      storePriceReader{s: store},
		History:     storeHistoryReader{s: store},
		Markets:     storeMarketsReader{s: store},
		Oracle:      storeOracleReader{s: store},
		Meta:        sep1Cache,
		RateLimit:   rateLimit,
	})

	if dryRun {
		logger.Info("dry-run complete — exiting")
		return nil
	}

	httpSrv := &http.Server{
		Addr:              cfg.API.ListenAddr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received — draining for up to 30s")
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	shutdownCtx, stopDrain := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopDrain()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http server shutdown", "err", err)
	} else {
		logger.Info("clean shutdown")
	}
	return nil
}

// storeChecker adapts *timescale.Store to the v1.ReadyChecker
// interface so /readyz can include it in the dependency poll.
type storeChecker struct{ s *timescale.Store }

func (c storeChecker) Name() string { return "postgres" }
func (c storeChecker) Ping(ctx context.Context) error {
	return c.s.DB().PingContext(ctx)
}

// redisChecker adapts *redis.Client to the v1.ReadyChecker interface.
// Redis is optional at API layer — readyz reports the actual state.
type redisChecker struct{ rdb *redis.Client }

func (c redisChecker) Name() string { return "redis" }
func (c redisChecker) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// storeAssetReader adapts *timescale.Store to v1.AssetReader. Keeps
// the typed boundary: the store returns canonical.Asset; the API
// layer owns the wire-shape conversion to v1.AssetDetail.
type storeAssetReader struct{ s *timescale.Store }

func (r storeAssetReader) ListAssets(ctx context.Context, cursor string, limit int) ([]v1.AssetDetail, string, error) {
	assets, next, err := r.s.DistinctAssets(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]v1.AssetDetail, len(assets))
	for i, a := range assets {
		out[i] = assetToDetail(a)
	}
	return out, next, nil
}

func (r storeAssetReader) GetAsset(ctx context.Context, a canonical.Asset) (v1.AssetDetail, error) {
	has, err := r.s.HasAsset(ctx, a)
	if err != nil {
		return v1.AssetDetail{}, err
	}
	if !has {
		return v1.AssetDetail{}, v1.ErrAssetNotFound
	}
	return assetToDetail(a), nil
}

// storeMarketsReader adapts *timescale.Store to v1.MarketsReader.
// Translates timescale.Market (typed Pair) to v1.Market (string
// wire shape) so the API layer owns its own schema.
type storeMarketsReader struct{ s *timescale.Store }

func (r storeMarketsReader) DistinctPairs(ctx context.Context, cursor string, limit int) ([]v1.Market, string, error) {
	rows, next, err := r.s.DistinctPairs(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]v1.Market, len(rows))
	for i, m := range rows {
		out[i] = v1.Market{
			Base:          m.Pair.Base.String(),
			Quote:         m.Pair.Quote.String(),
			LastTradeAt:   m.LastTradeAt,
			TradeCount24h: m.TradeCount24h,
		}
	}
	return out, next, nil
}

// storeOracleReader adapts *timescale.Store to v1.OracleReader.
type storeOracleReader struct{ s *timescale.Store }

func (r storeOracleReader) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return r.s.LatestOracleUpdatesForAsset(ctx, asset, sourceFilter)
}

// storeHistoryReader adapts *timescale.Store to v1.HistoryReader.
// Pure passthrough: the store already returns []canonical.Trade
// ordered by ts ASC, which is exactly what the handler expects.
type storeHistoryReader struct{ s *timescale.Store }

func (r storeHistoryReader) TradesInRange(ctx context.Context, pair canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	return r.s.TradesInRange(ctx, pair, from, to, limit)
}

func (r storeHistoryReader) TradesInRangeAfter(ctx context.Context, pair canonical.Pair, from, to, afterTs time.Time, afterLedger uint32, limit int) ([]canonical.Trade, error) {
	return r.s.TradesInRangeAfter(ctx, pair, from, to, afterTs, afterLedger, limit)
}

// storePriceReader adapts *timescale.Store to v1.PriceReader.
//
// This MVP impl always falls back to "last trade in the trades
// hypertable" and reports stale=true. Once the aggregator ships,
// swap this for an adapter that reads `price:<asset>` from Redis
// first and this trade-based path becomes the second-level
// fallback.
type storePriceReader struct{ s *timescale.Store }

func (r storePriceReader) LatestPrice(ctx context.Context, asset, quote canonical.Asset) (v1.PriceSnapshot, []string, bool, error) {
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		return v1.PriceSnapshot{}, nil, false, err
	}
	trades, err := r.s.LatestTradesForPair(ctx, pair, 1)
	if err != nil {
		return v1.PriceSnapshot{}, nil, false, err
	}
	if len(trades) == 0 {
		return v1.PriceSnapshot{}, nil, false, v1.ErrPriceNotFound
	}
	// decimals=7 matches Stellar's default stroop scale. A future
	// revision reads per-asset decimals from internal/metadata.
	snap := v1.LastTradeToSnapshot(trades[0], 7)
	// This path is always "stale" from the serving-plane's POV —
	// it's not an aggregated VWAP. Clients expecting freshness
	// should treat this as degraded.
	return snap, []string{trades[0].Source}, true, nil
}

// assetToDetail converts canonical.Asset → v1.AssetDetail. Nullable
// fields become nil pointers when empty so the JSON omits them.
//
// SEP-1 + home-domain overlay is future work — once
// internal/metadata is wired we'll enrich this with the stellar.toml
// fields (name, description, image, sep1_status).
func assetToDetail(a canonical.Asset) v1.AssetDetail {
	d := v1.AssetDetail{
		AssetID:    a.String(),
		Type:       string(a.Type),
		Code:       a.Code,
		Decimals:   7, // overlay from SEP-41 decimals() in follow-up
		Sep1Status: "not_applicable",
	}
	if a.Issuer != "" {
		v := a.Issuer
		d.Issuer = &v
	}
	if a.ContractID != "" {
		v := a.ContractID
		d.ContractID = &v
	}
	return d
}

// mkLogger mirrors the indexer's logger factory. Could extract to
// a shared internal/obs/slog.go in a future PR when we have three
// binaries doing the same thing.
func mkLogger(obs config.ObsConfig) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(obs.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(obs.LogFormat) {
	case "console", "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler).With("binary", "ratesengine-api")
}
