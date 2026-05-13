// Binary ratesengine-api is the public REST + SSE API server.
//
// Surface (registered in `internal/api/v1/server.go`'s
// `RegisterRoutes`):
//
//   - Pricing: /v1/price, /v1/price/batch (GET + POST),
//     /v1/price/tip, /v1/vwap, /v1/twap, /v1/observations.
//   - Historical: /v1/history, /v1/history/since-inception,
//     /v1/ohlc, /v1/chart.
//   - Catalogue: /v1/assets, /v1/assets/{id}, /v1/assets/{id}/metadata,
//     /v1/markets, /v1/pairs, /v1/sources.
//   - Oracle (SEP-40 passthrough): /v1/oracle/latest,
//     /v1/oracle/lastprice, /v1/oracle/prices,
//     /v1/oracle/x_last_price.
//   - Account self-service: /v1/account/me, /v1/account/usage,
//     /v1/account/keys (POST).
//   - SEP-10 web auth: /v1/auth/sep10/challenge,
//     /v1/auth/sep10/token.
//   - SSE streams: /v1/price/stream, /v1/price/tip/stream,
//     /v1/observations/stream.
//   - Operator-facing: /v1/healthz, /v1/readyz, /v1/version,
//     /metrics.
//
// The canonical list is the `s.mux.HandleFunc(...)` block in
// `internal/api/v1/server.go` and the OpenAPI spec at
// `openapi/rates-engine.v1.yaml`. CI (`lint-docs.sh §2`) keeps
// the two in lock-step.
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

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
	"github.com/RatesEngine/rates-engine/internal/aggregate/freeze"
	"github.com/RatesEngine/rates-engine/internal/api/streaming"
	"github.com/RatesEngine/rates-engine/internal/api/streaming/redispub"
	"github.com/RatesEngine/rates-engine/internal/api/streampublish"
	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/api/v1/dashboardauth"
	"github.com/RatesEngine/rates-engine/internal/api/v1/dashboardkeys"
	"github.com/RatesEngine/rates-engine/internal/api/v1/dashboardwebhooks"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/auth/sep10"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/currency"
	"github.com/RatesEngine/rates-engine/internal/customerwebhook"
	"github.com/RatesEngine/rates-engine/internal/divergence"
	"github.com/RatesEngine/rates-engine/internal/metadata"
	"github.com/RatesEngine/rates-engine/internal/notify"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/platform"
	"github.com/RatesEngine/rates-engine/internal/platform/postgresstore"
	"github.com/RatesEngine/rates-engine/internal/ratelimit"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	"github.com/RatesEngine/rates-engine/internal/sources/forex"
	"github.com/RatesEngine/rates-engine/internal/storage/redisclient"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/supply"
	"github.com/RatesEngine/rates-engine/internal/usage"
	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	var (
		cfgPath     = flag.String("config", "", "Path to TOML config file (required)")
		dryRun      = flag.Bool("dry-run", false, "Load config + open connections + exit without serving")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

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

	// Pre-launch hardening warnings. These don't block startup —
	// the binary still serves — but operators get loud signals at
	// boot for risky default configurations. See
	// docs/operations/pre-launch-hardening.md.
	warnUnsafeBind(logger, cfg.API.ListenAddr, cfg.API.TrustedProxyCIDRs)
	warnOpenCORS(logger, cfg.API.AllowedOrigins, cfg.API.AuthMode)

	// SEP-10 validator — wired regardless of auth_mode so the
	// /v1/auth/sep10/{challenge,token} endpoints serve. When the
	// required env vars (cfg.API.SEP10.SeedEnv / .JWTSecretEnv)
	// are missing or empty, the constructor errors and the binary
	// fails loud at startup rather than silently 503-ing on every
	// challenge.
	//
	// `nil` Redis client at this construction site means the
	// replay-guard defence (F-1224) is disabled at startup. The
	// validator is rebuilt below once `rdb` exists so production
	// always has the guard wired; this early Noop construction
	// keeps dry-run + early-failure paths unchanged.
	sep10Validator, err := buildSEP10Validator(cfg.API.SEP10, nil)
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
	// Production runs Sentinel mode (per ADR-0024); dev / single-
	// node runs single mode. redisclient.Build picks the branch
	// based on cfg.Storage.RedisSentinelAddrs.
	//
	// Exception: under -dry-run we ping explicitly. Without the
	// ping dry-run is a liar — both NewClient and NewFailoverClient
	// are lazy, so a bad addr / wrong password / wrong network
	// never surfaces. The whole point of dry-run is "does this
	// config actually work?" so a misconfig that only reveals
	// itself under real traffic defeats the flag.
	rdb := redisclient.Build(cfg.Storage)
	if rdb != nil {
		defer func() { _ = rdb.Close() }()
		mode := redisclient.Mode(cfg.Storage)
		if dryRun {
			pingCtx, cancelPing := context.WithTimeout(rootCtx, 5*time.Second)
			err := rdb.Ping(pingCtx).Err()
			cancelPing()
			if err != nil {
				return fmt.Errorf("redis: ping (%s mode): %w", mode, err)
			}
		}
		logger.Info("redis configured", "mode", mode)
	}

	// F-1224 (audit-2026-05-12): rebuild the SEP-10 validator with
	// the Redis-backed replay guard now that we have an `rdb`. The
	// earlier construction at line ~144 was Redis-less; that one is
	// retained because failing the env-var check there is the
	// fast-fail-on-misconfig path, but production always replaces
	// it here with a guarded validator. Skipped silently when
	// either the original construction errored (sep10Validator is
	// the Noop) or Redis is not configured (sep10Validator stays
	// guard-free; the replay defence falls open).
	if rdb != nil {
		if _, isNoop := sep10Validator.(auth.NoopSEP10Validator); !isNoop {
			guarded, err := buildSEP10Validator(cfg.API.SEP10, rdb)
			if err == nil {
				sep10Validator = guarded
				logger.Info("sep10 replay-guard wired", "store", "redis")
			} else {
				logger.Warn("sep10 replay-guard rebuild failed; falling back to non-guarded validator",
					"err", err)
			}
		}
	} else if cfg.API.AuthMode == "sep10" {
		// F-1217 (codex audit-2026-05-12): SEP-10 production
		// deployments MUST have a replay guard. A captured signed
		// challenge XDR is otherwise replayable for the
		// 15-minute window. The Redis-backed guard is the only
		// implementation today, so a Redis-less binary running
		// `auth_mode=sep10` is a misconfiguration we fail loud on
		// at startup rather than serving guard-free.
		return errors.New("sep10 replay-guard required when auth_mode=sep10: configure storage.redis_addr (see internal/auth/sep10/redisreplay.go)")
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

	// Trusted-proxy CIDRs — F-0009 remediation. The middleware
	// package's `requestCameViaTrustedProxy` consults this allow-list
	// before honouring `X-Forwarded-For`; an empty list means no
	// proxies are trusted and XFF is ignored entirely. Validation
	// already happened at config-load via internal/config/validate.go,
	// so a parse error here would be a programmer bug, not bad
	// operator input — surface as a hard startup failure.
	if err := middleware.SetTrustedProxyCIDRs(cfg.API.TrustedProxyCIDRs); err != nil {
		return fmt.Errorf("middleware.SetTrustedProxyCIDRs: %w", err)
	}
	if len(cfg.API.TrustedProxyCIDRs) > 0 {
		logger.Info("trusted-proxy CIDRs wired",
			"count", len(cfg.API.TrustedProxyCIDRs),
			"cidrs", cfg.API.TrustedProxyCIDRs)
	}

	// CORS — only wired when the operator configured allowed origins.
	// Empty list means same-origin only (no cross-origin clients).
	var cors middleware.Middleware
	if len(cfg.API.AllowedOrigins) > 0 {
		cors = middleware.CORS(middleware.CORSOptions{
			AllowedOrigins: cfg.API.AllowedOrigins,
		})
	}

	// Rate limit — separate buckets per tier (F-0008 remediation).
	// `anon` is keyed by remote IP (with Subject.Identifier when an
	// auth middleware has stamped one); `auth` is keyed per-API-key
	// or per-Subject for authenticated tiers (apikey, SEP-10). When
	// Redis is unavailable, neither bucket is constructed — the
	// middleware is omitted and the stack runs uncapped. An operator
	// who cares will see this in readyz.
	var rateLimit middleware.Middleware
	if rdb != nil && (cfg.API.AnonRateLimitPerMin > 0 || cfg.API.KeyRateLimitPerMin > 0) {
		var anonBucket, authBucket *ratelimit.Bucket
		if cfg.API.AnonRateLimitPerMin > 0 {
			anonBucket = ratelimit.New(rdb, cfg.API.AnonRateLimitPerMin, time.Minute)
		}
		if cfg.API.KeyRateLimitPerMin > 0 {
			authBucket = ratelimit.New(rdb, cfg.API.KeyRateLimitPerMin, time.Minute)
		}
		rateLimit = middleware.RateLimitBySubject(
			anonBucket,
			authBucket,
			middleware.SkipHealthAndMetrics,
			logger.With("component", "ratelimit"),
		)
		logger.Info("rate-limit tiers wired",
			"anon_per_min", cfg.API.AnonRateLimitPerMin,
			"key_per_min", cfg.API.KeyRateLimitPerMin,
			"anon_enabled", anonBucket != nil,
			"key_enabled", authBucket != nil,
		)
	}

	// Per-account usage counter — daily INCRs alongside rate-limit
	// for /v1/account/usage. F-1258 (codex audit-2026-05-12): only
	// constructed when Redis is actually wired. The previous
	// `usage.New(rdb)` with nil rdb returned a non-nil counter
	// whose Increment path dereferenced `c.rdb.TxPipeline()` and
	// panicked on the first authenticated request. The middleware
	// is now passed nil when Redis is absent and treats nil counters
	// as disabled.
	var usageCounter *usage.Counter
	if rdb != nil {
		usageCounter = usage.New(rdb)
	}

	// authMW is built later (after the dashboard bundle) so the
	// Postgres backend can borrow the same platform stores. Forward-
	// declared as nil here so the linter sees the read in v1.Options
	// before the assignment below.
	var authMW middleware.Middleware

	// Account store backs POST /v1/account/keys. Only wired when
	// Redis is reachable — without Redis there's nowhere to persist
	// the issued record. The handler then returns 503 for that
	// path; /me + /usage still serve from the request-context
	// Subject without the store.
	var accountStore v1.AccountStore
	if rdb != nil {
		accountStore = auth.NewRedisAPIKeyStore(rdb)
	}

	// Signup tracker — keyed off email-hash → key-id so a
	// duplicate POST /v1/signup with the same email returns 409
	// instead of minting a second key. Redis-backed; nil leaves
	// duplicate detection disabled (signup still works, just isn't
	// idempotent on the email).
	var signupTracker v1.SignupTracker
	if rdb != nil {
		signupTracker = auth.NewRedisSignupTracker(rdb)
	}

	// F-1232 (audit-2026-05-12): per-IP signup throttle, separate
	// from the global rate-limit middleware. Default 5/hour/IP —
	// tight enough to block bulk-mint, loose enough that an
	// operator onboarding a small team through a single shared
	// egress completes normally. Operators tune via
	// `[api].signup_ip_max_per_window` if needed.
	var signupIPThrottle v1.SignupIPThrottle
	if rdb != nil {
		signupIPThrottle = auth.NewRedisSignupIPThrottle(rdb, auth.SignupIPThrottleOptions{})
	}

	// F-1218 wave 42 + 43 (codex audit-2026-05-12): the email-
	// ownership-proof verifier. Wired only when Redis is reachable;
	// the signup handler issues a token in a future wave and the
	// /v1/signup/verify endpoint consumes it via SignupVerifier.
	// Redis-less deployments leave this nil and the verify endpoint
	// returns 503 with a clear "not configured" message.
	var signupVerifier v1.SignupVerifier
	if rdb != nil {
		signupVerifier = auth.NewRedisSignupVerifier(rdb)
	}

	// Stripe webhook handler — gated on (Redis up + signing secret
	// configured). Without the secret the handler 503s every request
	// (fail-closed; otherwise a forged event could lift any
	// identifier's keys). The Manager is the same RedisAPIKeyStore
	// used for mint-key + upgrade-key paths — Stripe-driven upgrades
	// are wire-equivalent to operator-driven ones.
	var stripeCfg *v1.StripeWebhookConfig
	if rdb != nil && cfg.API.Stripe.SigningSecret != "" {
		stripeCfg = &v1.StripeWebhookConfig{
			SigningSecret: cfg.API.Stripe.SigningSecret,
			Manager:       auth.NewRedisAPIKeyStore(rdb),
		}
		// F-1227: wire the Postgres-backed BillingStore for inbound
		// event dedupe whenever Postgres is available. Without this,
		// Stripe at-least-once delivery means a delayed re-delivery
		// of an event silently re-runs the upgrade after a manual
		// downgrade. Skipped when Postgres is absent (rare; the API
		// usually has Postgres), in which case the legacy "rely on
		// idempotent UpdateRateLimit" path stays.
		if pgDB := store.DB(); pgDB != nil {
			pgStore := postgresstore.New(pgDB)
			stripeCfg.Events = postgresstore.NewBillingStore(pgStore)
			// F-1240: durable audit row per tier upgrade. Same
			// postgres connection as the dedupe store; both target
			// the platform schema from migration 0027.
			stripeCfg.Audit = postgresstore.NewAuditStore(pgStore)
			// F-1219 (codex audit-2026-05-12): wire the platform
			// bridge so a successful Stripe upgrade also writes
			// the Subscription row and bumps the account's Tier on
			// the canonical platform stores. Without this the
			// dashboard's view of the customer's plan stays at
			// whatever it was before Stripe (typically Free) even
			// though the Redis API-key budgets were lifted. Empty
			// TierMap = the handler's default starter/pro/
			// business/enterprise mapping.
			stripeCfg.Platform = &v1.StripePlatformBridge{
				Accounts: postgresstore.NewAccountStore(pgStore),
				Billing:  postgresstore.NewBillingStore(pgStore),
				// F-1219 wave 55 (codex audit-2026-05-13):
				// fan the upgrade out to Postgres-backed
				// dashboard keys too — pre-fix only Redis-
				// stored /v1/signup keys were lifted.
				APIKeys: postgresstore.NewAPIKeyStore(pgStore),
			}
			logger.Info("stripe webhook wired", "endpoint", "/v1/webhooks/stripe", "dedupe", "postgres", "audit", "postgres", "platform", "accounts+billing+apikeys")
		} else {
			logger.Warn("stripe webhook wired without dedupe — Postgres absent",
				"endpoint", "/v1/webhooks/stripe")
		}
	} else if cfg.API.Stripe.SigningSecret != "" {
		logger.Warn("stripe webhook signing_secret set but Redis is not configured — endpoint will 503")
	}

	// Divergence lookup adapter. Only wired when Redis is reachable
	// (the worker's cached results live there). References are
	// constructed from cfg.Divergence; CoinGecko is on by default
	// (free tier, no auth required) so divergence_warning fires
	// out of the box; Chainlink is opt-in via cfg.Divergence.Chainlink.
	var divergenceLooker v1.DivergenceLooker
	if rdb != nil {
		refs := buildDivergenceReferences(cfg.Divergence, logger)
		divSvc, err := divergence.NewService(divergence.ServiceOptions{
			Cache:                rdb,
			References:           refs,
			Threshold:            cfg.Divergence.Threshold,
			MinSourcesForWarning: cfg.Divergence.MinSourcesForWarning,
			PerReferenceTimeout: time.Duration(
				cfg.Divergence.PerReferenceTimeoutSeconds) * time.Second,
		})
		if err != nil {
			return fmt.Errorf("divergence service: %w", err)
		}
		divergenceLooker = divergenceAdapter{svc: divSvc}
		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = r.Name()
		}
		logger.Info("divergence service wired",
			"reference_count", len(refs),
			"references", names,
			"threshold_pct", cfg.Divergence.Threshold,
			"min_sources_for_warning", cfg.Divergence.MinSourcesForWarning)
	}

	// Home-domain lookup chains the live LCM resolver (#298 +
	// account_observations table) with the operator-static
	// MetadataConfig map per ADR-0021. Live wins when an
	// observation exists; static fallback covers issuers the
	// observer hasn't backfilled yet OR storage transient errors.
	homeDomainLookup := metadata.ChainedHomeDomainLookup(
		metadata.NewLCMHomeDomainResolver(metadataStoreLookup{s: store}),
		cfg.Metadata.HomeDomainFor,
		func(msg string, kv ...any) {
			logger.With("component", "metadata-lcm").Warn(msg, kv...)
		},
	)

	// Freeze looker — reads the freeze:<asset>:<quote> cache
	// markers the aggregator's freeze.Writer publishes (ADR-0019
	// Phase 1 + 2 anomaly response). Without this wiring the API's
	// `flags.frozen` is permanently false regardless of what the
	// aggregator's anomaly detector decided. Nil rdb leaves the
	// looker nil, matching the rest of the redis-dependent options
	// — a deployment without Redis still serves prices, just
	// without freeze visibility.
	var freezeLooker v1.FrozenLooker
	if rdb != nil {
		fl, err := freeze.NewLooker(rdb)
		if err != nil {
			return fmt.Errorf("freeze looker: %w", err)
		}
		freezeLooker = fl
		logger.Info("freeze looker wired")
	}

	// Streaming Hub — backs /v1/price/stream's closed-bucket SSE
	// surface. Constructed unconditionally so the handler stops
	// returning 503; producer wiring (the per-pair publisher)
	// activates only when [api.streaming].pairs is non-empty.
	// L3.9 PR 2/2 wires the Redis pub/sub subscriber against this
	// Hub further down (gated on rdb != nil).
	hub := streaming.NewHub(0)

	priceReader := storePriceReader{s: store}

	// Oracle reader — Redis-cached read-through wrapper around the
	// store reader. /v1/oracle/latest's DISTINCT ON (source) sort
	// is expensive (~580 ms p95 on R1's oracle_updates volume); a
	// 30 s cache stays inside the oracle push interval and absorbs
	// polling fan-out. Falls back to direct-store reads when Redis
	// is missing.
	var oracleReader v1.OracleReader = storeOracleReader{s: store}
	if rdb != nil {
		oracleReader = cachedOracleReader{
			inner: oracleReader,
			rdb:   rdb,
			log:   logger.With("component", "oracle-cache"),
		}
		logger.Info("oracle reader wrapped with Redis cache",
			"ttl", cachekeys.OracleLatestTTL.String())
	}

	// Catalogue readers — same Redis read-through pattern for the
	// /v1/assets and /v1/markets list endpoints. Both derive from
	// 14-day-window aggregations over the trades hypertable
	// (~450-500 ms cold), so a 60 s cache absorbs polling fan-out
	// without delaying new-listing surfacing more than once-a-minute.
	var assetReader v1.AssetReader = storeAssetReader{s: store, homeDomainLookup: homeDomainLookup}
	var marketsReader v1.MarketsReader = storeMarketsReader{s: store}
	if rdb != nil {
		assetReader = cachedAssetReader{
			inner: assetReader,
			rdb:   rdb,
			log:   logger.With("component", "assets-cache"),
		}
		marketsReader = cachedMarketsReader{
			inner: marketsReader,
			rdb:   rdb,
			log:   logger.With("component", "markets-cache"),
		}
		logger.Info("catalogue readers wrapped with Redis cache",
			"ttl", cachekeys.CatalogueListTTL.String())
	}

	// Status backend — points /v1/status at a local Prometheus when
	// configured. Empty URL leaves the endpoint serving an
	// in-process surface (region label + uptime only).
	var statusBackend v1.StatusBackend
	if cfg.API.PrometheusURL != "" {
		statusBackend = &v1.PrometheusStatusBackend{
			URL:    cfg.API.PrometheusURL,
			Client: &http.Client{Timeout: 2 * time.Second},
		}
		logger.Info("status backend wired", "prometheus_url", cfg.API.PrometheusURL)
	}

	// Customer-dashboard magic-link auth + key-management surface.
	// Empty BaseURL leaves the dashboard auth flow off — the
	// routes simply aren't mounted. This is the expected pre-launch
	// shape: dashboard SPA ships standalone-preview with mocked
	// auth until operator configures Resend + app.ratesengine.net.
	dashboardBundle, err := buildDashboardBundle(cfg.API.Dashboard, store.DB(), rdb, logger)
	if err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}

	// Forex shim — periodic fetch of fiat rates from massive.com.
	// Cache is in-memory; worker installs a snapshot once per hour.
	// Backs /v1/currencies. Worker survives upstream failures
	// (logs at warn) — the cache holds the prior snapshot.
	//
	// API key comes from MASSIVE_API_KEY env var (passed through
	// systemd EnvironmentFile=/etc/default/ratesengine on r1). When
	// empty, the worker still constructs but every fetch returns
	// 401; a stale cache stays in place and /v1/currencies serves
	// "warming up" until the key is provided.
	forexCache := forex.NewCache()
	forexWorker := forex.NewWorker(
		forex.NewClient(os.Getenv("MASSIVE_API_KEY")),
		forexCache,
		logger.With("component", "forex"),
		time.Hour,
	)
	// Wire fx_quotes persistence — every refresh tick writes the
	// latest rates + 7d history to the hypertable so /v1/currencies
	// can serve historical charts beyond the in-memory window.
	forexWorker = forexWorker.WithWriter(&forexQuoteWriter{store: store})
	go func() {
		if err := forexWorker.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("forex worker exited", "err", err)
		}
	}()

	// Auth — translate the configured auth_mode + auth_backend into
	// the middleware. auth_mode=none yields nil (server stack omits
	// it; downstream code treats absence-of-Subject as anonymous).
	// Postgres backend opt-in requires the dashboard bundle (which
	// owns the platform store handles).
	authMW = buildAuthMiddleware(cfg.API.AuthMode, authValidatorOptions{
		Backend:           cfg.API.AuthBackend,
		Rdb:               rdb,
		PostgresValidator: dashboardBundle.pgValidator,
		SEP10:             sep10Validator,
	}, logger)

	// Hoist cache instances out of the Options literal so a prewarm
	// goroutine can hammer them on startup + every 25s (just inside
	// the 30s/60s TTLs) — keeps every cold-cache miss off the user's
	// path. The first /exchanges or /dexes pageload after a binary
	// restart now hits a warm cache.
	cachedSourcesStats := v1.NewCachedSourcesStatsReader(store, 60*time.Second)
	cachedMarketsReader := v1.NewCachedMarketsReader(marketsReader, 30*time.Second)
	cachedCoinsReader := v1.NewCachedCoinsReader(store, 30*time.Second)
	go prewarmCaches(rootCtx, logger.With("component", "prewarm"), cachedSourcesStats, cachedMarketsReader, cachedCoinsReader)

	usdPegs := parseUSDPeggedClassics(cfg.Trades.USDPeggedClassicAssets, logger)

	// Load the verified-currency catalogue (R-018 Phase 1.1).
	// Failure here is fatal — the seed YAML is embedded; a parse
	// error means a code change broke the build artifact, not an
	// operator misconfiguration.
	verifiedCurrencies, err := currency.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load verified-currency catalogue: %w", err)
	}
	logger.Info("verified-currency catalogue loaded", "entries", len(verifiedCurrencies.All()))

	apiSrv := v1.New(v1.Options{
		Logger:      logger.With("component", "api"),
		ReadyChecks: checks,
		Assets:      assetReader,
		Prices:      priceReader,
		History:     storeHistoryReader{s: store},
		// Wrap with a 30s TTL cache. /v1/markets and /v1/pools both
		// scan ~24h of the trades hypertable on every hit (5-10s
		// each); the explorer hits them on every page load. 30s
		// freshness is plenty for trade-volume aggregates.
		Markets:             cachedMarketsReader,
		Oracle:              oracleReader,
		Meta:                sep1Cache,
		Accounts:            accountStore,
		Signups:             signupTracker,
		SignupIPThrottle:    signupIPThrottle,
		SignupVerifier:      signupVerifier,
		SignupVerifyEmailer: signupVerifyEmailerOrNil(dashboardBundle.sender, dashboardBundle.emailFrom),
		// F-1218 wave 45 (codex audit-2026-05-12): the verify
		// handler flips the EmailVerifiedAt flag on the
		// underlying Redis-stored API key record after Consume.
		APIKeyEmailVerifier:  apiKeyEmailVerifierOrNil(rdb),
		RequireEmailVerified: requireEmailVerifiedOrNil(cfg.API.SignupRequireEmailVerification),
		Stripe:               stripeCfg,
		Divergence:           divergenceLooker,
		Confidence:           redisConfidenceLooker{rdb: rdb},
		Triangulated:         redisTriangulatedLooker{rdb: rdb},
		Freeze:               freezeLooker,
		Supply:               storeSupplyLooker{s: store},
		Volume:               storeVolumeReader{s: store},
		Change24h:            storeChange24hReader{s: store, pegs: usdPegs},
		ChangeSummary:        store,
		Coins:                cachedCoinsReader,
		Issuers:              store,
		Cursors:              store,
		NetworkStats:         store,
		// Wrap with a 60s TTL cache. The underlying SQL aggregations
		// (24h trades-hypertable scan grouped by source) take 5-10s;
		// the explorer hits these on every /dexes + /exchanges page
		// load. 60s freshness is plenty for a 24h-trailing aggregate.
		SourcesStats: cachedSourcesStats,
		Lending:      store,
		Currencies:   newForexAdapter(forexCache),
		FXHistory:    &fxHistoryReader{store: store},
		SEP10:        sep10Validator,
		Hub:          hub,
		CORS:         cors,
		Auth:         authMW,
		KeyPolicy:    middleware.KeyPolicy(),
		// F-1226 (codex audit-2026-05-12): monthly-quota enforcer.
		// Reads month-to-date counters from the same Redis Counter
		// the UsageTracker writes. Only Postgres-backed Subjects
		// carry MonthlyQuota; other validators leave it 0 and the
		// middleware short-circuits per request.
		MonthlyQuota: middleware.MonthlyQuota(usageCounter, logger.With("component", "monthly-quota")),
		RateLimit:    rateLimit,
		UsageTracker: middleware.UsageTracker(usageCounter, logger.With("component", "usage")),
		// F-1226 (codex audit-2026-05-12) wave 39 — TouchUsage half:
		// asynchronously update the api_keys row's `last_used_at` /
		// `last_used_ip` / `last_used_user_agent` columns, debounced
		// per (key, 5min) via Redis SETNX so the hot row sees at
		// most one UPDATE per window. Only wired when both Postgres
		// and Redis are present; deployments missing either fall
		// back to the legacy "no last_used updates" posture.
		TouchUsage: touchUsageMiddlewareOrNil(dashboardBundle.keysStore, rdb, logger),
		// F-1258 (codex audit-2026-05-12): only wire the UsageReader
		// adapter when the underlying counter is real. Pre-fix we
		// passed `usageReaderAdapter{c: nil}` even on Redis-less
		// deployments, which then nil-deref'd on the first
		// `/v1/account/usage` call. The handler short-circuits on
		// `usageReader == nil` with an empty list, which is the
		// correct "Redis absent → no usage data" shape.
		UsageReader:        usageReaderOrNil(usageCounter),
		CDNEnabled:         cfg.API.CDNEnabled,
		StatusBackend:      statusBackend,
		RegionName:         cfg.Region.ID,
		RegionDeployment:   "production",
		DashboardAuth:      nilOrMounter(dashboardBundle.auth),
		DashboardKeys:      nilOrMounter(dashboardBundle.keys),
		DashboardWebhooks:  nilOrMounter(dashboardBundle.webhooks),
		SessionAuth:        dashboardBundle.middleware,
		SessionPeeker:      sessionPeekerAdapter{},
		SACWrappers:        cfg.Supply.SACWrappers,
		USDPeggedClassics:  usdPegs,
		VerifiedCurrencies: verifiedCurrencies,
		GlobalPrice: globalPriceReader{
			s:   store,
			tri: redisTriangulatedLooker{rdb: rdb},
			pkPairFor: func(base, quote canonical.Asset) (canonical.Pair, error) {
				return canonical.NewPair(base, quote)
			},
		},
		GlobalPriceOpts: aggregate.GlobalPriceOptions{
			AggregatorSources: external.AggregatorSources(),
		},
	})

	// Closed-bucket producer — only spawn when the operator
	// configured pairs to broadcast. Empty pair list is a valid
	// deployment (Hub still constructs; subscribers connect and
	// receive heartbeats with no events). Bad pair strings fail
	// loud at startup rather than silently dropping a pair.
	streamPairs, err := parseStreamingPairs(cfg.API.Streaming.Pairs)
	if err != nil {
		return fmt.Errorf("api.streaming.pairs: %w", err)
	}
	if len(streamPairs) > 0 {
		pub := streampublish.New(hub, priceReader, cfg.API.Streaming.PollInterval, logger.With("component", "stream-publisher"))
		go func() {
			if err := pub.Run(rootCtx, streamPairs); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("stream publisher exited", "err", err)
			}
		}()
		logger.Info("stream publisher running", "pairs", len(streamPairs), "interval", cfg.API.Streaming.PollInterval)
	} else {
		logger.Info("stream publisher disabled (no pairs configured); /v1/price/stream serves heartbeats only")
	}

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

	// Run the closed-bucket subscriber alongside the HTTP server.
	// Bound to rootCtx — SIGINT/SIGTERM cancels both the server and
	// the subscriber together. Run errors don't take the API down
	// (the rest of the surface keeps serving); they log + leave the
	// stream endpoint serving 503 implicitly via the Hub falling
	// silent.
	// F-1238 (codex audit-2026-05-12): gate the subscriber on
	// `rdb != nil`. The Hub is always non-nil because streaming.NewHub
	// is called unconditionally a few hundred lines above; using
	// `hub != nil` here meant a Redis-less deployment passed the
	// gate, then `redispub.NewSubscriber(nil, ...)` returned
	// "RedisSubscriber is required" and aborted startup. Every
	// other Redis-backed feature in this file gates on `rdb != nil`;
	// streaming should too. Without Redis the Hub stays silent —
	// `/v1/price/stream` serves heartbeats but no closed-bucket
	// events, matching the documented "Redis optional at API
	// layer" posture.
	if rdb != nil && hub != nil {
		sub, err := redispub.NewSubscriber(rdb, redispub.DefaultChannel, hub, logger.With("component", "stream-sub"))
		if err != nil {
			return fmt.Errorf("redispub subscriber: %w", err)
		}
		go func() {
			if err := sub.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("stream subscriber exited",
					"channel", sub.Channel(), "err", err)
			}
		}()
	}

	// Customer-webhook delivery worker (F-1270). Drains the
	// queue every 5s, HMAC-signs payloads, POSTs to customer URLs
	// with exponential backoff on 5xx/network errors. Wired only
	// when the dashboard webhook store came up (i.e. Postgres is
	// reachable + the dashboard surface is enabled); leaves
	// non-dashboard deployments unaffected.
	if dashboardBundle.webhookStore != nil {
		worker := customerwebhook.New(dashboardBundle.webhookStore, customerwebhook.Options{
			Logger: logger.With("component", "customer-webhook"),
		})
		go func() {
			if err := worker.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("customer-webhook worker exited",
					"err", err)
			}
		}()
		logger.Info("customer-webhook delivery worker started")
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

