package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate"
	"github.com/RatesEngine/rates-engine/internal/api/streaming"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/currency"
	"github.com/RatesEngine/rates-engine/internal/currency/marketcap"
	"github.com/RatesEngine/rates-engine/internal/incidents"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// ReadyChecker is the interface /readyz polls to decide whether
// the serving-plane dependencies are responsive. Implementations
// in cmd/ratesengine-api/main.go:
//
//   - storeChecker (wraps *timescale.Store.DB().PingContext) — critical
//   - redisChecker (wraps *redis.Client.Ping) — non-critical
//
// Ping MUST respect ctx and return promptly on cancellation — the
// handler runs every checker in parallel under a shared 2 s
// deadline; a misbehaving checker that ignores ctx can turn readyz
// into a cascade-failure vector for the liveness probe.
//
// Critical() distinguishes "API can't serve requests without
// this" (Postgres — no fallback for trade/aggregate reads) from
// "API can degrade-but-serve without this" (Redis — cache miss
// falls back to Timescale per ADR-0007). The /readyz handler
// uses this to return 503 ONLY when a critical check fails;
// a failing non-critical check produces a 200 response with
// `status="degraded"` so edge load balancers (HAProxy, k8s
// readiness probes) keep the backend in service while operators
// see the per-check breakdown in the response body.
//
// F-1275 (codex audit-2026-05-13): pre-wave-110 every check was
// effectively critical — a Redis outage would 503 readyz and
// HAProxy would drain every healthy API backend even though
// Timescale fallback kept the actual customer-facing surface
// serving correctly.
type ReadyChecker interface {
	Ping(ctx context.Context) error
	Name() string
	Critical() bool
}

// Server is the HTTP handler for the Rates Engine v1 API.
//
// Construction: [New] returns a Server with routes mounted.
// Call [Server.Handler] to get an http.Handler for an
// [http.Server].
//
// Thread-safe.
type Server struct {
	logger               *slog.Logger
	checks               []ReadyChecker
	assets               AssetReader
	prices               PriceReader
	history              HistoryReader
	markets              MarketsReader
	oracle               OracleReader
	meta                 MetadataResolver
	accounts             AccountStore
	signups              SignupTracker
	signupIPThrottle     SignupIPThrottle
	signupVerifier       SignupVerifier
	signupVerifyEmailer  SignupVerifyEmailer
	apiKeyEmailVerifier  APIKeyEmailVerifier
	stripe               *StripeWebhookConfig
	divergence           DivergenceLooker
	freeze               FrozenLooker
	supply               SupplyLooker
	volume               VolumeReader
	change24h            Change24hReader
	changesum            ChangeSummaryReader
	coins                CoinsReader
	issuers              IssuersReader
	sep41Transfers       SEP41TransfersReader
	cursors              CursorsReader
	coverageReader       SourceCoverageReader
	networkStats         NetworkStatsReader
	sourcesStats         SourcesStatsReader
	lending              LendingReader
	currencies           CurrenciesReader
	fxHistory            FXHistoryReader
	sessionPeeker        SessionPeeker
	incidents            []incidents.Incident
	sep10                auth.SEP10Validator
	cors                 middleware.Middleware
	auth                 middleware.Middleware
	keyPolicy            middleware.Middleware
	rateLimit            middleware.Middleware
	monthlyQuota         middleware.Middleware
	touchUsage           middleware.Middleware
	requireEmailVerified middleware.Middleware
	usageTracker         middleware.Middleware
	usageReader          UsageReader
	hub                  *streaming.Hub
	confidence           ConfidenceLooker
	triangulated         TriangulatedPriceLooker
	cdnEnabled           bool
	statusBackend        StatusBackend
	regionName           string
	regionDeployment     string
	dashboardAuth        DashboardAuthMounter
	dashboardKeys        DashboardAuthMounter
	dashboardWebhooks    DashboardAuthMounter
	sessionAuth          middleware.Middleware
	// verifiedCurrencies is the loaded *currency.Catalogue — the
	// cross-chain currency seed (USDC, USDT, BTC, ETH, …) plus per-
	// network identities. Powers the `unverified_warning` body +
	// flags.unverified_ticker_collision attachment on /v1/assets/{id}
	// (R-018 Phase 1.1). Nil-safe: applyUnverifiedWarning returns
	// false when the catalogue isn't wired, leaving every response
	// without the warning surface — that's the same behaviour as
	// pre-1.1.
	verifiedCurrencies *currency.Catalogue
	// marketCaps is the process-local cache of CoinGecko-sourced
	// market_cap_usd values for catalogue crypto + stablecoin
	// entries. Populated by a background refresher
	// (internal/currency/marketcap.Refresher) on a 5-min cadence;
	// handlers read on demand and gracefully degrade to "—" when
	// the slug isn't cached yet. Nil-safe.
	marketCaps *marketcap.Cache
	// backfillCoverage is the per-source min/max-ledger snapshot
	// powering /v1/diagnostics/ingestion's coverage section. Nil
	// leaves that section absent. See [CoverageCache].
	backfillCoverage *CoverageCache
	// globalPrice + globalPriceOpts power the /v1/assets/{slug}
	// global view's three-tier fallback chain (R-018 Phase 1.3a/1.4a).
	// Nil-safe: handleGlobalAsset returns a view without the price
	// block when not wired — the slug still resolves to a catalogue
	// entry, networks[] still populates, and consumers can drill
	// into the Stellar network's deep_link for per-asset pricing.
	globalPrice     aggregate.GlobalPriceReader
	globalPriceOpts aggregate.GlobalPriceOptions
	// sacWrappers is the operator-config map of Stellar-Asset-Contract
	// C-strkey → "CODE-ISSUER" canonical asset key. Surfaced on
	// /v1/sac-wrappers so the explorer can resolve raw Soroban
	// contract addresses (which Soroswap/Phoenix/Aquarius/Comet
	// emit as base/quote in their swap events) back to readable
	// asset symbols. Nil means "operator hasn't configured the map"
	// — the endpoint serves an empty object.
	sacWrappers map[string]string
	// assetDetailCache is the response-level cache for /v1/assets/{id}.
	// Stores the pre-rendered JSON bytes + Flags per asset_id with a
	// short TTL (30s by default). Cache hits skip the entire handler
	// chain — resolveAssetDetail, applySep1Overlay (even on Redis
	// hit), applyF2Fields (4 uncached DB calls: volume / 2× price /
	// supply), applyCoinExtensionFields. Drift-safe by construction:
	// the cached entry IS what the handler produces.
	//
	// Pre-cache benchmark (rc.63 internal localhost on r1): ~700-900ms
	// warm. The 7-reader fan-out caches (CachedCoinsReader SWR) are
	// hot from prewarmCaches + selfPrewarmAssetEndpoints, so the
	// remaining cost is in the F2 chain. Wrapping each F2 reader is
	// 4 new wrapper types; the response-level cache is one type.
	//
	// Nil-safe: a nil cache short-circuits every method to no-op +
	// miss. ttl=0 has the same effect at config layer.
	assetDetailCache *assetDetailResponseCache
	// usdPeggedClassics is the operator's allow-list of classic
	// credit assets they declare as USD-pegged stablecoins.
	// Mirrors trades.usd_pegged_classic_assets from config. Used
	// at chart-fallback time: when /v1/chart is asked for X/fiat:USD
	// and the literal pair has zero points (because we don't store
	// synthetic XLM/USD in prices_1m — the proxy is applied at
	// query time), the chart handler retries against X/<peg> for
	// each entry until one returns data, marking the response
	// `triangulated: true` for transparency.
	usdPeggedClassics []canonical.Asset
	// ingestionSnapshot caches a fully-built IngestionDiagnostics
	// computed every ~15s by a background goroutine launched via
	// [Server.StartIngestionSnapshotRefresh]. Powers
	// /v1/diagnostics/ingestion sub-millisecond when populated
	// (#16). Nil before the first refresh fires; handler falls back
	// to inline-build (the legacy 200-500ms path) in that case.
	ingestionSnapshot atomic.Pointer[ingestionSnapshotEntry]
	mux               *http.ServeMux
	started           time.Time
}

