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
// internal/config/load.go LoadWithEnv.
//
// Graceful shutdown: SIGINT / SIGTERM cancel the root context;
// the HTTP server drains for up to 30 s before hard-exiting.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/auth/sep10"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/divergence"
	"github.com/RatesEngine/rates-engine/internal/metadata"
	"github.com/RatesEngine/rates-engine/internal/ratelimit"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/supply"
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

func run(cfgPath string, dryRun bool) error { //nolint:gocognit,funlen,gocyclo // dispatch-heavy wiring; splitting would reduce linearity
	cfg, err := config.LoadWithEnv(cfgPath)
	if err != nil {
		return err
	}

	logger := mkLogger(cfg.Obs)
	logger.Info("starting",
		"version", version.String(),
		"region", cfg.Region.ID,
		"listen", cfg.API.ListenAddr,
		"external_url", cfg.API.ExternalBaseURL,
		"auth_mode", cfg.API.AuthMode,
		"dry_run", dryRun,
	)

	// SEP-10 validator — wired regardless of auth_mode so the
	// /v1/auth/sep10/{challenge,token} endpoints serve. When the
	// required env vars (cfg.API.SEP10.SeedEnv / .JWTSecretEnv)
	// are missing or empty, the constructor errors and the binary
	// fails loud at startup rather than silently 503-ing on every
	// challenge.
	sep10Validator, err := buildSEP10Validator(cfg.API.SEP10)
	if err != nil {
		// auth_mode=sep10 makes this a hard failure (we MUST have a
		// validator to bootstrap auth at all). Otherwise log + carry
		// on with a Noop so the handlers return 503 specifically for
		// /v1/auth/sep10/* without taking down the rest of the API.
		if cfg.API.AuthMode == "sep10" {
			return fmt.Errorf("sep10 validator: %w (auth_mode=sep10 requires it)", err)
		}
		logger.Warn("sep10 validator not configured; /v1/auth/sep10/* will return 503",
			"err", err)
		sep10Validator = auth.NoopSEP10Validator{}
	} else {
		logger.Info("sep10 validator wired",
			"web_auth_domain", cfg.API.SEP10.WebAuthDomain,
			"home_domain", cfg.API.SEP10.HomeDomain,
			"challenge_ttl", cfg.API.SEP10.ChallengeTTL,
			"jwt_ttl", cfg.API.SEP10.JWTTTL)
	}

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
	//
	// Exception: under -dry-run we ping explicitly. Without the
	// ping dry-run is a liar — redis.NewClient is lazy, so a bad
	// RedisAddr / wrong password / wrong network never surfaces.
	// The whole point of dry-run is "does this config actually
	// work?" so a misconfig that only reveals itself under real
	// traffic defeats the flag.
	var rdb *redis.Client
	if cfg.Storage.RedisAddr != "" {
		rdb = redis.NewClient(&redis.Options{
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

	// CORS — only wired when the operator configured allowed origins.
	// Empty list means same-origin only (no cross-origin clients).
	var cors middleware.Middleware
	if len(cfg.API.AllowedOrigins) > 0 {
		cors = middleware.CORS(middleware.CORSOptions{
			AllowedOrigins: cfg.API.AllowedOrigins,
		})
	}

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

	// Auth — translate the configured auth_mode into the middleware.
	// auth_mode=none yields a nil middleware (server stack omits it
	// and downstream code treats absence-of-Subject as anonymous).
	// auth_mode=apikey wires the Redis-backed validator when Redis
	// is reachable; if Redis is unavailable the middleware still
	// runs but the validator returns ErrNotImplemented → 503, which
	// is the correct fail-loud behaviour for an opted-into mode.
	authMW := buildAuthMiddleware(cfg.API.AuthMode, rdb, sep10Validator, logger)

	// Account store backs POST /v1/account/keys. Only wired when
	// Redis is reachable — without Redis there's nowhere to persist
	// the issued record. The handler then returns 503 for that
	// path; /me + /usage still serve from the request-context
	// Subject without the store.
	var accountStore v1.AccountStore
	if rdb != nil {
		accountStore = auth.NewRedisAPIKeyStore(rdb)
	}

	// Divergence lookup adapter. Only wired when Redis is reachable
	// (the worker's cached results live there). Empty References
	// list means the worker writes no entries — handler treats every
	// asset as "not yet refreshed" and leaves the flag unset.
	var divergenceLooker v1.DivergenceLooker
	if rdb != nil {
		divSvc, err := divergence.NewService(divergence.ServiceOptions{
			Cache: rdb,
			// References list is empty by default — operator wires
			// concrete refs (CoinGecko, CMC, etc.) when ready. The
			// service still serves LookupCached against entries the
			// worker wrote; it just doesn't write any of its own.
		})
		if err != nil {
			return fmt.Errorf("divergence service: %w", err)
		}
		divergenceLooker = divergenceAdapter{svc: divSvc}
	}

	apiSrv := v1.New(v1.Options{
		Logger:       logger.With("component", "api"),
		ReadyChecks:  checks,
		Assets:       storeAssetReader{s: store, homeDomainLookup: cfg.Metadata.HomeDomainFor},
		Prices:       storePriceReader{s: store},
		History:      storeHistoryReader{s: store},
		Markets:      storeMarketsReader{s: store},
		Oracle:       storeOracleReader{s: store},
		Meta:         sep1Cache,
		Accounts:     accountStore,
		Divergence:   divergenceLooker,
		Confidence:   redisConfidenceLooker{rdb: rdb},
		Triangulated: redisTriangulatedLooker{rdb: rdb},
		Supply:       storeSupplyLooker{s: store},
		Volume:       storeVolumeReader{s: store},
		SEP10:        sep10Validator,
		CORS:         cors,
		Auth:         authMW,
		RateLimit:    rateLimit,
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

// buildAuthMiddleware translates the configured auth_mode into a
// concrete middleware. Returns nil for mode=none — the server stack
// omits absent middleware entirely so anonymous traffic doesn't
// pay a per-request closure cost.
//
// auth_mode=apikey requires Redis: when rdb is nil the middleware
// still wires up but with a Noop validator, so every request 503s
// — the correct fail-loud behaviour for a deployment that opted in
// without Redis. auth_mode=sep10 wires the same validator as the
// /v1/auth/sep10/* endpoints — the validator parameter is what
// `buildSEP10Validator` returned at startup (real or Noop).
func buildAuthMiddleware(mode string, rdb *redis.Client, sep10Validator auth.SEP10Validator, logger *slog.Logger) middleware.Middleware {
	switch mode {
	case "", "none":
		return nil

	case "apikey":
		var validator auth.APIKeyValidator = auth.NoopAPIKeyValidator{}
		if rdb != nil {
			validator = auth.NewRedisAPIKeyValidator(rdb)
			logger.Info("auth: apikey validator wired", "backend", "redis")
		} else {
			logger.Error("auth_mode=apikey but Redis is not configured — every request will 503",
				"reason", "RedisAPIKeyValidator requires a Redis client")
		}
		return middleware.Auth(middleware.AuthOptions{
			Mode:   middleware.AuthModeAPIKey,
			APIKey: validator,
		})

	case "sep10":
		return middleware.Auth(middleware.AuthOptions{
			Mode:  middleware.AuthModeSEP10,
			SEP10: sep10Validator,
		})
	}
	// Unknown mode reaches here only if config validation regressed.
	// Returning nil silently demotes to anonymous, which is the wrong
	// default; log loudly and rely on the validate.go gate to keep
	// this branch unreachable.
	logger.Error("unknown auth_mode — server falling through to no-auth", "mode", mode)
	return nil
}

// buildSEP10Validator constructs an [auth.SEP10Validator] from the
// API's SEP10Config. Reads the seed + JWT secret from the
// configured env vars; missing or empty values surface as errors
// the caller can decide to handle (auth_mode=sep10 → fail loud at
// startup; otherwise → wire a Noop and log).
//
// Empty SeedEnv / JWTSecretEnv (config not opted into SEP-10) is
// also an error — the caller treats it as "feature not configured"
// and falls back to the Noop. The behaviour is symmetric across
// "env name unset" and "env value empty": both mean the operator
// hasn't supplied a credential.
func buildSEP10Validator(cfg config.SEP10Config) (auth.SEP10Validator, error) {
	if cfg.SeedEnv == "" || cfg.JWTSecretEnv == "" {
		return nil, errors.New("sep10: seed_env / jwt_secret_env not configured")
	}
	seed := os.Getenv(cfg.SeedEnv)
	if seed == "" {
		return nil, fmt.Errorf("sep10: env %s is unset or empty", cfg.SeedEnv)
	}
	jwtSecret := os.Getenv(cfg.JWTSecretEnv)
	if jwtSecret == "" {
		return nil, fmt.Errorf("sep10: env %s is unset or empty", cfg.JWTSecretEnv)
	}

	network := stellarNetworkPassphrase()

	v, err := sep10.NewValidator(sep10.Options{
		ServerSeed:        seed,
		NetworkPassphrase: network,
		WebAuthDomain:     cfg.WebAuthDomain,
		HomeDomain:        cfg.HomeDomain,
		ChallengeTTL:      cfg.ChallengeTTL,
		JWTTTL:            cfg.JWTTTL,
		JWTSecret:         []byte(jwtSecret),
	})
	if err != nil {
		return nil, fmt.Errorf("sep10: NewValidator: %w", err)
	}
	return v, nil
}

// stellarNetworkPassphrase returns the SDK-canonical pubnet
// passphrase. Wrapped in a function so future testnet support can
// branch on cfg.Stellar.Network without rewriting buildSEP10Validator.
func stellarNetworkPassphrase() string {
	return "Public Global Stellar Network ; September 2015"
}

// divergenceAdapter wraps *divergence.Service to satisfy the v1
// DivergenceLooker interface. v1 deliberately doesn't import the
// divergence package (kept storage-package-agnostic); this thin
// shim is the wire between them.
type divergenceAdapter struct {
	svc *divergence.Service
}

func (a divergenceAdapter) DivergenceFiringFor(ctx context.Context, asset canonical.Asset) (bool, error) {
	cached, found, err := a.svc.LookupCached(ctx, asset)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return cached.WarningFired, nil
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
//
// homeDomainLookup is the curated issuer → home-domain map from
// cfg.Metadata. Returns ("", false) for un-curated issuers; the
// AssetDetail then has HomeDomain==nil and the overlay handler
// stamps sep1_status="not_fetched" for that case.
type storeAssetReader struct {
	s                *timescale.Store
	homeDomainLookup func(issuer string) (string, bool)
}

func (r storeAssetReader) ListAssets(ctx context.Context, cursor string, limit int) ([]v1.AssetDetail, string, error) {
	assets, next, err := r.s.DistinctAssets(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]v1.AssetDetail, len(assets))
	for i, a := range assets {
		out[i] = assetToDetail(a, r.homeDomainLookup)
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
	return assetToDetail(a, r.homeDomainLookup), nil
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

func (r storeMarketsReader) PairMarket(ctx context.Context, base, quote canonical.Asset) (v1.Market, bool, error) {
	m, ok, err := r.s.PairMarket(ctx, base, quote)
	if err != nil || !ok {
		return v1.Market{}, ok, err
	}
	return v1.Market{
		Base:          m.Pair.Base.String(),
		Quote:         m.Pair.Quote.String(),
		LastTradeAt:   m.LastTradeAt,
		TradeCount24h: m.TradeCount24h,
	}, true, nil
}

// storeOracleReader adapts *timescale.Store to v1.OracleReader.
type storeOracleReader struct{ s *timescale.Store }

func (r storeOracleReader) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return r.s.LatestOracleUpdatesForAsset(ctx, asset, sourceFilter)
}

// redisConfidenceLooker adapts the shared Redis client to
// v1.ConfidenceLooker by reading the JSON-encoded confidence.Score
// the aggregator writes at `confidence:<base>:<quote>:<window>`.
//
// Cache miss → (zero, false, nil); the v1 handler then leaves the
// confidence fields off the wire for that response. Read errors
// propagate so the handler can log; the response still ships
// without confidence.
type redisConfidenceLooker struct{ rdb *redis.Client }

func (r redisConfidenceLooker) LookupConfidence(ctx context.Context, asset, quote canonical.Asset, window time.Duration) (v1.PriceSnapshotConfidence, bool, error) {
	if r.rdb == nil {
		return v1.PriceSnapshotConfidence{}, false, nil
	}
	key := cachekeys.Confidence(asset, quote, window)
	raw, err := r.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return v1.PriceSnapshotConfidence{}, false, nil
	}
	if err != nil {
		return v1.PriceSnapshotConfidence{}, false, fmt.Errorf("confidence cache get %s: %w", key, err)
	}
	var score confidence.Score
	if err := json.Unmarshal(raw, &score); err != nil {
		return v1.PriceSnapshotConfidence{}, false, fmt.Errorf("confidence cache decode %s: %w", key, err)
	}
	return v1.PriceSnapshotConfidence{
		Confidence: score.Confidence,
		Factors: v1.ConfidenceFactors{
			ZScore:          score.Factors.ZScore,
			SourceCount:     score.Factors.SourceCount,
			Diversity:       score.Factors.Diversity,
			Liquidity:       score.Factors.Liquidity,
			CrossOracle:     score.Factors.CrossOracle,
			BaselineQuality: score.Factors.BaselineQuality,
		},
	}, true, nil
}

// redisTriangulatedLooker adapts the shared Redis client to
// v1.TriangulatedPriceLooker. Reads:
//
//   - cachekeys.VWAP(base, quote, window) — the value
//   - cachekeys.VWAPProvenance(...)        — the marker
//
// Per the marker contract, "triangulated" means the aggregator's
// triangulation worker wrote this value (vs. the direct per-pair
// refresh, which doesn't write the marker). Absence of the marker
// → isTriangulated=false; the handler then preserves the original
// 404 rather than serving a direct VWAP from cache (Timescale is
// the source of truth for direct VWAPs).
//
// Cache miss returns (found=false, no error). Read errors
// propagate so the handler can log; the response 404s.
type redisTriangulatedLooker struct{ rdb *redis.Client }

func (r redisTriangulatedLooker) LookupTriangulatedVWAP(
	ctx context.Context, base, quote canonical.Asset, window time.Duration,
) (string, bool, bool, error) {
	if r.rdb == nil {
		return "", false, false, nil
	}
	valKey := cachekeys.VWAP(base, quote, window)
	val, err := r.rdb.Get(ctx, valKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("vwap cache get %s: %w", valKey, err)
	}
	provKey := cachekeys.VWAPProvenance(base, quote, window)
	prov, err := r.rdb.Get(ctx, provKey).Result()
	if errors.Is(err, redis.Nil) {
		// Value exists but no provenance marker → direct VWAP
		// (per the marker contract). Return found=true but
		// isTriangulated=false so the handler preserves the 404.
		return val, false, true, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("provenance cache get %s: %w", provKey, err)
	}
	return val, prov == cachekeys.VWAPProvenanceTriangulated, true, nil
}

// storeHistoryReader adapts *timescale.Store to v1.HistoryReader.
// Pure passthrough: the store already returns []canonical.Trade
// ordered by ts ASC, which is exactly what the handler expects.
type storeHistoryReader struct{ s *timescale.Store }

func (r storeHistoryReader) TradesInRange(ctx context.Context, pair canonical.Pair, from, to time.Time, limit int) ([]canonical.Trade, error) {
	return r.s.TradesInRange(ctx, pair, from, to, limit)
}

func (r storeHistoryReader) TradesInRangeAfter(ctx context.Context, pair canonical.Pair, from, to, afterTs time.Time, afterLedger uint32, afterTxHash, afterSource string, afterOpIndex uint32, limit int) ([]canonical.Trade, error) {
	return r.s.TradesInRangeAfter(ctx, pair, from, to, afterTs, afterLedger, afterTxHash, afterSource, afterOpIndex, limit)
}

// LatestTradePerSource adapts [timescale.Store.LatestTradePerSource]
// to the v1.HistoryReader interface. Pure passthrough: the store
// already does the DISTINCT ON (source) work in SQL.
func (r storeHistoryReader) LatestTradePerSource(ctx context.Context, pair canonical.Pair, sourceFilter string) ([]canonical.Trade, error) {
	return r.s.LatestTradePerSource(ctx, pair, sourceFilter)
}

// HistoryPoints adapts [timescale.Store.HistoryPoints] to the
// v1.HistoryReader interface. Translates the storage-side
// timescale.HistoryGranularity string-typed enum back to plain
// strings for the v1 type, and the rich timescale.HistoryPoint to
// the v1 wire-shape variant. Unknown granularities propagate as
// v1.ErrUnknownGranularity (handler turns into 400).
func (r storeHistoryReader) HistoryPoints(ctx context.Context, pair canonical.Pair, granularity string, limit int) ([]v1.HistoryPoint, error) {
	g := timescale.HistoryGranularity(granularity)
	if err := g.Validate(); err != nil {
		return nil, v1.ErrUnknownGranularity
	}
	rows, err := r.s.HistoryPoints(ctx, pair, g, limit)
	if err != nil {
		return nil, err
	}
	out := make([]v1.HistoryPoint, len(rows))
	for i, row := range rows {
		out[i] = v1.HistoryPoint{
			Bucket:    row.Bucket,
			VWAP:      row.VWAP,
			VolumeUSD: row.VolumeUSD,
		}
	}
	return out, nil
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

	// Primary path: most-recent CLOSED 1-minute VWAP from the
	// prices_1m CAGG (per ADR-0015 we serve only closed buckets).
	// This is preferred because:
	//   - it's volume-weighted across every contributing source;
	//   - it's pre-computed by the aggregator policy (sub-100ms read);
	//   - it labels stale=false in the envelope.
	row, err := r.s.LatestClosedVWAP1mForPair(ctx, pair)
	if err == nil {
		return v1.VWAP1mToSnapshot(asset.String(), quote.String(), row.VWAP, row.Bucket),
			row.Sources, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return v1.PriceSnapshot{}, nil, false, err
	}

	// Fallback: latest-trade. Hit when no closed 1m bucket exists for
	// the pair — typical for a brand-new listing that just got its
	// first trade in the in-progress bucket. Marks the response
	// stale=true; clients expecting freshness treat this as degraded.
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
	return snap, []string{trades[0].Source}, true, nil
}

// RecentClosedSnapshots is the SEP-40 prices(asset, records)
// passthrough — most-recent N closed 1-minute VWAP buckets. Empty
// slice + nil error when the pair has no closed buckets yet (the
// "asset unknown" distinction is the API handler's job via the
// asset-existence check, not this reader's).
func (r storePriceReader) RecentClosedSnapshots(ctx context.Context, asset, quote canonical.Asset, n int) ([]v1.PriceSnapshot, error) {
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		return nil, err
	}
	rows, err := r.s.RecentClosedVWAP1mForPair(ctx, pair, n)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []v1.PriceSnapshot{}, nil
	}
	out := make([]v1.PriceSnapshot, len(rows))
	for i, row := range rows {
		out[i] = v1.VWAP1mToSnapshot(asset.String(), quote.String(), row.VWAP, row.Bucket)
	}
	return out, nil
}

// assetToDetail converts canonical.Asset → v1.AssetDetail. Nullable
// fields become nil pointers when empty so the JSON omits them.
//
// homeDomainLookup populates HomeDomain for classic assets whose
// issuer is on the operator's curated map (cfg.Metadata.IssuerHomeDomains).
// When the issuer is curated, the SEP-1 overlay handler downstream
// resolves stellar.toml and fills the overlay fields. When the
// issuer is NOT curated, HomeDomain stays nil and the handler
// stamps sep1_status="not_fetched". Pass nil for the lookup if the
// caller doesn't have one (tests + scaffolding paths).
//
// SAC-wrapped classics + Soroban tokens have no issuer in the
// classic sense — HomeDomain stays nil; sep1_status falls through
// to "not_applicable" via the handler.
func assetToDetail(a canonical.Asset, homeDomainLookup func(issuer string) (string, bool)) v1.AssetDetail {
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
		// Classic asset with a known issuer — try the curated lookup
		// to populate HomeDomain. The handler's overlay logic takes
		// over from here: with HomeDomain set + s.meta wired,
		// applySep1Overlay runs and stamps the resulting status; with
		// HomeDomain set + s.meta nil, the handler stamps "not_fetched".
		if homeDomainLookup != nil {
			if hd, ok := homeDomainLookup(a.Issuer); ok {
				d.HomeDomain = &hd
				// Clear the "not_applicable" so the handler's overlay
				// logic kicks in. The handler stamps the right value
				// based on overlay outcome.
				d.Sep1Status = ""
			}
		}
	}
	if a.ContractID != "" {
		v := a.ContractID
		d.ContractID = &v
	}
	return d
}

// storeVolumeReader adapts *timescale.Store to v1.VolumeReader.
// Returns the trailing-24h USD volume across every pair the asset
// participates in. No error translation needed — the timescale
// helper returns "0" when the asset is tracked but had no trades,
// and a real error for genuine SQL failures.
type storeVolumeReader struct{ s *timescale.Store }

func (r storeVolumeReader) Volume24hUSDForAsset(ctx context.Context, assetKey string) (string, error) {
	return r.s.Volume24hUSDForAsset(ctx, assetKey)
}

// storeSupplyLooker adapts *timescale.Store to v1.SupplyLooker for
// the F2-fields path on /v1/assets/{id}. Closes audit F-0020 +
// Codex Freighter-V2 high-1: the API binary previously left
// Options.Supply nil, dead-coding the F2 read path entirely.
//
// Error translation: timescale.ErrNotFound (no recorded snapshot)
// becomes v1.ErrSupplyNotFound, which the handler treats as
// "feature unavailable for this asset" and leaves the F2 fields
// null on the response. Other errors propagate unchanged so the
// handler can log them at WARN.
type storeSupplyLooker struct{ s *timescale.Store }

func (r storeSupplyLooker) LatestSupply(ctx context.Context, assetKey string) (supply.Supply, error) {
	snap, err := r.s.LatestSupply(ctx, assetKey)
	if err != nil {
		if errors.Is(err, timescale.ErrNotFound) {
			return supply.Supply{}, v1.ErrSupplyNotFound
		}
		return supply.Supply{}, err
	}
	return snap, nil
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