// authValidatorOptions carries the wiring buildAuthMiddleware
// needs beyond the mode string — the backend choice + the
// already-built Postgres validator (when backend=postgres).
type authValidatorOptions struct {
	Backend           string // "redis" (default) or "postgres"
	Rdb               redis.UniversalClient
	PostgresValidator *auth.PostgresAPIKeyValidator // non-nil when dashboard wired
	SEP10             auth.SEP10Validator
}

// buildAuthMiddleware translates the configured auth_mode into a
// concrete middleware. Returns nil for mode=none — the server stack
// omits absent middleware entirely so anonymous traffic doesn't
// pay a per-request closure cost.
//
// auth_mode=apikey requires a working API-key validator (Redis or
// Postgres). auth_mode=sep10 wires the same validator as the
// /v1/auth/sep10/* endpoints. The Postgres backend additionally
// requires the platform stores; missing them falls back to Noop
// (every request 503s — the correct fail-loud behaviour for a
// deployment that opted in to the postgres backend without the
// platform tables wired).
func buildAuthMiddleware(mode string, opts authValidatorOptions, logger *slog.Logger) middleware.Middleware {
	switch mode {
	case "", "none":
		return nil
	case "sep10":
		return middleware.Auth(middleware.AuthOptions{
			Mode:  middleware.AuthModeSEP10,
			SEP10: opts.SEP10,
		})
	case "apikey":
		return middleware.Auth(middleware.AuthOptions{
			Mode:   middleware.AuthModeAPIKey,
			APIKey: buildAPIKeyValidator(opts, logger, "apikey"),
		})
	case "apikey_optional":
		return middleware.Auth(middleware.AuthOptions{
			Mode:   middleware.AuthModeAPIKeyOptional,
			APIKey: buildAPIKeyValidator(opts, logger, "apikey_optional"),
		})
	}
	logger.Error("unknown auth_mode — server falling through to no-auth", "mode", mode)
	return nil
}