// DashboardAuthMounter is the interface main.go's
// dashboardauth.Handlers satisfies — defined here so this package
// doesn't import dashboardauth (the dependency goes the other
// way: dashboardauth uses internal/notify + internal/platform,
// both of which are leaf packages, and main.go wires the result
// into v1.Options).
type DashboardAuthMounter interface {
	Mount(mux *http.ServeMux)
}

// Options configures a [Server] at construction.
type Options struct {
	Logger *slog.Logger
	// ReadyChecks are polled by /readyz. Order matters only for
	// log output (first-failed wins).
	ReadyChecks []ReadyChecker
	// Assets, when non-nil, backs /v1/assets and /v1/assets/{id}.
	// Leave nil during early bring-up; handlers return an empty
	// list + degrade single-asset lookups to pure canonical echo.
	Assets AssetReader
	// Prices, when non-nil, backs /v1/price. Leave nil to return
	// 503 — the handler is mounted either way so clients can
	// integrate against the wire contract before we have a
	// reader wired.
	Prices PriceReader

	// History, when non-nil, backs /v1/history. Leave nil to return
	// 503 on that path.
	History HistoryReader

	// Markets, when non-nil, backs /v1/markets. Leave nil and the
	// handler serves an empty list (mirrors /v1/assets' pattern so
	// clients can integrate before the data is available).
	Markets MarketsReader

	// Oracle, when non-nil, backs /v1/oracle/latest. Leave nil to
	// return 503 on that path.
	Oracle OracleReader
	// Meta, when non-nil, enables the SEP-1 overlay on
	// /v1/assets/{id}. Typically a *metadata.Cache wrapping a
	// *metadata.Resolver backed by Redis.
	Meta MetadataResolver

	// CORS, when non-nil, is inserted above RateLimit in the
	// middleware stack. Preflight OPTIONS requests short-circuit
	// before the rate-limit counter increments. Typically
	// constructed via middleware.CORS(...) with AllowedOrigins
	// drawn from cfg.API.AllowedOrigins.
	CORS middleware.Middleware

	// Accounts, when non-nil, backs POST /v1/account/keys (key
	// issuance). Leave nil to make that endpoint return 503 — the
	// GET endpoints (/me, /usage) only consult the request-context
	// Subject and don't need the store. Wire only when Redis is
	// reachable; the binary's auth.NewRedisAPIKeyStore enforces that.
	Accounts AccountStore

	// Signups, when non-nil, backs POST /v1/signup's per-email
	// duplicate check. Without it, signup still works but isn't
	// idempotent on the email — a second signup for the same address
	// just mints another key. Production wires a Redis-backed
	// implementation that persists email-hash → key-id; nil makes
	// the duplicate check a no-op (key always mints).
	Signups SignupTracker

	// SignupIPThrottle, when non-nil, applies a per-IP cap to
	// /v1/signup separate from the global-rate-limit middleware.
	// The global IP bucket allows 60/min anonymous; that's plenty
	// for browsing the public surfaces but lets an attacker
	// bulk-mint signup→key_id pairs (one signup per request, so
	// 60 keys per minute per IP, ~3,600/hour). This throttle
	// hardens specifically against the signup-bulk-mint abuse
	// vector — typical wiring is a 5/hour-per-IP Redis bucket.
	// Nil keeps the legacy "trust the global rate limit alone"
	// behaviour. F-1232 (audit-2026-05-12).
	SignupIPThrottle SignupIPThrottle

	// SignupVerifier, when non-nil, backs the email-ownership-
	// proof flow added in F-1218 (codex audit-2026-05-12). The
	// signup handler issues a single-use token via
	// `Reserve(token, keyID, ttl)`; the
	// `GET /v1/signup/verify?token=…` handler consumes it via
	// `Consume(token)` and (in subsequent waves) flips the key
	// to a verified state. Nil disables the verify endpoint —
	// it returns 503 with a clear "verification not configured"
	// message so customers don't get the silent-no-op surprise.
	SignupVerifier SignupVerifier

	// SignupVerifyEmailer, when non-nil + paired with a non-nil
	// `SignupVerifier`, makes the signup handler issue a token,
	// Reserve it, and email the click-through verify URL.
	// F-1218 wave 44 (codex audit-2026-05-12). Nil keeps the
	// signup-handler response shape unchanged (no email sent,
	// `email_verification_sent: false` on the wire); the
	// verifier endpoint stays a no-op until wave 44 is wired
	// end-to-end.
	SignupVerifyEmailer SignupVerifyEmailer

	// APIKeyEmailVerifier, when non-nil, lets the
	// `/v1/signup/verify` handler flip the `EmailVerifiedAt`
	// timestamp on the underlying API key record after Consume.
	// F-1218 wave 45 (codex audit-2026-05-12). Production wiring
	// is `auth.RedisAPIKeyStore.MarkEmailVerified`. Nil disables
	// the marker write — the verify endpoint still returns 200
	// (the customer's click is acknowledged), but the optional
	// `RequireEmailVerified` gate can't reflect it back into
	// subsequent requests.
	APIKeyEmailVerifier APIKeyEmailVerifier

	// Stripe, when non-nil, backs POST /v1/webhooks/stripe (paid-
	// tier upgrade webhook). Nil makes the endpoint return 503 so
	// deployments without Stripe don't accept arbitrary upgrade
	// requests. The signing secret inside is the `whsec_…` value
	// from the Stripe dashboard.
	Stripe *StripeWebhookConfig

	// Divergence, when non-nil, is consulted by /v1/price after a
	// successful LatestPrice lookup. When the lookup says
	// "warning fired" for the asset, the response carries
	// flags.divergence_warning=true. Nil means "no divergence
	// signal available" — the flag stays at its default false.
	// Wire when both the divergence worker and Redis are running.
	Divergence DivergenceLooker

	// Freeze, when non-nil, is consulted by /v1/price (and
	// /v1/price/batch) after a successful LatestPrice lookup. When
	// it reports "frozen" for the pair, the response carries
	// flags.frozen=true and flags.single_source=true (per
	// anomaly.ActionFreeze, ADR-0019). Nil means "no freeze signal
	// available" — flags.frozen stays false and flags.single_source
	// is derived from the observation count instead. Wire when the
	// aggregator's freeze-marker writer + Redis are both running.
	Freeze FrozenLooker

	// Supply, when non-nil, populates the F2 fields
	// (total_supply, circulating_supply, max_supply, market_cap_usd,
	// fdv_usd, supply_basis) on /v1/assets/{id} per ADR-0011.
	// Production wiring: a thin adapter around timescale.Store.LatestSupply.
	// Nil means "F2 fields unavailable" — the asset-detail body still
	// serves; F2 fields stay null. A non-nil reader still depends on
	// some other process populating asset_supply_history; this repo
	// snapshot only wires the read path.
	Supply SupplyLooker

	// Volume, when non-nil, populates the `volume_24h_usd` field on
	// /v1/assets/{id} (trailing-24h USD-denominated trade volume
	// across every pair the asset participates in). Per Freighter V2
	// scope. Production wiring: a thin adapter around
	// timescale.Store.Volume24hUSDForAsset. Nil leaves the field
	// null — independent of Supply, so the volume can serve even
	// when supply isn't yet wired (and vice versa).
	Volume VolumeReader

	// Change24h, when non-nil, populates the `change_24h_pct` field
	// on /v1/assets/{id} (signed percentage change vs the asset's
	// USD price ~24h ago). Production wiring: a thin adapter around
	// timescale.Store.ClosedVWAP1mAtOrBefore at t=now-24h. Nil
	// leaves the field null. Independent of Supply / Volume — any
	// combination of (Supply, Volume, Change24h) is legal.
	Change24h Change24hReader

	// ChangeSummary, when non-nil, backs GET /v1/changes/{entity_type}/{id}.
	// Production wiring: a thin adapter around
	// timescale.Store.GetChangeSummary, which reads the
	// change_summary_5m hypertable populated by the changesummary
	// worker (Phase 3). Powers every multi-window delta strip on
	// the explorer. Nil makes the endpoint return 503.
	ChangeSummary ChangeSummaryReader

	// Coins, when non-nil, supplies the coin-equivalence overlay
	// the /v1/assets handlers fan out across (price / volume /
	// market_cap / sparkline / ATH / top_markets). The standalone
	// /v1/coins HTTP route was removed in rc.48; this seam stays
	// because every /v1/assets row sources the same data through
	// it. Production wiring is timescale.Store directly (implements
	// ListCoinsExt). Nil makes the affected /v1/assets fields 503.
	Coins CoinsReader

	// Issuers, when non-nil, backs GET /v1/issuers/{g_strkey}.
	// Production wiring is timescale.Store directly. Nil makes
	// the endpoint return 503.
	Issuers IssuersReader

	// SEP41Transfers, when non-nil, backs GET
	// /v1/contracts/{contract_id}/transfers. Production wiring is
	// timescale.Store directly (it implements ListSEP41Transfers).
	// Nil makes the endpoint return 503. F-0021 closure
	// (audit-2026-05-26): per-account net-position queries — the
	// Stellar moat feature CG/CMC structurally cannot offer.
	SEP41Transfers SEP41TransfersReader

	// Cursors, when non-nil, backs GET /v1/diagnostics/cursors.
	// Production wiring is timescale.Store directly (it implements
	// ListCursors). Nil makes the endpoint return 503. Operator-
	// facing diagnostic; powers the explorer /diagnostics page.
	Cursors CursorsReader

	// CoverageReader, when non-nil, backs the ADR-0031 shadow
	// data-derived density on /v1/diagnostics/ingestion. Reads
	// source_coverage_snapshots rows that the gap detector
	// (in the aggregator binary) upserts every cycle. Production
	// wiring is timescale.Store directly (ListSourceCoverage). Nil
	// leaves DensityPctV2 / GapFreePct as zero in every response
	// row; v1 cursor-derived density remains the authoritative
	// signal during the Phase 1 shadow window.
	CoverageReader SourceCoverageReader

	// NetworkStats, when non-nil, backs GET /v1/network/stats —
	// the consolidated home-page aggregate (24h volume, markets,
	// assets indexed, latest ledger). Production wiring is
	// timescale.Store directly. Nil makes the endpoint 503.
	NetworkStats NetworkStatsReader

	// SourcesStats, when non-nil, populates the per-source
	// trade_count_24h field on /v1/sources?include=stats. Without
	// it, the include flag is silently ignored and the response
	// stays the all-static-registry projection.
	SourcesStats SourcesStatsReader

	// Lending, when non-nil, backs /v1/lending/pools (the per-Blend-
	// pool summary listing). Leave nil and the handler serves an
	// empty array — same degradation pattern as Markets.
	Lending LendingReader

	// Currencies, when non-nil, supplies the world fiat-currency
	// rates snapshot used by /v1/assets fiat rows + chart fiat:fiat
	// fallback. The standalone /v1/currencies route was removed in
	// rc.48; this seam stays because /v1/assets and /v1/chart both
	// consume the same snapshot. Leave nil to fall back to empty
	// currencies state.
	Currencies CurrenciesReader

	// FXHistory, when non-nil, lets /v1/chart serve fiat:fiat pairs
	// from the fx_quotes hypertable for ranges beyond 7d. Leave nil
	// to keep /v1/chart fiat:fiat in 7d-only mode.
	FXHistory FXHistoryReader

	// SessionPeeker, when non-nil, lets handlers read the
	// magic-link session bound to the request context. Used by
	// /v1/account/me to surface user/account info for cookie-auth
	// callers (the API-key path uses Subject; both can coexist on a
	// request, in which case session takes precedence).
	SessionPeeker SessionPeeker

	// SEP10, when non-nil, backs GET /v1/auth/sep10/challenge and
	// POST /v1/auth/sep10/token. Production wiring: an
	// auth/sep10.Validator constructed from the binary's signing
	// seed + JWT secret config. Nil makes both endpoints return 503
	// (the binary didn't wire one — typically because the seed/
	// secret config is absent in this deployment).
	SEP10 auth.SEP10Validator

	// Auth, when non-nil, is inserted between CORS and RateLimit.
	// Sets a Subject in the request context that downstream
	// middleware (rate-limit, request logger) and handlers can
	// read via [auth.SubjectFrom]. Typically constructed via
	// middleware.Auth(middleware.AuthOptions{Mode: cfg.API.AuthMode, …}).
	// Leave nil for legacy "no auth, anonymous-only" behaviour;
	// the rate-limit middleware then keys on RemoteIP only.
	Auth middleware.Middleware

	// KeyPolicy, when non-nil, runs AFTER Auth and BEFORE RateLimit.
	// Enforces the per-key policy fields the dashboard surfaces
	// (IP allowlist, Referer allowlist, per-endpoint permissions)
	// against the authenticated Subject. F-1226 (codex
	// audit-2026-05-12): pre-fix these were accepted at key
	// creation but no middleware enforced them at request time.
	// Anonymous subjects pass through unchanged; the policy data
	// only ships on Subjects produced by the Postgres validator.
	// Typically constructed via middleware.KeyPolicy().
	KeyPolicy middleware.Middleware

	// RateLimit, when non-nil, is appended to the middleware stack
	// as the innermost wrapper — so the Logger + Auth middlewares
	// have already populated remote_ip + Subject into the request
	// context. Typically constructed via
	// middleware.RateLimitBySubject(anonBucket, authBucket, ...)
	// so the per-tier limits (api.anon_rate_limit_per_min vs
	// api.key_rate_limit_per_min) actually take effect; the older
	// single-bucket middleware.RateLimit shape is kept for tests
	// but production wiring uses the by-subject form. See
	// cmd/ratesengine-api/main.go for the canonical wire-up.
	RateLimit middleware.Middleware

	// UsageTracker, when non-nil, is inserted at the end of the
	// middleware chain; fires per-request to record per-day
	// counters that feed /v1/account/usage. Best-effort — never
	// blocks a request. Pair with UsageReader to expose the data.
	UsageTracker middleware.Middleware

	// MonthlyQuota, when non-nil, is inserted BEFORE rate-limit so
	// a request that exceeds the per-key monthly cap returns 429
	// without spending a rate-limit token. F-1226 (codex audit-
	// 2026-05-12). Wire-up: middleware.MonthlyQuota(usageCounter,
	// …). Skipped when nil — the cap is opt-in per validator (only
	// Postgres-backed keys carry `Subject.MonthlyQuota`).
	MonthlyQuota middleware.Middleware

	// TouchUsage, when non-nil, is inserted INSIDE rate-limit so
	// a denied (429) request doesn't update the dashboard's "last
	// seen" column for the rejected attempt. The middleware
	// itself fires post-handler with a Redis-SETNX debounce, so
	// per-request cost is one Redis SETNX even on cache hit. F-1226
	// (codex audit-2026-05-12) wave 39. Skipped when nil — opt-in
	// per deployment (requires both Postgres keys store + Redis).
	TouchUsage middleware.Middleware

	// RequireEmailVerified, when non-nil, is inserted AFTER auth
	// and BEFORE rate-limit. It rejects API-key callers whose
	// `EmailVerifiedAt` is zero AND whose identifier indicates a
	// `/v1/signup` origin. F-1218 wave 45 (codex audit-2026-05-12).
	// Opt-in per deployment — production wiring gates this on
	// `cfg.API.SignupRequireEmailVerification` so existing keys
	// keep working through the rollout window.
	RequireEmailVerified middleware.Middleware

	// UsageReader, when non-nil, backs /v1/account/usage with
	// real per-day counts. Without it the endpoint stays on its
	// "empty list with locked wire shape" default.
	UsageReader UsageReader

	// Hub, when non-nil, backs the closed-bucket SSE endpoint
	// (`/v1/price/stream`). Producers (typically the aggregator's
	// per-window-close pass) call Hub.Publish(); subscribers attach
	// via [streaming.Stream] inside the handler.
	//
	// Leave nil to make `/v1/price/stream` return 503 — the rest
	// of the v1 API serves cleanly. The tip + observations stream
	// endpoints do NOT use this Hub; they are per-connection-tick.
	Hub *streaming.Hub

	// Confidence, when non-nil, populates the confidence + factors
	// fields on `/v1/price` responses (ADR-0019 §"Multi-factor
	// confidence score"). Production wiring: a Redis adapter that
	// reads `confidence:<base>:<quote>:<window>` from the cache
	// the aggregator's confidence-compute path writes.
	//
	// Leave nil to keep the score off the wire — the rest of the
	// `/v1/price` envelope serves cleanly without it. Cache misses
	// at lookup time also leave the field unset.
	Confidence ConfidenceLooker

	// Triangulated, when non-nil, is the fallback /v1/price
	// consults after a Timescale miss. Returns triangulated
	// implied VWAPs (per the aggregator's triangulation worker)
	// + the provenance marker that gates `flags.triangulated`.
	// Production wiring: a Redis adapter reading
	// `vwap:<base>:<quote>:<window>` + the `:provenance` sibling.
	// Nil leaves /v1/price 404'ing for triangulated-only pairs
	// (the historical behaviour).
	Triangulated TriangulatedPriceLooker

	// CDNEnabled controls whether cacheable routes emit `s-maxage`
	// (CDN-tier) Cache-Control directives in addition to `max-age`
	// (client tier). Default: true — operators with a CDN in front
	// of the API leave it on. Set false (via cfg.API.CDNEnabled) for
	// deployments without a CDN, so a CDN they don't run can't cache
	// anything that downstream changes might have made auth-tied.
	// See [middleware.CacheControlWithCDN] for the policy detail.
	CDNEnabled bool

	// StatusBackend, when non-nil, backs /v1/status with
	// Prometheus-derived service heartbeats, latency percentiles,
	// freshness signals, and Alertmanager incident counts. Nil
	// keeps /v1/status serving an in-process surface (uptime +
	// region label only) — useful for deployments without a local
	// Prometheus.
	StatusBackend StatusBackend

	// RegionName + RegionDeployment label /v1/status responses.
	// Default to "unknown" / "production" when unset.
	RegionName       string
	RegionDeployment string

	// DashboardAuth, when non-nil, mounts the customer-dashboard
	// magic-link auth flow (POST /v1/auth/login + GET /v1/auth/callback
	// + POST /v1/auth/logout). Production wiring is a
	// dashboardauth.Handlers built from the Postgres platform stores
	// + a Resend (or Noop) sender; main.go gates construction on
	// cfg.API.Dashboard.BaseURL being non-empty.
	DashboardAuth DashboardAuthMounter

	// DashboardKeys, when non-nil, mounts the dashboard's
	// key-management surface (GET / POST / DELETE /v1/dashboard/keys
	// — the dashboard SPA's source of truth for listing + minting
	// + revoking customer keys, gated on the session cookie that
	// DashboardAuth sets). Same DashboardAuthMounter shape; main.go
	// gates construction on the Postgres platform stores being
	// reachable.
	DashboardKeys DashboardAuthMounter

	// DashboardWebhooks, when non-nil, mounts the dashboard's
	// customer-webhook CRUD surface (GET / POST / PATCH / DELETE
	// /v1/dashboard/webhooks + GET /v1/dashboard/webhooks/{id}/deliveries).
	// Backed by `internal/platform/postgresstore.WebhookStore`; the
	// delivery worker that drains the queue runs in
	// `internal/customerwebhook` and is orthogonal to these
	// handlers. F-1270 (audit-2026-05-12).
	DashboardWebhooks DashboardAuthMounter

	// SACWrappers is the operator-config map of SAC C-strkey →
	// "CODE-ISSUER" classic asset key. Backs /v1/sac-wrappers,
	// the read-only resolution endpoint the explorer's AssetLabel
	// joins client-side to render readable symbols for Soroban DEX
	// pools (which use SAC contracts as base/quote at the wire). Nil
	// or empty makes the endpoint return an empty map — the explorer
	// degrades to showing the raw C-strkey.
	SACWrappers map[string]string

	// USDPeggedClassics is the operator's allow-list of classic
	// credit assets they trust as 1:1 USD stablecoins. Same list
	// fed to trades.usd_pegged_classic_assets — wire it through
	// from the same TradesConfig field. Used by /v1/chart to
	// fall back from a literal X/fiat:USD lookup (which has no
	// rows in prices_1m — the proxy is computed at query time)
	// to X/<peg> when the literal pair returns 0 points. Empty
	// disables the fallback; the chart endpoint still serves the
	// literal pair when one exists.
	USDPeggedClassics []canonical.Asset

	// SessionAuth, when non-nil, wraps every handler so a present
	// dashboard session cookie populates a SessionContext on the
	// request context. Anonymous + bearer-token requests pass
	// through untouched. Required for the /v1/dashboard/* routes
	// to read the session — DashboardKeys handlers 401 on missing
	// session context.
	SessionAuth middleware.Middleware

	// VerifiedCurrencies, when non-nil, enables the verified-
	// currency overlay on /v1/assets/{id}: an `unverified_warning`
	// body + flags.unverified_ticker_collision when the requested
	// asset's code matches a verified currency's Stellar ticker
	// but the issuer doesn't. Production wiring loads
	// currency.LoadEmbedded() in cmd/ratesengine-api/main.go. Nil
	// keeps the warning surface off — every response serves
	// unchanged.
	//
	// When set, also enables the slug dispatch on
	// `/v1/assets/{slug}`: a path that matches a verified-currency
	// slug routes to the global view (Phase 1.4a) instead of the
	// per-Stellar-asset surface.
	VerifiedCurrencies *currency.Catalogue

	// MarketCaps, when non-nil, is the process-local CoinGecko-
	// sourced market_cap_usd cache for catalogue crypto +
	// stablecoin entries. The cmd/ratesengine-api binary wires it
	// with a background refresher; tests can pass a populated
	// stub or leave nil (handlers degrade gracefully).
	MarketCaps *marketcap.Cache

	// BackfillCoverage, when non-nil, is the process-local cache of
	// per-source min/max ledger + trade count, refreshed on a 5-min
	// background goroutine. Powers the per-source coverage section
	// on `/v1/diagnostics/ingestion`. The underlying SQL is 2–3s on
	// a populated trades hypertable so we never run it synchronously
	// from a request. Nil leaves that section absent from the wire.
	BackfillCoverage *CoverageCache

	// GlobalPrice, when non-nil, powers the price block on
	// `/v1/assets/{slug}` global views via the three-tier fallback
	// chain (vwap_native → aggregator_avg → triangulated). Nil
	// leaves the price block empty — the slug still resolves, the
	// catalogue identity + networks list still surface, but
	// consumers fall back to the Stellar-network deep_link for a
	// headline price.
	GlobalPrice aggregate.GlobalPriceReader

	// GlobalPriceOpts tunes the three-tier policy. Leave zero-value
	// to use [aggregate.DefaultGlobalPriceOptions] except for the
	// aggregator source list, which is wired explicitly (the
	// defaults can't safely guess which sources are aggregator
	// class without importing the registry).
	GlobalPriceOpts aggregate.GlobalPriceOptions
}

