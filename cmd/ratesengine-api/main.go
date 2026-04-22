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

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
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

	// Build readiness-check set. Each implements v1.ReadyChecker.
	checks := []v1.ReadyChecker{
		storeChecker{s: store},
		// TODO(#0): redis readiness-check adapter once we wire the
		// Redis client at this level.
	}

	apiSrv := v1.New(v1.Options{
		Logger:      logger.With("component", "api"),
		ReadyChecks: checks,
		Assets:      storeAssetReader{s: store},
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