// buildAPIKeyValidator picks Redis vs Postgres backend based on
// auth_backend config. Postgres falls back to Noop (every request
// 503s) when the dashboard bundle wasn't wired — fail loud rather
// than silently demote.
func buildAPIKeyValidator(opts authValidatorOptions, logger *slog.Logger, modeName string) auth.APIKeyValidator {
	switch opts.Backend {
	case "postgres":
		if opts.PostgresValidator == nil {
			logger.Error("auth_backend=postgres but the dashboard bundle is not wired — every request will 503",
				"mode", modeName,
				"reason", "set api.dashboard.base_url to enable the Postgres backend (the bundle owns the platform store handles the validator borrows)")
			return auth.NoopAPIKeyValidator{}
		}
		logger.Info("auth: apikey validator wired",
			"mode", modeName, "backend", "postgres",
			"cache", opts.Rdb != nil)
		return opts.PostgresValidator
	default:
		// "redis" or unset — default to the legacy backend.
		if opts.Rdb == nil {
			logger.Error("auth_backend=redis but Redis is not configured — every request will 503",
				"mode", modeName,
				"reason", "RedisAPIKeyValidator requires a Redis client")
			return auth.NoopAPIKeyValidator{}
		}
		logger.Info("auth: apikey validator wired",
			"mode", modeName, "backend", "redis")
		return auth.NewRedisAPIKeyValidator(opts.Rdb)
	}
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
// nilOrMounter returns nil-typed nil when the supplied
// concrete *Handlers pointer is nil, otherwise returns it as
// the v1.DashboardAuthMounter interface.
//
// Naked assignment (`opts.DashboardKeys = bundle.keys`) wraps a
// typed-nil pointer in a non-nil interface, so server.go's
// `if s.dashboardKeys != nil` would mount routes whose handlers
// then panic on first request when they dereference cfg. This
// helper sidesteps the Go interface-nil-vs-pointer-nil gotcha.
func nilOrMounter[T v1.DashboardAuthMounter](h T) v1.DashboardAuthMounter {
	// Generic constraint catches both *dashboardauth.Handlers and
	// *dashboardkeys.Handlers without runtime reflection.
	var zero T
	if any(h) == any(zero) {
		return nil
	}
	return h
}

// dashboardBundle bundles the dashboard wirings main.go threads
// into v1.Options — the auth handlers, the keys handlers, and
// the session-resolving middleware that runs in the global
// request stack so dashboardkeys.HandleList et al can read the
// session context.
//
// The platform stores ride along too so the Postgres-backed auth
// validator (cfg.API.AuthBackend == "postgres") can borrow the
// same handles instead of opening a second connection pool.
type dashboardBundle struct {
	auth         *dashboardauth.Handlers
	keys         *dashboardkeys.Handlers
	webhooks     *dashboardwebhooks.Handlers
	webhookStore platform.WebhookStore
	middleware   middleware.Middleware
	keysStore    platform.APIKeyStore
	accounts     platform.AccountStore
	pgValidator  *auth.PostgresAPIKeyValidator
	// sender + emailFrom are exported so the public-API signup
	// flow (F-1218 wave 44) can re-use the same Resend / Noop
	// transport the dashboard auth flow uses, without having to
	// build a second sender at the top level.
	sender    notify.Sender
	emailFrom string
}

// buildDashboardBundle wires the customer-dashboard magic-link
// auth flow + the key-management surface. Returns a bundle with
// all-nil fields (and a logged warn) when the operator hasn't
// configured BaseURL — the API still serves; /v1/auth/* and
// /v1/dashboard/* routes simply aren't mounted.
//
// When BaseURL is configured but the Resend env var is unset or
// empty, the sender falls back to a NoopSender. The flow still
// works end-to-end: a CreateMagicLinkToken row lands in Postgres,
// the rendered email is silently dropped, and the operator can
// look up the plaintext from the API's structured logs to test
// the callback path. This is the expected dev/local default;
// production sets the env var.
func buildDashboardBundle(cfg config.DashboardConfig, db *sql.DB, rdb redis.UniversalClient, logger *slog.Logger) (dashboardBundle, error) {
	if cfg.BaseURL == "" {
		logger.Warn("dashboard not wired (api.dashboard.base_url is empty); /v1/auth/* + /v1/dashboard/* will 404")
		return dashboardBundle{}, nil
	}
	if db == nil {
		return dashboardBundle{}, errors.New("dashboard requires a Postgres connection")
	}

	pg := postgresstore.New(db)
	accounts := postgresstore.NewAccountStore(pg)
	users := postgresstore.NewUserStore(pg)
	tokens := postgresstore.NewTokenStore(pg)
	keysStore := postgresstore.NewAPIKeyStore(pg)

	var sender notify.Sender
	apiKey := os.Getenv(cfg.ResendAPIKeyEnv)
	if apiKey == "" {
		logger.Warn("dashboard auth using NoopSender — magic-link emails will be dropped",
			"reason", fmt.Sprintf("env %s is unset/empty", cfg.ResendAPIKeyEnv))
		sender = &notify.NoopSender{}
	} else {
		s, err := notify.NewResendSender(apiKey)
		if err != nil {
			return dashboardBundle{}, fmt.Errorf("resend sender: %w", err)
		}
		sender = s
		logger.Info("dashboard auth using Resend sender", "from", cfg.EmailFrom)
	}

	authCfg := dashboardauth.Config{
		Accounts:         accounts,
		Users:            users,
		Tokens:           tokens,
		Sender:           sender,
		Logger:           logger.With("component", "dashboard-auth"),
		DashboardBaseURL: cfg.BaseURL,
		EmailFrom:        cfg.EmailFrom,
		MagicLinkTTL:     time.Duration(cfg.MagicLinkTTLMinutes) * time.Minute,
		SessionTTL:       time.Duration(cfg.SessionTTLDays) * 24 * time.Hour,
		CookieSecure:     cfg.CookieSecure,
		CookieDomain:     cfg.CookieDomain,
	}
	// F-1255 (codex audit-2026-05-12): per-email signup lock. Redis-
	// backed SETNX serialises first-login provisioning so two
	// callback callers for the same just-verified email can't both
	// create speculative Account rows. Redis-less deployments leave
	// the locker nil and fall back to the Suspend-on-conflict
	// recovery path (still safe; the orphan row gets reaped).
	if rdb != nil {
		authCfg.EmailLocker = auth.NewRedisSignupEmailLocker(rdb)
	}
	authH, err := dashboardauth.NewHandlers(authCfg)
	if err != nil {
		return dashboardBundle{}, fmt.Errorf("dashboard auth handlers: %w", err)
	}
	// Optional Postgres-backed runtime auth validator. Constructed
	// here so the dashboard's Revoke handler can call its
	// InvalidateCachedKey after a successful soft-delete; main.go
	// also re-uses it when cfg.API.AuthBackend == "postgres".
	pgValidator, err := auth.NewPostgresAPIKeyValidator(auth.PostgresValidatorOptions{
		Keys:     keysStore,
		Accounts: accounts,
		Cache:    rdb,
	})
	if err != nil {
		return dashboardBundle{}, fmt.Errorf("postgres auth validator: %w", err)
	}

	keysH, err := dashboardkeys.NewHandlers(dashboardkeys.Config{
		Keys:             keysStore,
		CacheInvalidator: pgValidator,
		Logger:           logger.With("component", "dashboard-keys"),
	})
	if err != nil {
		return dashboardBundle{}, fmt.Errorf("dashboard keys handlers: %w", err)
	}

	// F-1270: dashboard webhook handlers atop the same Postgres
	// store the delivery worker reads. Single store handle threads
	// through both surfaces: dashboard handlers (CRUD) and the
	// delivery worker (drain queue). The worker runs as a goroutine
	// in this binary's main() — see the `customer-webhook delivery
	// worker` block there.
	webhookStore := postgresstore.NewWebhookStore(postgresstore.New(db))
	webhooksH, err := dashboardwebhooks.NewHandlers(dashboardwebhooks.Config{
		Webhooks: webhookStore,
		Logger:   logger.With("component", "dashboard-webhooks"),
	})
	if err != nil {
		return dashboardBundle{}, fmt.Errorf("dashboard webhooks handlers: %w", err)
	}
	logger.Info("dashboard wired",
		"base_url", cfg.BaseURL,
		"magic_link_ttl_minutes", cfg.MagicLinkTTLMinutes,
		"session_ttl_days", cfg.SessionTTLDays,
		"cookie_secure", cfg.CookieSecure)

	// authCfg drives both the handlers AND the resolver
	// middleware — the latter needs the same Accounts / Users /
	// Tokens stores to resolve the cookie on every request.
	return dashboardBundle{
		auth:         authH,
		keys:         keysH,
		webhooks:     webhooksH,
		webhookStore: webhookStore,
		middleware:   middleware.Middleware(dashboardauth.Middleware(&authCfg)),
		keysStore:    keysStore,
		accounts:     accounts,
		pgValidator:  pgValidator,
		sender:       sender,
		emailFrom:    cfg.EmailFrom,
	}, nil
}

func buildSEP10Validator(cfg config.SEP10Config, rdb redis.UniversalClient) (auth.SEP10Validator, error) {
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

	// F-1224 (audit-2026-05-12): wire the Redis-backed replay
	// guard whenever the API has Redis (i.e. always in production).
	// Without it, a captured signed challenge XDR is reusable for
	// the full ChallengeTTL window — long enough for an attacker
	// who steals one signed XDR (e.g. via XSS exfil) to mint a
	// stream of JWTs after the user closes the tab.
	var replayGuard sep10.ReplayGuard
	if rdb != nil {
		replayGuard = sep10.NewRedisReplayGuard(rdb)
	}

	v, err := sep10.NewValidator(sep10.Options{
		ServerSeed:        seed,
		NetworkPassphrase: network,
		WebAuthDomain:     cfg.WebAuthDomain,
		HomeDomain:        cfg.HomeDomain,
		ChallengeTTL:      cfg.ChallengeTTL,
		JWTTTL:            cfg.JWTTTL,
		JWTSecret:         []byte(jwtSecret),
		ReplayGuard:       replayGuard,
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

// buildDivergenceReferences turns DivergenceConfig into the
// concrete []divergence.Reference list the service consumes.
//
// CoinGecko is on by default (free tier, no auth required); when
// the operator's IDMap is empty, the reference falls back to the
// built-in defaults inside CoinGeckoReference (covers XLM + major
// stables).
//
// Chainlink is on only when both Enabled=true AND a non-empty
// FeedMap is set. An empty FeedMap with Enabled=true logs a WARN
// and skips Chainlink rather than wiring it as a no-op (every
// LookupPrice call would return ErrAssetUnsupported, which is
// noisy and a misconfiguration signal).
func buildDivergenceReferences(cfg config.DivergenceConfig, logger *slog.Logger) []divergence.Reference {
	var refs []divergence.Reference

	if cfg.CoinGecko.Enabled {
		refs = append(refs, divergence.NewCoinGeckoReference(divergence.CoinGeckoOptions{
			BaseURL: cfg.CoinGecko.BaseURL,
			IDMap:   cfg.CoinGecko.IDMap,
		}))
	}

	if cfg.Chainlink.Enabled {
		if len(cfg.Chainlink.FeedMap) == 0 {
			logger.Warn("divergence: chainlink enabled but FeedMap is empty — skipping")
		} else {
			feedMap := make(map[string]divergence.ChainlinkFeed, len(cfg.Chainlink.FeedMap))
			for pair, f := range cfg.Chainlink.FeedMap {
				feedMap[pair] = divergence.ChainlinkFeed{
					Address:  f.Address,
					Decimals: f.Decimals,
					Invert:   f.Invert,
				}
			}
			refs = append(refs, divergence.NewChainlinkReference(divergence.ChainlinkOptions{
				RPCURL:  cfg.Chainlink.RPCURL,
				FeedMap: feedMap,
			}))
		}
	}

	return refs
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
//
// Postgres is critical — every request that returns trade /
// aggregate / supply data reads from Timescale. There's no
// fallback path; a Postgres outage really does mean the API
// can't serve. Critical()==true so /readyz returns 503 when
// Postgres is unreachable.
type storeChecker struct{ s *timescale.Store }

func (c storeChecker) Name() string   { return "postgres" }
func (c storeChecker) Critical() bool { return true }
func (c storeChecker) Ping(ctx context.Context) error {
	return c.s.DB().PingContext(ctx)
}

// redisChecker adapts redis.UniversalClient to the v1.ReadyChecker
// interface. Redis is non-critical at API layer — cache misses
// fall back to Timescale per ADR-0007, so a Redis outage degrades
// latency (every read becomes a Timescale query instead of a
// Redis read) but does NOT break correctness. UniversalClient
// (vs typed Client) lets the same adapter work against both the
// dev single-node and production Sentinel-backed FailoverClient.
//
// F-1275 (codex audit-2026-05-13): Critical()==false so a Redis
// outage produces a 200 with status="degraded" from /v1/readyz
// instead of a 503; HAProxy keeps the backend in service while
// operators see the degradation in the response body.
type redisChecker struct{ rdb redis.UniversalClient }

func (c redisChecker) Name() string   { return "redis" }
func (c redisChecker) Critical() bool { return false }
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
	detail := assetToDetail(a, r.homeDomainLookup)

	// Best-effort F2 enrichment from the per-asset stats lookup
	// — same data the /v1/coins listing carries. Failures here
	// don't break the detail response; the field stays null and
	// the rest of the body still serves cleanly. The proper
	// supply pipeline (asset_supply_history) will overwrite
	// these when it has a snapshot — populateF2Fields runs
	// AFTER us in the handler stack.
	if stats, err := r.s.LatestAssetStats(ctx, a.String()); err == nil {
		if detail.VolumeUSD24h == nil && stats.Volume24hUSD != nil {
			detail.VolumeUSD24h = stats.Volume24hUSD
		}
		if detail.CirculatingSupply == nil && stats.CirculatingSupply != nil {
			detail.CirculatingSupply = stats.CirculatingSupply
		}
		if detail.MarketCapUSD == nil && stats.MarketCapUSD != nil {
			detail.MarketCapUSD = stats.MarketCapUSD
		}
	}
	return detail, nil
}

// storeMarketsReader adapts *timescale.Store to v1.MarketsReader.
// Translates timescale.Market (typed Pair) to v1.Market (string
// wire shape) so the API layer owns its own schema.
type storeMarketsReader struct{ s *timescale.Store }

func (r storeMarketsReader) DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	rows, next, err := r.s.DistinctPairsExt(ctx, cursor, limit, order)
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
			Volume24hUSD:  m.Volume24hUSD,
			LastPrice:     m.LastPrice,
		}
	}
	return out, next, nil
}

func (r storeMarketsReader) SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	rows, next, err := r.s.SourceMarkets(ctx, source, cursor, limit, order)
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
			Volume24hUSD:  m.Volume24hUSD,
			LastPrice:     m.LastPrice,
		}
	}
	return out, next, nil
}