// New constructs a Server and mounts all v1 routes.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:               logger,
		checks:               opts.ReadyChecks,
		assets:               opts.Assets,
		prices:               opts.Prices,
		history:              opts.History,
		markets:              opts.Markets,
		oracle:               opts.Oracle,
		meta:                 opts.Meta,
		accounts:             opts.Accounts,
		signups:              opts.Signups,
		signupIPThrottle:     opts.SignupIPThrottle,
		signupVerifier:       opts.SignupVerifier,
		signupVerifyEmailer:  opts.SignupVerifyEmailer,
		apiKeyEmailVerifier:  opts.APIKeyEmailVerifier,
		stripe:               opts.Stripe,
		divergence:           opts.Divergence,
		freeze:               opts.Freeze,
		supply:               opts.Supply,
		volume:               opts.Volume,
		change24h:            opts.Change24h,
		changesum:            opts.ChangeSummary,
		coins:                opts.Coins,
		issuers:              opts.Issuers,
		sep41Transfers:       opts.SEP41Transfers,
		cursors:              opts.Cursors,
		coverageReader:       opts.CoverageReader,
		networkStats:         opts.NetworkStats,
		sourcesStats:         opts.SourcesStats,
		lending:              opts.Lending,
		currencies:           opts.Currencies,
		fxHistory:            opts.FXHistory,
		sessionPeeker:        opts.SessionPeeker,
		sep10:                opts.SEP10,
		cors:                 opts.CORS,
		auth:                 opts.Auth,
		keyPolicy:            opts.KeyPolicy,
		rateLimit:            opts.RateLimit,
		monthlyQuota:         opts.MonthlyQuota,
		touchUsage:           opts.TouchUsage,
		requireEmailVerified: opts.RequireEmailVerified,
		usageTracker:         opts.UsageTracker,
		usageReader:          opts.UsageReader,
		hub:                  opts.Hub,
		confidence:           opts.Confidence,
		triangulated:         opts.Triangulated,
		cdnEnabled:           opts.CDNEnabled,
		statusBackend:        opts.StatusBackend,
		regionName:           valueOr(opts.RegionName, "unknown"),
		regionDeployment:     valueOr(opts.RegionDeployment, "production"),
		dashboardAuth:        opts.DashboardAuth,
		dashboardKeys:        opts.DashboardKeys,
		dashboardWebhooks:    opts.DashboardWebhooks,
		sessionAuth:          opts.SessionAuth,
		verifiedCurrencies:   opts.VerifiedCurrencies,
		marketCaps:           opts.MarketCaps,
		backfillCoverage:     opts.BackfillCoverage,
		globalPrice:          opts.GlobalPrice,
		globalPriceOpts:      globalPriceOptsWithDefaults(opts.GlobalPriceOpts),
		sacWrappers:          opts.SACWrappers,
		usdPeggedClassics:    opts.USDPeggedClassics,
		// 120s TTL on /v1/assets/{id} responses. MUST exceed the
		// selfPrewarmAssetEndpoints cadence (60s) with margin — at the
		// old 30s TTL the cache expired for 30 of every 60 seconds
		// between prewarm passes, so every probe landing in that window
		// (the status page polls /v1/assets/native every 30s) paid the
		// full cold-rebuild cost and inflated API p95/p99 (#52 / rc.67).
		// 120s = one full prewarm interval of headroom; matches the
		// sibling F2-path caches (1–2 min TTL, same 60s prewarm).
		// Underlying data updates per-minute at fastest; 120s staleness
		// still fits the ADR-0015 closed-bucket-only contract. Drift-safe
		// by construction — the cached entry IS what the handler
		// produces (see assetDetailResponseCache doc comment).
		assetDetailCache: newAssetDetailResponseCache(120 * time.Second),
		mux:              http.NewServeMux(),
		started:          time.Now().UTC(),
	}
	// Load + cache the embedded incident corpus once at startup;
	// the data is small (a few markdown files) and ships with the
	// binary, so re-parsing per-request is wasted work. New
	// incident posts ship with a redeploy.
	if loaded, err := incidents.Load(logger); err != nil {
		logger.Warn("incidents: load failed; /v1/incidents returns empty",
			"err", err)
	} else {
		s.incidents = loaded
	}
	s.mountRoutes()
	return s
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// globalPriceOptsWithDefaults backs `Options.GlobalPriceOpts` with
// [aggregate.DefaultGlobalPriceOptions] for any zero field so
// callers can supply just the aggregator source list and get
// sensible defaults for everything else.
func globalPriceOptsWithDefaults(o aggregate.GlobalPriceOptions) aggregate.GlobalPriceOptions {
	defaults := aggregate.DefaultGlobalPriceOptions()
	if o.VWAPMinTradeCount == 0 {
		o.VWAPMinTradeCount = defaults.VWAPMinTradeCount
	}
	if o.TriangulationWindow == 0 {
		o.TriangulationWindow = defaults.TriangulationWindow
	}
	if o.MaxAggregatorAge == 0 {
		o.MaxAggregatorAge = defaults.MaxAggregatorAge
	}
	return o
}