func (r storeMarketsReader) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	rows, next, err := r.s.AssetMarkets(ctx, asset, cursor, limit, order)
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
			Volume24hUSD:  m.Volume24hUSD,
			LastPrice:     m.LastPrice,
		}
	}
	return out, next, nil
}

func (r storeMarketsReader) AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Pool, string, error) {
	rows, next, err := r.s.AllPools(ctx, filter, cursor, limit, order)
	if err != nil {
		return nil, "", err
	}
	out := make([]v1.Pool, len(rows))
	for i, p := range rows {
		out[i] = v1.Pool{
			Source:        p.Source,
			Base:          p.Pair.Base.String(),
			Quote:         p.Pair.Quote.String(),
			LastTradeAt:   p.LastTradeAt,
			TradeCount24h: p.TradeCount24h,
			Volume24hUSD:  p.Volume24hUSD,
			LastPrice:     p.LastPrice,
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
		Volume24hUSD:  m.Volume24hUSD,
		LastPrice:     m.LastPrice,
	}, true, nil
}

func (r storeMarketsReader) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return r.s.GetPairsVolumeHistory24hBatch(ctx, pairs)
}

// storeOracleReader adapts *timescale.Store to v1.OracleReader.
type storeOracleReader struct{ s *timescale.Store }

func (r storeOracleReader) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return r.s.LatestOracleUpdatesForAsset(ctx, asset, sourceFilter)
}