// Handler returns the mux wrapped in the standard middleware stack
// (outermost-first): RequestID → HTTPMetrics → Logger → Recoverer
// → SecurityHeaders → [optional CORS] → [optional RateLimit].
//
// HTTPMetrics sits inside RequestID so future trace-exemplar links
// work, and outside Logger+Recoverer so metrics count every
// request including those where the handler panicked.
//
// SecurityHeaders runs INSIDE Recoverer so a panic's 500
// problem+json response still carries the nosniff header — the
// recoverer synthesises a response header, and SecurityHeaders
// hasn't written yet at that point because the inner handler is
// what panics, not the middleware around it.
//
// CORS runs outside RateLimit so preflight OPTIONS requests don't
// consume rate-limit budget. RateLimit runs innermost — AFTER
// Logger populates remote_ip into the context, so
// middleware.RemoteIPFrom returns a meaningful key.
func (s *Server) Handler() http.Handler {
	stack := []middleware.Middleware{
		middleware.RequestID,
		obs.HTTPMetrics,
		middleware.Logger(s.logger),
		middleware.Recoverer(s.logger),
		// Security headers live inside Recoverer so even a panic's
		// 500 problem+json response carries nosniff. Cheap, always
		// safe, idempotent with any edge-proxy that also sets it.
		middleware.SecurityHeaders,
		// Cache-Control directives per route — set BEFORE handlers
		// run so writeJSON / writeProblem responses inherit the
		// directive. Handlers may override (Etag flows, immutable
		// historical buckets) by setting Cache-Control themselves.
		// CDN-tier `s-maxage` is gated on s.cdnEnabled so deployments
		// without a CDN don't emit a directive a CDN they don't run
		// could later honour.
		middleware.CacheControlWithCDN(s.cdnEnabled),
		// Convert Go's default text/plain 404 / 405 from the mux into
		// problem+json so unknown paths and method mismatches use the
		// same wire shape as the rest of our error surface. Sits AFTER
		// CacheControl so the override gets the same Cache-Control
		// directive a regular handler-side response would.
		middleware.Envelope404,
		// 308-redirect trailing-slash paths to their no-slash form
		// (e.g. /v1/assets/native/ → /v1/assets/native). Every v1
		// route is registered without a trailing slash; without this
		// middleware, clients that auto-append (axios with `/v1/`
		// baseURL, OpenAPI codegens, mistyped curl) hit a dead 404.
		// 308 preserves method+body so POST/DELETE don't degrade.
		middleware.TrailingSlashRedirect,
	}
	if s.cors != nil {
		stack = append(stack, s.cors)
	}
	// Auth runs INSIDE CORS (so preflight OPTIONS short-circuits
	// before any credential check) but OUTSIDE RateLimit (so
	// per-tier limits see the authenticated Subject in context).
	if s.auth != nil {
		stack = append(stack, s.auth)
	}
	// KeyPolicy runs after Auth (so the Subject is on context) but
	// before RateLimit (so a policy-denied 403 never spends a
	// rate-limit token). F-1226 (codex audit-2026-05-12).
	if s.keyPolicy != nil {
		stack = append(stack, s.keyPolicy)
	}
	// RequireEmailVerified runs after KeyPolicy (same "Subject
	// already resolved" precondition) and BEFORE rate-limit (so
	// an unverified-key 403 doesn't spend a per-minute token).
	// F-1218 wave 45 (codex audit-2026-05-12); opt-in per
	// deployment via the api binary's
	// cfg.API.SignupRequireEmailVerification flag.
	if s.requireEmailVerified != nil {
		stack = append(stack, s.requireEmailVerified)
	}
	// MonthlyQuota runs AFTER auth/key-policy (so the Subject is
	// on context) but BEFORE rate-limit (so a quota-rejected
	// request doesn't also spend a per-minute token). F-1226
	// (codex audit-2026-05-12).
	if s.monthlyQuota != nil {
		stack = append(stack, s.monthlyQuota)
	}
	if s.rateLimit != nil {
		stack = append(stack, s.rateLimit)
	}
	// Usage tracker runs INSIDE rate-limit so denied (429) requests
	// don't pollute per-day counters — only allowed traffic counts
	// against the user's billing window. Best-effort; failures
	// log at debug and never block.
	if s.usageTracker != nil {
		stack = append(stack, s.usageTracker)
	}
	// TouchUsage runs INSIDE rate-limit (and after the usage
	// tracker for ordering symmetry) so a denied (429) request
	// doesn't bump the dashboard's "last seen" column for the
	// rejected attempt. Wraps next.ServeHTTP — the actual touch
	// fires post-handler with a SETNX debounce so per-request
	// cost is bounded. F-1226 (codex audit-2026-05-12) wave 39.
	if s.touchUsage != nil {
		stack = append(stack, s.touchUsage)
	}
	// Session resolver runs INSIDE rate-limit so the per-account
	// rate limit could observe the dashboard subject in the future
	// (today only key-tier limits look at Subject; once the cutover
	// makes Postgres canonical, dashboard sessions can carry tier
	// info too). Either way the cookie is parsed once per request
	// and the result stays attached for the rest of the chain.
	if s.sessionAuth != nil {
		stack = append(stack, s.sessionAuth)
	}
	// CaptureRoute MUST be innermost — directly above the mux — so
	// r.Pattern is populated before it reads. It writes the matched
	// route into the *routeCapture HTTPMetrics planted in the
	// context, so the outermost metrics middleware can label by
	// route even though Logger's r.WithContext between them shadows
	// the original request struct. See obs.HTTPMetrics docstring
	// for the why.
	stack = append(stack, obs.CaptureRoute)
	return middleware.Chain(s.mux, stack...)
}