func (r storeOracleReader) LatestOracleUpdatesForAssets(ctx context.Context, assets []canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return r.s.LatestOracleUpdatesForAssets(ctx, assets, sourceFilter)
}

func (r storeOracleReader) LatestOracleStreams(ctx context.Context) ([]canonical.OracleUpdate, error) {
	return r.s.LatestOracleStreams(ctx)
}

// cachedOracleReader wraps an inner OracleReader with a Redis
// read-through cache. The inner DISTINCT ON (source) sort is
// expensive (~580 ms p95 on R1's oracle_updates volume); the
// reading only refreshes every 1–5 minutes, so a 30 s Redis
// entry absorbs the polling fan-out without delaying customer-
// facing freshness in any meaningful way.
//
// Cache miss: hit the inner reader, then SET. Cache hit: deserialise
// and skip the DB. Errors on either side fall through to the inner
// reader — never fail open.
type cachedOracleReader struct {
	inner v1.OracleReader
	rdb   redis.UniversalClient
	log   *slog.Logger
}

func (r cachedOracleReader) LatestOracleUpdatesForAsset(ctx context.Context, asset canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	return r.LatestOracleUpdatesForAssets(ctx, []canonical.Asset{asset}, sourceFilter)
}

func (r cachedOracleReader) LatestOracleUpdatesForAssets(ctx context.Context, assets []canonical.Asset, sourceFilter string) ([]canonical.OracleUpdate, error) {
	if r.rdb == nil {
		return r.inner.LatestOracleUpdatesForAssets(ctx, assets, sourceFilter)
	}

	keys := make([]string, len(assets))
	for i, a := range assets {
		keys[i] = a.String()
	}
	cacheKey := cachekeys.OracleLatest(keys, sourceFilter)

	raw, err := r.rdb.Get(ctx, cacheKey).Bytes()
	switch {
	case err == nil:
		var out []canonical.OracleUpdate
		if jerr := json.Unmarshal(raw, &out); jerr == nil {
			return out, nil
		} else {
			// Bad payload — log and re-read; don't fail the request
			// on a cache deserialisation glitch.
			r.log.Warn("oracle cache decode failed; falling through to DB",
				"key", cacheKey, "err", jerr)
		}
	case errors.Is(err, redis.Nil):
		// miss — proceed to DB
	default:
		r.log.Warn("oracle cache read failed; falling through to DB",
			"key", cacheKey, "err", err)
	}

	updates, err := r.inner.LatestOracleUpdatesForAssets(ctx, assets, sourceFilter)
	if err != nil {
		return nil, err
	}
	if buf, jerr := json.Marshal(updates); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.OracleLatestTTL).Err(); serr != nil {
			r.log.Warn("oracle cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return updates, nil
}

// LatestOracleStreams pass-through — the underlying scan is one
// query against oracle_updates with DISTINCT ON. Cheap enough to
// skip the cache layer at this volume; revisit if the page becomes
// a hot endpoint.
func (r cachedOracleReader) LatestOracleStreams(ctx context.Context) ([]canonical.OracleUpdate, error) {
	return r.inner.LatestOracleStreams(ctx)
}

// cachedAssetReader / cachedMarketsReader — Redis read-through
// caches for the catalogue list endpoints. Same shape as
// cachedOracleReader: deserialise on hit, hit-the-DB-then-SET on
// miss, fall through on error. Single-asset / single-pair lookups
// pass through unchanged — they're already fast and benefit less
// from caching.

type listCachePayload[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next"`
}

type cachedAssetReader struct {
	inner v1.AssetReader
	rdb   redis.UniversalClient
	log   *slog.Logger
}

func (r cachedAssetReader) GetAsset(ctx context.Context, a canonical.Asset) (v1.AssetDetail, error) {
	return r.inner.GetAsset(ctx, a)
}

func (r cachedAssetReader) ListAssets(ctx context.Context, cursor string, limit int) ([]v1.AssetDetail, string, error) {
	if r.rdb == nil {
		return r.inner.ListAssets(ctx, cursor, limit)
	}
	cacheKey := cachekeys.AssetsList(cursor, limit)
	if raw, err := r.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var p listCachePayload[v1.AssetDetail]
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return p.Items, p.NextCursor, nil
		}
		r.log.Warn("assets cache decode failed", "key", cacheKey)
	} else if !errors.Is(err, redis.Nil) {
		r.log.Warn("assets cache read failed", "key", cacheKey, "err", err)
	}

	items, next, err := r.inner.ListAssets(ctx, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	if buf, jerr := json.Marshal(listCachePayload[v1.AssetDetail]{Items: items, NextCursor: next}); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.CatalogueListTTL).Err(); serr != nil {
			r.log.Warn("assets cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return items, next, nil
}

type cachedMarketsReader struct {
	inner v1.MarketsReader
	rdb   redis.UniversalClient
	log   *slog.Logger
}

func (r cachedMarketsReader) PairMarket(ctx context.Context, base, quote canonical.Asset) (v1.Market, bool, error) {
	return r.inner.PairMarket(ctx, base, quote)
}

// GetPairsVolumeHistory24hBatch — pass-through. The query runs at
// page granularity (max 500 pairs) and the result depends on the
// 24h time window; not worth caching since invalidation tracks
// every minute boundary.
func (r cachedMarketsReader) GetPairsVolumeHistory24hBatch(ctx context.Context, pairs [][2]string) (map[string][]timescale.PairVolumePoint, error) {
	return r.inner.GetPairsVolumeHistory24hBatch(ctx, pairs)
}

func (r cachedMarketsReader) AllPools(ctx context.Context, filter timescale.PoolsFilter, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Pool, string, error) {
	// Pools queries are heavy (group by source × pair); cache
	// follows the same TTL as the markets list. Cache key
	// includes the filter so pools-with-DEX-filter, pools-by-pair,
	// and unfiltered pools don't collide.
	if r.rdb == nil {
		return r.inner.AllPools(ctx, filter, cursor, limit, order)
	}
	srcKey := strings.Join(filter.Sources, ",")
	cacheKey := cachekeys.MarketsList(cursor, limit) + ":order=" + marketsOrderKey(order) + ":pools=1:src=" + srcKey + ":base=" + filter.Base + ":quote=" + filter.Quote + ":asset=" + filter.Asset
	if raw, err := r.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var p listCachePayload[v1.Pool]
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return p.Items, p.NextCursor, nil
		}
		r.log.Warn("pools cache decode failed", "key", cacheKey)
	} else if !errors.Is(err, redis.Nil) {
		r.log.Warn("pools cache read failed", "key", cacheKey, "err", err)
	}
	items, next, err := r.inner.AllPools(ctx, filter, cursor, limit, order)
	if err != nil {
		return nil, "", err
	}
	if buf, jerr := json.Marshal(listCachePayload[v1.Pool]{Items: items, NextCursor: next}); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.CatalogueListTTL).Err(); serr != nil {
			r.log.Warn("pools cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return items, next, nil
}

func (r cachedMarketsReader) SourceMarkets(ctx context.Context, source, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	// Per-source markets share the same cache shape as
	// DistinctPairsExt but partition by source so a source's pool
	// list isn't aliased with the global one.
	if r.rdb == nil {
		return r.inner.SourceMarkets(ctx, source, cursor, limit, order)
	}
	cacheKey := cachekeys.MarketsList(cursor, limit) + ":order=" + marketsOrderKey(order) + ":source=" + source
	if raw, err := r.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var p listCachePayload[v1.Market]
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return p.Items, p.NextCursor, nil
		}
		r.log.Warn("source-markets cache decode failed", "key", cacheKey)
	} else if !errors.Is(err, redis.Nil) {
		r.log.Warn("source-markets cache read failed", "key", cacheKey, "err", err)
	}

	items, next, err := r.inner.SourceMarkets(ctx, source, cursor, limit, order)
	if err != nil {
		return nil, "", err
	}
	if buf, jerr := json.Marshal(listCachePayload[v1.Market]{Items: items, NextCursor: next}); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.CatalogueListTTL).Err(); serr != nil {
			r.log.Warn("source-markets cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return items, next, nil
}

func (r cachedMarketsReader) AssetMarkets(ctx context.Context, asset, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	// Per-asset markets share the same cache shape as
	// DistinctPairsExt but partition by asset so an asset's
	// involvement list isn't aliased with the global one.
	if r.rdb == nil {
		return r.inner.AssetMarkets(ctx, asset, cursor, limit, order)
	}
	cacheKey := cachekeys.MarketsList(cursor, limit) + ":order=" + marketsOrderKey(order) + ":asset=" + asset
	if raw, err := r.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var p listCachePayload[v1.Market]
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return p.Items, p.NextCursor, nil
		}
		r.log.Warn("asset-markets cache decode failed", "key", cacheKey)
	} else if !errors.Is(err, redis.Nil) {
		r.log.Warn("asset-markets cache read failed", "key", cacheKey, "err", err)
	}

	items, next, err := r.inner.AssetMarkets(ctx, asset, cursor, limit, order)
	if err != nil {
		return nil, "", err
	}
	if buf, jerr := json.Marshal(listCachePayload[v1.Market]{Items: items, NextCursor: next}); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.CatalogueListTTL).Err(); serr != nil {
			r.log.Warn("asset-markets cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return items, next, nil
}

func (r cachedMarketsReader) DistinctPairsExt(ctx context.Context, cursor string, limit int, order timescale.MarketsOrder) ([]v1.Market, string, error) {
	if r.rdb == nil {
		return r.inner.DistinctPairsExt(ctx, cursor, limit, order)
	}
	cacheKey := cachekeys.MarketsList(cursor, limit) + ":order=" + marketsOrderKey(order)
	if raw, err := r.rdb.Get(ctx, cacheKey).Bytes(); err == nil {
		var p listCachePayload[v1.Market]
		if jerr := json.Unmarshal(raw, &p); jerr == nil {
			return p.Items, p.NextCursor, nil
		}
		r.log.Warn("markets cache decode failed", "key", cacheKey)
	} else if !errors.Is(err, redis.Nil) {
		r.log.Warn("markets cache read failed", "key", cacheKey, "err", err)
	}

	items, next, err := r.inner.DistinctPairsExt(ctx, cursor, limit, order)
	if err != nil {
		return nil, "", err
	}
	if buf, jerr := json.Marshal(listCachePayload[v1.Market]{Items: items, NextCursor: next}); jerr == nil {
		if serr := r.rdb.Set(ctx, cacheKey, buf, cachekeys.CatalogueListTTL).Err(); serr != nil {
			r.log.Warn("markets cache write failed", "key", cacheKey, "err", serr)
		}
	}
	return items, next, nil
}

func marketsOrderKey(o timescale.MarketsOrder) string {
	switch o {
	case timescale.MarketsOrderVolume24hDesc:
		return "vol_desc"
	default:
		return "pair"
	}
}

// redisConfidenceLooker adapts the shared Redis client to
// v1.ConfidenceLooker by reading the JSON-encoded confidence.Score
// the aggregator writes at `confidence:<base>:<quote>:<window>`.
//
// Cache miss → (zero, false, nil); the v1 handler then leaves the
// confidence fields off the wire for that response. Read errors
// propagate so the handler can log; the response still ships
// without confidence.
type redisConfidenceLooker struct{ rdb redis.UniversalClient }

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
type redisTriangulatedLooker struct{ rdb redis.UniversalClient }

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

// globalPriceReader adapts *timescale.Store + the existing Redis
// triangulated looker to aggregate.GlobalPriceReader (R-018 Phase
// 1.4a). Each method maps to one tier of ComputeGlobalPrice:
//
//   - LatestVWAP → Store.LatestClosedVWAP1mForPair (tier 1)
//   - LatestAggregatorPrices → Store.LatestAggregatorPricesForPair (tier 2)
//   - LookupTriangulated → wraps redisTriangulatedLooker (tier 3)
//
// Constructed once at startup and passed via v1.Options.GlobalPrice.
type globalPriceReader struct {
	s         *timescale.Store
	tri       redisTriangulatedLooker
	pkPairFor func(base, quote canonical.Asset) (canonical.Pair, error)
}

func (g globalPriceReader) LatestVWAP(ctx context.Context, base, quote canonical.Asset) (string, time.Time, int64, []string, bool, error) {
	pair, err := g.pkPairFor(base, quote)
	if err != nil {
		// Invalid pair (e.g. quote == base, or any other allow-list
		// violation) is the same as "no data" from this seam's
		// perspective — caller falls through to the aggregator tier.
		return "", time.Time{}, 0, nil, false, nil //nolint:nilerr // intentional: invalid pair → "no data" from this tier
	}
	row, err := g.s.LatestClosedVWAP1mForPair(ctx, pair)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, 0, nil, false, nil
	}
	if err != nil {
		return "", time.Time{}, 0, nil, false, err
	}
	// row.Bucket is the bucket's *start*; the closed-bucket contract
	// (ADR-0015) means the bucket's served observation_at is the
	// bucket end. Add one minute to surface the consumer-facing
	// timestamp matching every other closed-bucket surface.
	asOf := row.Bucket.Add(time.Minute)
	return row.VWAP, asOf, row.TradeCount, row.Sources, true, nil
}