// Uptime returns how long this server has been running. Exposed
// for debugging / testing.
func (s *Server) Uptime() time.Duration { return time.Since(s.started) }

// loopbackOnly wraps `next` so it returns 404 for any request
// whose RemoteAddr is not a loopback IP (127.0.0.0/8 or ::1).
// Used for `/metrics` so the binary refuses to answer scrapes
// from anything but localhost — defense-in-depth against a
// misconfigured reverse proxy that forwards public traffic to
// the binary's :3000 port.
//
// Returns 404 (not 403) deliberately — 403 would confirm the
// route exists; 404 mirrors what a properly-configured Caddy
// would emit and gives no signal to a scanner.
func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr // RemoteAddr without port (rare)
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mountRoutes() { //nolint:funlen // route registration is intentionally one block for grep-ability; splitting into sub-functions makes "where is /v1/X served?" harder to answer.
	// Health / meta endpoints. Deliberately NOT behind rate-limit
	// middleware — infra (k8s probes, load balancers) hits these.
	s.mux.HandleFunc("GET /v1/issuers", s.handleIssuersList)
	s.mux.HandleFunc("GET /v1/issuers/{g_strkey}", s.handleIssuer)

	// Per-contract SEP-41 transfer audit-trail. F-0021 closure
	// (audit-2026-05-26): every transfer / approve / set_admin /
	// set_authorized event for a watched SEP-41 contract, with
	// optional ?from= / ?to= address filters. Unlocks per-account
	// net-position queries — the Stellar moat feature CG/CMC
	// structurally cannot offer.
	s.mux.HandleFunc("GET /v1/contracts/{contract_id}/transfers", s.handleSEP41Transfers)

	s.mux.HandleFunc("GET /v1/changes/{entity_type}/{id}", s.handleChangeSummary)
	s.mux.HandleFunc("GET /v1/diagnostics/cursors", s.handleCursors)
	s.mux.HandleFunc("GET /v1/diagnostics/ingestion", s.handleDiagnosticsIngestion)

	// Live-ingest frontier — a lightweight slice of the ingestion
	// snapshot (latest ingested ledger + lag). /tip is a 2s-cached
	// poll; /stream is the SSE counterpart that pushes one
	// ledger_update per new ledger so a status page renders blocks
	// arriving in real time.
	s.mux.HandleFunc("GET /v1/ledger/tip", s.handleLedgerTip)
	s.mux.HandleFunc("GET /v1/ledger/stream", s.handleLedgerStream)

	s.mux.HandleFunc("GET /v1/incidents", s.handleIncidents)
	s.mux.HandleFunc("GET /v1/incidents.atom", s.handleIncidentsAtom)
	s.mux.HandleFunc("GET /v1/network/stats", s.handleNetworkStats)
	s.mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /v1/readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /v1/version", s.handleVersion)
	s.mux.HandleFunc("GET /v1/status", s.handleStatus)

	// Prometheus scrape endpoint. Deliberately unversioned — it's
	// operator-facing, not part of the public API contract.
	//
	// Defense-in-depth: also gate at the Go layer on RemoteAddr
	// being a loopback address. The intended posture is that Caddy
	// 404s `/metrics` from public hosts (configs/caddy/Caddyfile.api)
	// and only the local Prometheus scraper hits the binary
	// directly via 127.0.0.1:3000. This guard catches the case where
	// the Caddyfile config is stale OR the binary is exposed behind
	// a different proxy that hasn't been audited. /metrics on a
	// public host fingerprints the deployment (Go runtime stats,
	// per-source counters, build info) — the cost of a missed
	// public hit is non-trivial enough to justify two layers of
	// blocking.
	s.mux.Handle("GET /metrics", loopbackOnly(obs.Handler()))

	// Asset catalogue.
	s.mux.HandleFunc("GET /v1/assets", s.handleAssetList)
	// /v1/assets/verified must register before /v1/assets/{asset_id}
	// — Go 1.22+ ServeMux picks the more-specific pattern, but
	// listing the static path first keeps the precedence obvious
	// to anyone reading the mount order.
	s.mux.HandleFunc("GET /v1/assets/verified", s.handleAssetsVerified)
	s.mux.HandleFunc("GET /v1/assets/{asset_id}", s.handleAssetGet)
	// /v1/assets/{asset_id}/metadata is more specific than
	// /v1/assets/{asset_id}/{network} (literal segment beats
	// wildcard); Go's mux handles the precedence, but listing the
	// literal route first keeps the ordering obvious.
	s.mux.HandleFunc("GET /v1/assets/{asset_id}/metadata", s.handleAssetMetadata)
	// Per-network drill-down (R-018 assets-unification step 3). When
	// {asset_id} is a verified-currency slug, the handler dispatches
	// per network: Stellar redirects to the canonical asset_id view;
	// non-Stellar returns a thin PerNetworkAssetView with the catalogue's
	// contract + external_link.
	s.mux.HandleFunc("GET /v1/assets/{asset_id}/{network}", s.handleAssetByNetwork)

	// Current price — last-trade fallback today; VWAP path when
	// the aggregator ships.
	s.mux.HandleFunc("GET /v1/price", s.handlePrice)

	// Rolling-window tip surface (ADR-0018) — VWAP over the last
	// few seconds, falling back to last-good-price when the window
	// is empty. NOT cross-region consistent; use /v1/price for that.
	s.mux.HandleFunc("GET /v1/price/tip", s.handlePriceTip)

	// SSE counterpart of /v1/price/tip — same compute logic, pushed
	// on a per-connection tick. See ADR-0018 §"SSE wires onto the
	// tip surface".
	s.mux.HandleFunc("GET /v1/price/tip/stream", s.handlePriceTipStream)

	// Raw per-source observations (ADR-0018 Surface 3) — array of
	// most-recent trade per source for the pair. No aggregation; the
	// rawest of the three consistency surfaces.
	s.mux.HandleFunc("GET /v1/observations", s.handleObservations)

	// SSE counterpart of /v1/observations — same compute, pushed on
	// a per-connection tick. interval_seconds tunes cadence.
	s.mux.HandleFunc("GET /v1/observations/stream", s.handleObservationsStream)

	// Closed-bucket SSE — fed by the aggregator publishing into the
	// shared Hub on each window close. Carries the strict ADR-0015
	// closed-bucket consistency contract that /v1/price serves.
	s.mux.HandleFunc("GET /v1/price/stream", s.handlePriceStream)

	// Batch price lookup, up to 100 assets per request.
	s.mux.HandleFunc("GET /v1/price/batch", s.handlePriceBatch)

	// Batch price lookup via JSON body — same shape, raises the
	// per-request ceiling to 1000.
	s.mux.HandleFunc("POST /v1/price/batch", s.handlePriceBatchPost)

	// Trade history within a time window.
	s.mux.HandleFunc("GET /v1/history", s.handleHistory)

	// Aggregated history at a granularity over the asset's full
	// indexed range. CAGG-served (prices_<granularity>); per
	// ADR-0015 only closed buckets returned.
	s.mux.HandleFunc("GET /v1/history/since-inception", s.handleHistorySinceInception)

	// Rolling-window chart series matching the Freighter RFP shape
	// (timeframe, granularity, price_type). Per ADR-0020.
	s.mux.HandleFunc("GET /v1/chart", s.handleChart)

	// Single-bar OHLC over a time window.
	s.mux.HandleFunc("GET /v1/ohlc", s.handleOHLC)

	// Volume-weighted average price over a time window.
	s.mux.HandleFunc("GET /v1/vwap", s.handleVWAP)

	// Time-weighted average price over a time window.
	s.mux.HandleFunc("GET /v1/twap", s.handleTWAP)

	// Distinct trading pairs.
	s.mux.HandleFunc("GET /v1/markets", s.handleMarkets)

	// Per-pool listing — every (source, base, quote) tuple in the
	// recency window. Backs the /dexes table on the explorer.
	s.mux.HandleFunc("GET /v1/pools", s.handlePools)

	// Single-pair activity summary.
	s.mux.HandleFunc("GET /v1/pairs", s.handlePairs)

	// Latest oracle readings per source for an asset.
	s.mux.HandleFunc("GET /v1/oracle/latest", s.handleOracleLatest)

	// Every active oracle stream — one row per (source, asset, quote)
	// triple, latest observation in the trailing 7d window. Backs
	// the explorer's /oracles "price streams" table.
	s.mux.HandleFunc("GET /v1/oracle/streams", s.handleOracleStreams)

	// SEP-40 passthrough surface — same data as /v1/price, reshaped
	// to the single-quote SEP-40 contract that on-chain oracle
	// readers expect. Quote fixed at fiat:USD on /lastprice;
	// /x_last_price takes explicit base + quote.
	s.mux.HandleFunc("GET /v1/oracle/lastprice", s.handleOracleLastPrice)
	s.mux.HandleFunc("GET /v1/oracle/prices", s.handleOraclePrices)
	s.mux.HandleFunc("GET /v1/oracle/x_last_price", s.handleOracleXLastPrice)

	// Lending — Blend pools observed in the auction stream.
	s.mux.HandleFunc("GET /v1/lending/pools", s.handleLendingPools)

	// Source catalogue — every venue the aggregator knows about,
	// with class + IncludeInVWAP metadata.
	s.mux.HandleFunc("GET /v1/sources", s.handleSources)

	// Methodology — machine-readable summary of the active
	// aggregation policy (VWAP method, outlier filters,
	// stablecoin proxy, source classes, ADR refs). Mirrors what
	// the explorer's /methodology HTML page documents, in a form
	// transparency consumers can parse. R-023.
	s.mux.HandleFunc("GET /v1/methodology", s.handleMethodology)

	// SAC wrapper resolution — operator-config map of
	// Stellar-Asset-Contract C-strkey → "CODE-ISSUER" classic asset.
	// Used by the explorer to render Soroban DEX pools (Soroswap /
	// Phoenix / Aquarius / Comet) with readable asset symbols
	// instead of raw C-strkeys.
	s.mux.HandleFunc("GET /v1/sac-wrappers", s.handleSACWrappers)

	// Account self-service. /me and /usage require an authenticated
	// Subject; /keys (POST) additionally requires the AccountStore
	// to be wired (typically only when Redis is reachable). All
	// three return 401 for anonymous callers.
	s.mux.HandleFunc("GET /v1/account/me", s.handleAccountMe)
	s.mux.HandleFunc("GET /v1/account/usage", s.handleAccountUsage)
	s.mux.HandleFunc("GET /v1/account/keys", s.handleAccountKeysList)
	s.mux.HandleFunc("POST /v1/account/keys", s.handleAccountKeysCreate)
	s.mux.HandleFunc("DELETE /v1/account/keys/{keyID}", s.handleAccountKeysRevoke)
	s.mux.HandleFunc("POST /v1/signup", s.handleSignup)
	// F-1218 (codex audit-2026-05-12): email-ownership-proof
	// flow. The signup handler issues a token (subsequent
	// wave) and emails it; this endpoint consumes the token
	// from the click-through link.
	s.mux.HandleFunc("GET /v1/signup/verify", s.handleSignupVerify)
	s.mux.HandleFunc("POST /v1/webhooks/stripe", s.handleStripeWebhook)

	// Customer-dashboard magic-link auth — POST /v1/auth/login +
	// GET /v1/auth/callback + POST /v1/auth/logout. Mounted only
	// when main.go wired a non-nil DashboardAuth (gated on Postgres
	// reachable + cfg.API.Dashboard.BaseURL non-empty); otherwise
	// the routes don't exist and ServeMux returns the standard 404.
	if s.dashboardAuth != nil {
		s.dashboardAuth.Mount(s.mux)
	}

	// Dashboard key-management routes — gated internally on the
	// session cookie planted by DashboardAuth's middleware. Mount
	// only when main.go wired Postgres for the platform stores.
	if s.dashboardKeys != nil {
		s.dashboardKeys.Mount(s.mux)
	}
	// Dashboard webhook-management routes (F-1270). Same
	// session-cookie + Postgres-wiring gate as dashboardKeys above.
	if s.dashboardWebhooks != nil {
		s.dashboardWebhooks.Mount(s.mux)
	}

	// SEP-10 Web Auth. Both endpoints are unauthenticated by design
	// — challenge bootstraps auth from a public Stellar G-strkey;
	// the JWT issued by /token is what authenticates subsequent
	// requests. The validator is wired only when the binary has
	// the server-signing seed + JWT secret configured.
	s.mux.HandleFunc("GET /v1/auth/sep10/challenge", s.handleSEP10Challenge)
	s.mux.HandleFunc("POST /v1/auth/sep10/token", s.handleSEP10Token)

	// Bare-root welcome. GET / lands accidental visitors on a
	// friendly envelope pointing at the docs. The `{$}` anchor means
	// this pattern matches ONLY the literal "/" — it does not catch
	// `/anything-else`, so ServeMux's 405 method-mismatch detection
	// for known paths stays intact. Unknown paths fall through to
	// envelope404Middleware (see Handler()) which converts Go's
	// default text/plain 404 / 405 responses into RFC 9457
	// problem+json.
	s.mux.HandleFunc("GET /{$}", s.handleRoot)

	// /robots.txt — disallow crawler indexing of the API hostname.
	// The endpoints are JSON, not user-facing HTML; crawlers
	// hitting them waste their budget on payloads that won't rank
	// for any meaningful search query. The companion explorer site
	// (ratesengine.net) and docs site (docs.ratesengine.net) are
	// where indexable content lives, with their own robots.txt
	// directives. Without this handler Cloudflare's auto-managed
	// robots.txt is served on GET but the API origin returns 404
	// on HEAD — flagging the inconsistency is what surfaced this
	// gap in the 2026-05-09 audit.
	s.mux.HandleFunc("GET /robots.txt", s.handleRobotsTxt)

	// /.well-known/security.txt — RFC 9116 disclosure metadata.
	// Researchers scanning the API origin for vulnerabilities find
	// the disclosure email here without having to traverse to the
	// explorer subdomain. The Canonical: directive points at the
	// explorer's copy so the two stay aligned without drift.
	s.mux.HandleFunc("GET /.well-known/security.txt", s.handleSecurityTxt)
}