func (g globalPriceReader) LatestAggregatorPrices(ctx context.Context, base, quote canonical.Asset, sources []string) ([]canonical.OracleUpdate, error) {
	return g.s.LatestAggregatorPricesForPair(ctx, base, quote, sources)
}

func (g globalPriceReader) LookupTriangulated(ctx context.Context, base, quote canonical.Asset, window time.Duration) (string, time.Time, bool, error) {
	val, isTri, found, err := g.tri.LookupTriangulatedVWAP(ctx, base, quote, window)
	if err != nil || !found || !isTri {
		// `found && !isTri` means the cache had a direct (non-
		// triangulated) value — per the marker contract we shouldn't
		// serve that as the triangulation tier. Tell the caller the
		// tier missed.
		return "", time.Time{}, false, err
	}
	// The Redis cache doesn't store an observed-at timestamp alongside
	// the VWAP value (the producer's `vwap:` key is a bare string).
	// Use the request time as the as_of — the cache's TTL means the
	// value is at most the configured window old, so this is an
	// upper-bound approximation rather than the exact observation
	// timestamp. Future improvement: have the producer also write a
	// sibling `:observed_at` key.
	return val, time.Now().UTC(), true, nil
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
	return convertHistoryPoints(rows), nil
}

// HistoryPointsInRange adapts [timescale.Store.HistoryPointsInRange]
// to the v1.HistoryReader interface. Same translation rules as
// [storeHistoryReader.HistoryPoints]; passes the from/to window
// through to the storage layer.
func (r storeHistoryReader) HistoryPointsInRange(ctx context.Context, pair canonical.Pair, granularity string, from, to time.Time, limit int) ([]v1.HistoryPoint, error) {
	g := timescale.HistoryGranularity(granularity)
	if err := g.Validate(); err != nil {
		return nil, v1.ErrUnknownGranularity
	}
	rows, err := r.s.HistoryPointsInRange(ctx, pair, g, from, to, limit)
	if err != nil {
		return nil, err
	}
	return convertHistoryPoints(rows), nil
}

func convertHistoryPoints(rows []timescale.HistoryPoint) []v1.HistoryPoint {
	out := make([]v1.HistoryPoint, len(rows))
	for i, row := range rows {
		out[i] = v1.HistoryPoint{
			Bucket:    row.Bucket,
			VWAP:      row.VWAP,
			VolumeUSD: row.VolumeUSD,
		}
	}
	return out
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

	// Fast-path the synthetic-fiat case: no on-chain trades ever
	// exist for fiat: / crypto: quotes (those pairs are synthesised
	// by the aggregator's triangulation worker from the underlying
	// stablecoin pairs). Skipping LatestTradesForPair here saves a
	// full hypertable chunk-walk against an index condition that's
	// known to return zero rows; the handler's tryRedisVWAPFallback
	// picks up the synthesised value via Redis on the back of
	// ErrPriceNotFound.
	if quote.Type == canonical.AssetFiat || quote.Type == canonical.AssetCrypto {
		return v1.PriceSnapshot{}, nil, false, v1.ErrPriceNotFound
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

// metadataStoreLookup adapts *timescale.Store to
// metadata.AccountObservationLookup. Projects the timescale
// AccountObservation row's *string HomeDomain into the
// (string, bool, error) shape the resolver consumes —
// HomeDomain==nil → ("", false, nil) (no observation), pointer-to-
// empty → ("", false, nil) (observed but operator never set a
// domain), pointer-to-non-empty → (value, true, nil).
type metadataStoreLookup struct{ s *timescale.Store }

func (a metadataStoreLookup) HomeDomainAtOrBefore(ctx context.Context, issuer string, asOfLedger uint32) (string, bool, error) {
	row, err := a.s.LatestAccountObservationAtOrBefore(ctx, issuer, asOfLedger)
	if err != nil {
		// timescale.ErrNotFound → no observation; return ("", false, nil).
		if errors.Is(err, timescale.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if row.HomeDomain == nil || *row.HomeDomain == "" {
		return "", false, nil
	}
	return *row.HomeDomain, true, nil
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

// usdQuoteAsset is the implicit USD quote used to anchor 24h-ago
// price lookups in [storeChange24hReader]. Same string value as
// the v1 handler's defaultPriceQuote — keeping them constructed
// independently here avoids reaching into v1's unexported
// `mustParseAsset`.
var usdQuoteAsset = func() canonical.Asset {
	a, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		panic("ratesengine-api: USD quote asset must parse: " + err.Error())
	}
	return a
}()

// storeChange24hReader adapts *timescale.Store to v1.Change24hReader.
// Looks up the latest closed prices_1m bucket whose end is at-or-
// before now-24h for the asset/USD pair. sql.ErrNoRows (asset
// first traded < 24h ago, or retention pruned the bucket) is
// translated to v1.ErrChange24hUnavailable so the handler treats
// it as "feature unavailable for this asset" rather than a real
// failure. Other errors propagate unchanged.
//
// Pegs is the same operator-declared classic USD-pegged set used
// by the v1 handler's tryStablecoinFiatProxy fallback. When the
// literal asset/fiat:USD lookup misses (the steady-state case on
// Stellar mainnet — nothing on-chain quotes in fiat:USD), this
// adapter walks the pegs and re-runs the at-or-before lookup
// against asset/<peg>. First non-error result wins. Without
// this, /v1/assets/{id}.change_24h_pct silently stays null for
// every on-chain asset (mirrors the same gap fixed in #1217 for
// the /v1/price handler).
type storeChange24hReader struct {
	s    *timescale.Store
	pegs []canonical.Asset
}

func (r storeChange24hReader) USDPrice24hAgo(ctx context.Context, asset canonical.Asset) (string, error) {
	row, err := r.s.ClosedVWAP1mAtOrBefore(ctx,
		canonical.Pair{Base: asset, Quote: usdQuoteAsset},
		time.Now().Add(-24*time.Hour),
	)
	if err == nil {
		return row.VWAP, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// Stablecoin-fiat proxy fallback: walk the operator's USD pegs
	// and try asset/<peg>. First non-error row wins.
	for _, peg := range r.pegs {
		if peg.Equal(asset) {
			continue
		}
		pegRow, pegErr := r.s.ClosedVWAP1mAtOrBefore(ctx,
			canonical.Pair{Base: asset, Quote: peg},
			time.Now().Add(-24*time.Hour),
		)
		if pegErr == nil {
			return pegRow.VWAP, nil
		}
	}
	return "", v1.ErrChange24hUnavailable
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

// parseStreamingPairs converts the operator-declared
// `[api.streaming].pairs` TOML rows (each a [base, quote]
// two-element string array) into canonical Pairs.
//
// Validation is strict: each row must have exactly two non-empty
// strings, and each string must round-trip through
// canonical.ParseAsset. Any error returns immediately so the
// binary fails loud at startup rather than silently dropping
// pairs the operator expected to be streamed.
func parseStreamingPairs(rows [][]string) ([]canonical.Pair, error) {
	out := make([]canonical.Pair, 0, len(rows))
	for i, row := range rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("row %d: expected [base, quote], got %d elements", i, len(row))
		}
		base, err := canonical.ParseAsset(row[0])
		if err != nil {
			return nil, fmt.Errorf("row %d base %q: %w", i, row[0], err)
		}
		quote, err := canonical.ParseAsset(row[1])
		if err != nil {
			return nil, fmt.Errorf("row %d quote %q: %w", i, row[1], err)
		}
		out = append(out, canonical.Pair{Base: base, Quote: quote})
	}
	return out, nil
}

func mkLogger(cfg config.ObsConfig) *slog.Logger {
	return obs.NewLogger(cfg, "ratesengine-api")
}

// warnUnsafeBind logs a security warning at startup when the API
// is configured to listen on a non-loopback address WITHOUT a
// reverse-proxy CIDR allow-list. The combination is the classic
// "raw HTTP exposed to the public internet" footgun:
//
//   - 0.0.0.0:3000 binds to every interface
//   - empty TrustedProxyCIDRs means we trust X-Forwarded-For from
//     the immediate peer, OR (if empty list) we don't honour it at
//     all and everyone looks like the socket peer
//
// Operators running behind Caddy / Cloudflare / similar should
// either (a) bind to 127.0.0.1 + let the proxy share the host or
// (b) populate TrustedProxyCIDRs with the proxy's source range.
//
// Doesn't block startup — the binary still serves — but the
// warning is loud enough to surface in journalctl + log aggregation.
func warnUnsafeBind(logger *slog.Logger, listenAddr string, trustedProxyCIDRs []string) {
	host, _, found := strings.Cut(listenAddr, ":")
	if !found {
		return
	}
	loopback := host == "127.0.0.1" || host == "::1" || host == "localhost"
	if loopback {
		return
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		if len(trustedProxyCIDRs) == 0 {
			logger.Warn("SECURITY: API is listening on a public interface with no trusted proxy CIDRs configured — direct :PORT requests bypass any TLS/WAF you have in front. Bind to 127.0.0.1 OR populate trusted_proxy_cidrs with your reverse proxy's source range.",
				"listen", listenAddr,
				"docs", "https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/pre-launch-hardening.md")
			return
		}
		logger.Warn("API listening on a public interface; trusting forwarded headers from configured proxies. Confirm your reverse proxy strips client-supplied X-Forwarded-* headers before forwarding.",
			"listen", listenAddr,
			"trusted_proxy_cidrs", trustedProxyCIDRs)
	}
}

// warnOpenCORS logs a warning at startup when CORS is set to
// allow every origin AND auth_mode permits authenticated calls.
// The combination lets any third-party site issue authenticated
// requests against the API on behalf of a logged-in browser
// user — a classic CSRF amplifier when paired with cookie-based
// auth (we use bearer tokens, which mitigates the worst of it,
// but the wide-open posture is still a smell).
//
// Default config ships AllowedOrigins=["*"] for the dev path; the
// operator must explicitly narrow before exposing the API.
// parseUSDPeggedClassics resolves the operator's
// trades.usd_pegged_classic_assets strings into canonical Assets so
// /v1/chart can use them as fallback quotes when the literal
// X/fiat:USD pair has zero points. Mirrors the aggregator's
// parseUSDPeggedClassicAssets — soft-fails on malformed entries,
// same rationale (a missing peg is a smaller failure than refusing
// to start).
func parseUSDPeggedClassics(raws []string, logger *slog.Logger) []canonical.Asset {
	if len(raws) == 0 {
		return nil
	}
	out := make([]canonical.Asset, 0, len(raws))
	for _, raw := range raws {
		a, err := canonical.ParseAsset(raw)
		if err != nil {
			logger.Warn("usd_pegged_classic_assets: skipping malformed entry",
				"raw", raw, "err", err)
			continue
		}
		if a.Type != canonical.AssetClassic {
			logger.Warn("usd_pegged_classic_assets: ignoring non-classic asset",
				"raw", raw, "type", a.Type)
			continue
		}
		out = append(out, a)
	}
	return out
}

func warnOpenCORS(logger *slog.Logger, allowedOrigins []string, authMode string) {
	wildcardOnly := len(allowedOrigins) == 1 && allowedOrigins[0] == "*"
	if !wildcardOnly {
		return
	}
	switch authMode {
	case "apikey", "apikey_optional", "sep10":
		logger.Warn("SECURITY: CORS allows every origin (\"*\") and auth_mode permits credentials — narrow [api].allowed_origins to your explorer / explorer hostnames before exposing the API publicly.",
			"auth_mode", authMode,
			"docs", "https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/pre-launch-hardening.md")
	}
}

// signupVerifyEmailerAdapter bridges the v1.SignupVerifyEmailer
// interface to the underlying notify.Sender + an EmailFrom
// address. F-1218 wave 44 (codex audit-2026-05-12).
//
// The plaintext email body (English-only at v1) tells the
// customer the link is single-use and expires in 24h, mirroring
// the dashboard magic-link copy. HTML body included so spam
// filters don't down-rank the message; text body is the
// authoritative content for screen readers and plaintext
// clients.
type signupVerifyEmailerAdapter struct {
	sender notify.Sender
	from   string
}

func (a *signupVerifyEmailerAdapter) SendSignupVerification(ctx context.Context, toEmail, verifyURL string) error {
	if a == nil || a.sender == nil {
		return errors.New("signupVerifyEmailer: not configured")
	}
	subject := "Confirm your Rates Engine signup"
	textBody := "Welcome to the Rates Engine API.\n\n" +
		"Click the link below to confirm your email address. The\n" +
		"link is single-use and expires in 24 hours.\n\n" +
		verifyURL + "\n\n" +
		"You can use the API key returned in the signup response\n" +
		"immediately. Confirmation flips a `email_verified=true`\n" +
		"flag on the key so the dashboard can surface it as a\n" +
		"verified account.\n\n" +
		"If you didn't sign up, you can safely ignore this email.\n"
	htmlBody := "<p>Welcome to the Rates Engine API.</p>" +
		"<p>Click the link below to confirm your email address. " +
		"The link is single-use and expires in 24 hours.</p>" +
		`<p><a href="` + verifyURL + `">` + verifyURL + `</a></p>` +
		"<p>You can use the API key returned in the signup response " +
		"immediately. Confirmation flips an <code>email_verified=true</code> " +
		"flag on the key so the dashboard can surface it as a verified account.</p>" +
		"<p>If you didn't sign up, you can safely ignore this email.</p>"
	msg := notify.Message{
		From:    a.from,
		To:      []string{toEmail},
		Subject: subject,
		HTML:    htmlBody,
		Text:    textBody,
		Tags: map[string]string{
			"flow":   "signup-verify",
			"source": "ratesengine-api",
		},
	}
	return a.sender.Send(ctx, msg)
}

// apiKeyEmailVerifierOrNil returns the v1.APIKeyEmailVerifier
// adapter when Redis is reachable; otherwise nil so the verify
// handler skips the marker step. F-1218 wave 45 (codex audit-
// 2026-05-12).
func apiKeyEmailVerifierOrNil(rdb redis.UniversalClient) v1.APIKeyEmailVerifier {
	if rdb == nil {
		return nil
	}
	return &apiKeyEmailVerifierAdapter{store: auth.NewRedisAPIKeyStore(rdb)}
}

// apiKeyEmailVerifierAdapter bridges the v1.APIKeyEmailVerifier
// interface to auth.RedisAPIKeyStore.MarkEmailVerified.
type apiKeyEmailVerifierAdapter struct {
	store *auth.RedisAPIKeyStore
}

func (a *apiKeyEmailVerifierAdapter) MarkEmailVerified(ctx context.Context, keyID string, at time.Time) error {
	_, err := a.store.MarkEmailVerified(ctx, keyID, at)
	return err
}

// requireEmailVerifiedOrNil returns the F-1218 wave 45 gate
// middleware when the operator has opted in via
// `cfg.API.SignupRequireEmailVerification`; nil keeps the gate
// off so the pre-F-1218 wire contract is preserved.
func requireEmailVerifiedOrNil(enabled bool) middleware.Middleware {
	if !enabled {
		return nil
	}
	return middleware.RequireEmailVerified()
}

// signupVerifyEmailerOrNil returns the v1.SignupVerifyEmailer
// when both a real sender and a non-empty EmailFrom are wired;
// otherwise nil so the signup handler skips the email send and
// reports `email_verification_sent: false` on the wire.
func signupVerifyEmailerOrNil(sender notify.Sender, from string) v1.SignupVerifyEmailer {
	if sender == nil || from == "" {
		return nil
	}
	if _, isNoop := sender.(*notify.NoopSender); isNoop {
		// NoopSender accepts everything but drops the message —
		// surfacing it as "wired" would falsely promise the
		// customer an email. Treat as nil so the wire shape
		// honestly says `email_verification_sent: false`.
		return nil
	}
	return &signupVerifyEmailerAdapter{sender: sender, from: from}
}

// touchUsageMiddlewareOrNil returns the wired TouchUsage
// middleware when BOTH a Postgres-backed keys store AND a Redis
// client are present; otherwise nil so the server's chain
// assembly skips it.
//
// F-1226 (codex audit-2026-05-12) wave 39: Postgres is required
// for the actual UPDATE, Redis for the SETNX debounce. Either
// missing → legacy "no last_used updates" posture, which the
// dashboard renders as "—".
func touchUsageMiddlewareOrNil(keys platform.APIKeyStore, rdb redis.UniversalClient, logger *slog.Logger) middleware.Middleware {
	if keys == nil || rdb == nil {
		return nil
	}
	debouncer := auth.NewRedisTouchDebouncer(rdb, 0)
	return middleware.TouchUsage(keys, debouncer, logger.With("component", "touch-usage"))
}

// usageReaderOrNil returns a v1.UsageReader bound to `c` when
// `c` is non-nil, and a typed-nil v1.UsageReader otherwise. The
// `/v1/account/usage` handler treats `UsageReader == nil` as
// "no usage backend wired" and returns an empty list — the
// correct degradation when Redis is absent. F-1258 (codex
// audit-2026-05-12).
func usageReaderOrNil(c *usage.Counter) v1.UsageReader {
	if c == nil {
		return nil
	}
	return usageReaderAdapter{c: c}
}

// usageReaderAdapter bridges *usage.Counter to v1.UsageReader so
// the v1 package stays free of the internal/usage import.
type usageReaderAdapter struct{ c *usage.Counter }

func (a usageReaderAdapter) Read(ctx context.Context, subject string, days int) ([]v1.UsageDay, error) {
	rows, err := a.c.Read(ctx, subject, days)
	if err != nil {
		return nil, err
	}
	out := make([]v1.UsageDay, len(rows))
	for i, d := range rows {
		out[i] = v1.UsageDay{Date: d.Date, Requests: d.Requests}
	}
	return out, nil
}

// sessionPeekerAdapter bridges dashboardauth.SessionFromContext
// to v1.SessionPeeker so v1's /v1/account/me handler can read
// the magic-link session without importing dashboardauth.
//
// Stateless — the lookup is a context.Value read.
type sessionPeekerAdapter struct{}

func (sessionPeekerAdapter) SessionFromContext(ctx context.Context) (v1.SessionInfo, bool) {
	sc, ok := dashboardauth.SessionFromContext(ctx)
	if !ok {
		return v1.SessionInfo{}, false
	}
	return v1.SessionInfo{
		UserID:          sc.User.ID.String(),
		Email:           sc.User.Email,
		DisplayName:     sc.User.DisplayName,
		Role:            string(sc.User.Role),
		IsStaff:         sc.User.IsStaff,
		EmailVerifiedAt: sc.User.EmailVerifiedAt,
		LastLoginAt:     sc.User.LastLoginAt,
		AccountID:       sc.Account.ID.String(),
		AccountName:     sc.Account.Name,
		AccountSlug:     sc.Account.Slug,
		AccountTier:     string(sc.Account.Tier),
		AccountStatus:   string(sc.Account.Status),
	}, true
}

// forexAdapter bridges the forex.Cache (raw snapshot type) to
// v1.CurrenciesReader (wire-shape projection). The v1 package
// can't import internal/sources/forex without inverting the
// dependency direction; the adapter lives here so main.go owns
// the conversion.
type forexAdapter struct{ cache *forex.Cache }

func newForexAdapter(c *forex.Cache) *forexAdapter { return &forexAdapter{cache: c} }

func (a *forexAdapter) Latest() *v1.CurrenciesSnapshot {
	snap := a.cache.Latest()
	if snap == nil {
		return nil
	}
	rows := make([]v1.CurrencyEntry, len(snap.Currencies))
	for i, c := range snap.Currencies {
		row := v1.CurrencyEntry{
			Ticker:    c.Ticker,
			Name:      c.Name,
			RateUSD:   c.RateUSD,
			UpdatedAt: c.UpdateAt,
		}
		// Join curated monetary-base CSV (lower-case keyed). Market
		// cap is computed in USD-equivalent: the local-units M2
		// divided by "1 USD = N units" rate gives "M2 in USD".
		if entry, ok := snap.Circulation[strings.ToLower(c.Ticker)]; ok && entry.AggregateLocalUnits > 0 {
			supply := entry.AggregateLocalUnits
			row.CirculatingSupply = &supply
			if c.RateUSD > 0 {
				mcap := supply / c.RateUSD
				row.MarketCapUSD = &mcap
			}
			row.CirculationAsOf = entry.AsOf.Format("2006-01-02")
			row.CirculationSource = entry.Source
		}
		rows[i] = row
	}
	history := make(map[string][]v1.CurrencyHistoryRaw, len(snap.History7d))
	for ticker, points := range snap.History7d {
		out := make([]v1.CurrencyHistoryRaw, len(points))
		for i, p := range points {
			out[i] = v1.CurrencyHistoryRaw{Date: p.Date, RateUSD: p.RateUSD}
		}
		history[ticker] = out
	}
	return &v1.CurrenciesSnapshot{
		Currencies:  rows,
		PublishedAt: snap.PublishedAt,
		FetchedAt:   snap.FetchedAt,
		History7d:   history,
	}
}

// prewarmCaches keeps the heaviest read caches hot. The
// /v1/sources?include=stats and /v1/markets / /v1/pools queries
// run aggregations over the trades hypertable that take 5–10s on
// a cold path; cache TTLs of 30–60s mean a single user-pageload
// with no recent neighbours always pays the full cost. Calling
// the cached readers from this goroutine on a 25s cadence keeps
// the entry alive continuously.
//
// Errors get logged at debug level — a transient warmup failure
// is rare and the next cycle retries. Stops on ctx cancel.
func prewarmCaches(
	ctx context.Context,
	logger *slog.Logger,
	stats *v1.CachedSourcesStatsReader,
	markets *v1.CachedMarketsReader,
	coins *v1.CachedCoinsReader,
) {
	const cadence = 25 * time.Second
	prewarmOnce(ctx, logger, stats, markets, coins)
	tick := time.NewTicker(cadence)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			prewarmOnce(ctx, logger, stats, markets, coins)
		}
	}
}

func prewarmOnce(
	ctx context.Context,
	logger *slog.Logger,
	stats *v1.CachedSourcesStatsReader,
	markets *v1.CachedMarketsReader,
	coins *v1.CachedCoinsReader,
) {
	// Per-call deadlines stop a slow query from stalling the whole
	// cycle (a missed cycle is fine — the user's request can still
	// hit the cached entry).
	statsCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := stats.GetSourceStats(statsCtx); err != nil {
		logger.Debug("prewarm sources stats failed", "err", err)
	}
	if _, err := stats.GetSourceVolumeHistory24h(statsCtx); err != nil {
		logger.Debug("prewarm sources volume history failed", "err", err)
	}

	mkCtx, mkCancel := context.WithTimeout(ctx, 60*time.Second)
	defer mkCancel()
	// Mirrors the most-trafficked /v1/markets, /v1/pools requests
	// the explorer fires (default order, no source filter). The
	// limit set covers the four common values we see in practice:
	// 5 (audit script), 25 (Scalar default test), 100 (OpenAPI
	// default), 200 (currencies listing). Each limit is its own
	// cache key under [v1.CachedMarketsReader.AllPools]; without
	// per-limit prewarm, anything off the warmed key 503s under
	// the new pools-server-timeout (#1082).
	//
	// Per-handler order semantics MUST match the cache key the
	// handler will look up:
	// - /v1/markets defaults to MarketsOrderPair (handler accepts
	//   ""|"pair" → 0). Prewarming with 0 hits the right key.
	// - /v1/pools defaults to MarketsOrderVolume24hDesc (handler
	//   accepts ""|"volume_24h_usd_desc" → 1). Pre-2026-05-09 we
	//   prewarmed with 0 which was a phantom slot — every cold-
	//   cache user request still ran a 10-30s SQL scan because the
	//   warmed key never matched. Live measurement that day:
	//   /v1/pools?source=sdex took 27s, soroswap 16s, phoenix 12s,
	//   aquarius 9s, comet 11s. Match the handler's default
	//   explicitly so the warmed key is the one users hit.
	// Important: the unfiltered /v1/pools handler builds
	// `PoolsFilter{Sources: v1.DexSourceNames()}` (the registry's
	// DEX list) — NOT `Sources: nil`. Cache key includes the
	// stringified Sources slice; passing `PoolsFilter{}` here
	// (`Sources: nil` → key fragment `[]`) warms a different key
	// than the user request lands on (`[aquarius comet phoenix
	// sdex soroswap]`). Mirror the handler's behaviour explicitly.
	dexSources := v1.DexSourceNames()
	for _, lim := range []int{5, 25, 100, 200} {
		// /v1/markets default order — alphabetical (MarketsOrderPair).
		if _, _, err := markets.DistinctPairsExt(mkCtx, "", lim, timescale.MarketsOrderPair); err != nil {
			logger.Debug("prewarm markets failed", "limit", lim, "order", "pair", "err", err)
		}
		// /v1/markets explorer-actual order — the home page,
		// HomeTopMarkets, sitemap, and embed/pair routes ALL pass
		// `?order_by=volume_24h_usd_desc`. Pre-fix, the warmer only
		// covered MarketsOrderPair so every explorer pageload hit the
		// 8s timeout (R-001 in docs/review-2026-05-10.md). The cache
		// key includes the order, so the volume-desc variant is a
		// separate slot and needs its own prewarm.
		if _, _, err := markets.DistinctPairsExt(mkCtx, "", lim, timescale.MarketsOrderVolume24hDesc); err != nil {
			logger.Debug("prewarm markets failed", "limit", lim, "order", "volume_24h_usd_desc", "err", err)
		}
		if _, _, err := markets.AllPools(mkCtx, timescale.PoolsFilter{Sources: dexSources}, "", lim, timescale.MarketsOrderVolume24hDesc); err != nil {
			logger.Debug("prewarm pools failed", "limit", lim, "err", err)
		}
	}

	// Per-DEX prewarm — the explorer's /dexes/{source} pages each
	// fire `/v1/pools?source=<dex>&limit=100`, which lands on a
	// distinct cache key per source. Without this, every page click
	// missed cache and ran a 10-30s full-window trades-hypertable
	// scan; sometimes returning a 503 (#1082 path), sometimes
	// overshooting because lib/pq doesn't reliably propagate
	// context cancellation mid-query. Per-DEX prewarm runs the
	// canonical limit=100 + default order the explorer hits;
	// subsequent users land on warm cache (sub-second). Errors are
	// logged at Debug — a missed cycle is fine since the user
	// request still fronts the cache.
	for _, src := range []string{"soroswap", "phoenix", "aquarius", "sdex", "comet"} {
		filter := timescale.PoolsFilter{Sources: []string{src}}
		if _, _, err := markets.AllPools(mkCtx, filter, "", 100, timescale.MarketsOrderVolume24hDesc); err != nil {
			logger.Debug("prewarm per-source pools failed", "source", src, "err", err)
		}
	}

	// Per-CEX/source markets prewarm — the explorer's /exchanges/{name}
	// PairsTable.tsx fires `/v1/markets?source=<src>&limit=200`
	// (volume-desc default). Each maps to a SourceMarkets cache slot
	// distinct from the unfiltered DistinctPairsExt warmed above, so
	// every cold visit to /exchanges/binance, /exchanges/coinbase, etc.
	// previously paid the full 8s ceiling (R-002). One pass per
	// registered source on each cycle keeps the typical pageload at
	// sub-100ms.
	for _, src := range v1.CexSourceNames() {
		if _, _, err := markets.SourceMarkets(mkCtx, src, "", 200, timescale.MarketsOrderVolume24hDesc); err != nil {
			logger.Debug("prewarm per-source markets failed", "source", src, "err", err)
		}
	}

	// /v1/coins?limit=200&include=sparkline backs the unified
	// currencies listing — single most-trafficked coins read.
	//
	// Important: the handler's `prependNative` path subtracts one
	// from `limit` when cursor/issuer/q are all empty (the explorer's
	// no-filter case) so it can splice the synthetic XLM row at the
	// top without overshooting the user's requested page size. So a
	// /v1/coins?limit=200 user request actually calls
	// `ListCoinsExt(ctx, ListCoinsOptions{Limit: 199, …})` under the
	// hood — passing Limit=200 here warms a different cache key than
	// the one the user request looks up. Mirror the listingLimit the
	// handler actually uses.
	coinsCtx, coinsCancel := context.WithTimeout(ctx, 20*time.Second)
	defer coinsCancel()
	if _, err := coins.ListCoinsExt(coinsCtx, timescale.ListCoinsOptions{Limit: 199}); err != nil {
		logger.Debug("prewarm coins listing failed", "err", err)
	}
}

// forexQuoteWriter adapts (*timescale.Store) to forex.FXQuoteWriter
// (the worker can't import timescale without inverting the
// dependency direction). Translates the per-package FXQuote shape.
type forexQuoteWriter struct{ store *timescale.Store }

func (w *forexQuoteWriter) InsertFXQuoteBatch(ctx context.Context, quotes []forex.FXQuote) error {
	if len(quotes) == 0 {
		return nil
	}
	out := make([]timescale.FXQuote, len(quotes))
	for i, q := range quotes {
		out[i] = timescale.FXQuote{
			Bucket:     q.Bucket,
			Ticker:     q.Ticker,
			RateUSD:    q.RateUSD,
			InverseUSD: q.InverseUSD,
			Source:     q.Source,
		}
	}
	return w.store.InsertFXQuoteBatch(ctx, out)
}

// fxHistoryReader adapts (*timescale.Store) to v1.FXHistoryReader.
// Mirrors the writer adapter but on the read path; the v1 package's
// FXQuotePoint deliberately omits Ticker + Source (the handler
// already knows ticker, source is provenance not display data).
type fxHistoryReader struct{ store *timescale.Store }

func (r *fxHistoryReader) ListFXHistory(ctx context.Context, ticker string, from, to time.Time) ([]v1.FXQuotePoint, error) {
	rows, err := r.store.ListFXHistory(ctx, ticker, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]v1.FXQuotePoint, len(rows))
	for i, q := range rows {
		out[i] = v1.FXQuotePoint{
			Bucket:     q.Bucket,
			RateUSD:    q.RateUSD,
			InverseUSD: q.InverseUSD,
		}
	}
	return out, nil
}