// ─── Handlers ─────────────────────────────────────────────────────

// healthResponse is the shape for /healthz + /readyz.
type healthResponse struct {
	Status string `json:"status"` // ok | degraded
	// Uptime is a human-readable duration. Precise-to-the-second is
	// fine for monitoring.
	Uptime string `json:"uptime"`
	// Checks is populated on /readyz with per-dependency results.
	// Absent on /healthz.
	Checks []checkResult `json:"checks,omitempty"`
	// StatusRoot points consumers at /v1/status for the rich
	// rollup that covers ingest lag, supply, oracle freshness,
	// and per-pair SLA latency — F-1210 (codex audit-2026-05-12).
	// Static "/v1/status" today; surfaced here so a probe
	// consumer following only /healthz / /readyz can still find
	// the SLA-truth endpoint without out-of-band knowledge.
	StatusRoot string `json:"status_root,omitempty"`
}

type checkResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	// Error is populated only when OK is false; freeform string.
	Error string `json:"error,omitempty"`
}

// handleHealthz is the shallow liveness probe. Returns 200 as long
// as the process is running + mux is serving. Does NOT touch the
// database or Redis — those are the readiness probe's job.
//
// F-1210 (codex audit-2026-05-12): /healthz and /readyz are
// deliberately scoped to the serving-plane (process, postgres,
// redis). They do NOT report ingest lag, supply state, oracle
// freshness, or per-pair SLA latency. The rich rollup lives at
// `/v1/status`, which aggregates Prometheus-backed signals. The
// scoping is intentional: liveness probes (k8s, systemd) must
// not flap when a backfill stalls or when one source goes silent;
// those are SLO concerns surfaced separately. The healthz response
// links to /v1/status so operators using either endpoint find the
// authoritative view.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, healthResponse{
		Status:     "ok",
		Uptime:     s.Uptime().Truncate(time.Second).String(),
		StatusRoot: "/v1/status",
	}, Flags{})
}

// handleReadyz is the deep readiness probe. Pings every registered
// ReadyChecker in parallel with a short shared timeout. 200 only if
// all pass; 503 otherwise.
//
// Parallelism matters: with 3 checks at 500ms each, serial execution
// uses 1.5s of the 2s budget; parallel uses the max of any single
// check. The k8s liveness-probe timeout is typically 1s — blowing
// past it flaps the pod.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	results := make([]checkResult, len(s.checks))
	criticalFlags := make([]bool, len(s.checks))
	var wg sync.WaitGroup
	for i, c := range s.checks {
		wg.Add(1)
		criticalFlags[i] = c.Critical()
		go func(i int, c ReadyChecker) {
			defer wg.Done()
			err := c.Ping(ctx)
			r := checkResult{Name: c.Name(), OK: err == nil}
			if err != nil {
				r.Error = err.Error()
			}
			results[i] = r // distinct indices — no mutex needed
		}(i, c)
	}
	wg.Wait()

	// F-1275 (codex audit-2026-05-13): split fail-cases into
	// critical (503) vs non-critical (200 with status="degraded").
	// Pre-wave-110 a Redis outage would 503 readyz and HAProxy
	// would drain every healthy API backend even though Timescale
	// fallback kept the customer-facing surface serving correctly.
	criticalFailed := false
	anyFailed := false
	for i, r := range results {
		if r.OK {
			continue
		}
		anyFailed = true
		if criticalFlags[i] {
			criticalFailed = true
		}
	}

	resp := healthResponse{
		Status:     "ok",
		Uptime:     s.Uptime().Truncate(time.Second).String(),
		Checks:     results,
		StatusRoot: "/v1/status",
	}
	switch {
	case criticalFailed:
		resp.Status = "unready"
		env := Envelope{
			Data:  resp,
			AsOf:  time.Now().UTC(),
			Flags: Flags{Stale: true},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(env)
		return
	case anyFailed:
		// Non-critical dependency degraded — API still serves
		// (Timescale fallback for Redis cache misses per
		// ADR-0007); 200 keeps the backend in HAProxy's pool;
		// the response body's status="degraded" + per-check
		// breakdown tells operators what's down.
		resp.Status = "degraded"
		writeJSON(w, resp, Flags{Stale: true})
		return
	}

	writeJSON(w, resp, Flags{})
}

// handleVersion reports binary version + build date + VCS info.
//
// Operators use this for quick fleet-wide "what's running" checks
// over the API rather than ssh-ing into every host. `version` is
// the human-readable git-describe; `commit` is the full VCS SHA;
// `dirty` reports whether the build tree had uncommitted changes
// (production builds should always be `dirty=false`); `go_version`
// is the runtime Go version.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{
		"version":    version.Version,
		"build_date": version.BuildDate,
		"commit":     version.Commit,
		"dirty":      version.Dirty,
		"go_version": version.GoVersion,
	}, Flags{})
}

// handleSecurityTxt serves /.well-known/security.txt per RFC 9116.
//
// The Canonical: URL points at the explorer copy
// (ratesengine.net/.well-known/security.txt) so the two origins
// don't drift; both the explorer and API surfaces deliberately
// share the same disclosure email + policy URL. Expires is one
// year out — handler runs at request time so it always returns a
// valid future date as long as the binary is up.
func (s *Server) handleSecurityTxt(w http.ResponseWriter, _ *http.Request) {
	expires := time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339)
	body := "# Rates Engine — security.txt (api origin)\n" +
		"# RFC-9116. Mirrors ratesengine.net/.well-known/security.txt;\n" +
		"# the Canonical: URL is the authoritative copy.\n" +
		"\n" +
		"Contact: mailto:security@ratesengine.net\n" +
		"Expires: " + expires + "\n" +
		"Preferred-Languages: en\n" +
		"Canonical: https://ratesengine.net/.well-known/security.txt\n" +
		"Policy: https://github.com/RatesEngine/rates-engine/blob/main/SECURITY.md\n" +
		"Acknowledgments: https://github.com/RatesEngine/rates-engine/security/advisories\n"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(body))
}

// handleRoot welcomes accidental visitors at GET /. Returns a small
// envelope with the binary version + a pointer at the docs; not part
// of the public API surface (no OpenAPI entry), strictly a "you've
// reached the API hostname" affordance.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{
		"name":    "rates-engine",
		"version": version.Version,
		"docs":    "https://docs.ratesengine.net",
		"openapi": "https://docs.ratesengine.net/openapi.yaml",
	}, Flags{})
}

// handleRobotsTxt serves /robots.txt. The API origin holds JSON
// endpoints not meant for crawler indexing — point search engines
// at the companion docs + explorer subdomains instead. The
// `Sitemap:` directive lets a crawler that ignored the Disallow
// (or has a per-bot exception) at least crawl what's worth
// indexing.
func (s *Server) handleRobotsTxt(w http.ResponseWriter, _ *http.Request) {
	const body = `# api.ratesengine.net — JSON API, not for human reading.
# Indexable content lives on the companion subdomains:
#   - https://ratesengine.net          — explorer + market UI
#   - https://docs.ratesengine.net     — API reference
#   - https://status.ratesengine.net   — status + incident postmortems

User-agent: *
Disallow: /

Sitemap: https://ratesengine.net/sitemap.xml
`
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(body))
}
