package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/streaming"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/incidents"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// ReadyChecker is the interface /readyz polls to decide whether
// the serving-plane dependencies are responsive. Implementations
// in cmd/ratesengine-api/main.go:
//
//   - storeChecker (wraps *timescale.Store.DB().PingContext)
//   - redisChecker (wraps *redis.Client.Ping)
//
// Ping MUST respect ctx and return promptly on cancellation — the
// handler runs every checker in parallel under a shared 2 s
// deadline; a misbehaving checker that ignores ctx can turn readyz
// into a cascade-failure vector for the liveness probe.
type ReadyChecker interface {
	Ping(ctx context.Context) error
	Name() string
}

// Server is the HTTP handler for the Rates Engine v1 API.
//
// Construction: [New] returns a Server with routes mounted.
// Call [Server.Handler] to get an http.Handler for an
// [http.Server].
//
// Thread-safe.
type Server struct {
	logger           *slog.Logger
	checks           []ReadyChecker
	assets           AssetReader
	prices           PriceReader
	history          HistoryReader
	markets          MarketsReader
	oracle           OracleReader
	meta             MetadataResolver
	accounts         AccountStore
	signups          SignupTracker
	stripe           *StripeWebhookConfig
	divergence       DivergenceLooker
	freeze           FrozenLooker
	supply           SupplyLooker
	volume           VolumeReader
	change24h        Change24hReader
	changesum        ChangeSummaryReader
	coins            CoinsReader
	issuers          IssuersReader
	cursors          CursorsReader
	networkStats     NetworkStatsReader
	sourcesStats     SourcesStatsReader
	lending          LendingReader
	currencies       CurrenciesReader
	fxHistory        FXHistoryReader
	sessionPeeker    SessionPeeker
	incidents        []incidents.Incident
	sep10            auth.SEP10Validator
	cors             middleware.Middleware
	auth             middleware.Middleware
	rateLimit        middleware.Middleware
	usageTracker     middleware.Middleware
	usageReader      UsageReader
	hub              *streaming.Hub
	confidence       ConfidenceLooker
	triangulated     TriangulatedPriceLooker
	cdnEnabled       bool
	statusBackend    StatusBackend
	regionName       string
	regionDeployment string
	dashboardAuth    DashboardAuthMounter
	dashboardKeys    DashboardAuthMounter
	sessionAuth      middleware.Middleware
	// sacWrappers is the operator-config map of Stellar-Asset-Contract
	// C-strkey → "CODE-ISSUER" canonical asset key. Surfaced on
	// /v1/sac-wrappers so the explorer can resolve raw Soroban
	// contract addresses (which Soroswap/Phoenix/Aquarius/Comet
	// emit as base/quote in their swap events) back to readable
	// asset symbols. Nil means "operator hasn't configured the map"
	// — the endpoint serves an empty object.
	sacWrappers map[string]string
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

	// Coins, when non-nil, backs GET /v1/coins. Production wiring
	// is timescale.Store directly (it implements ListCoins). Nil
	// makes the endpoint return 503.
	Coins CoinsReader

	// Issuers, when non-nil, backs GET /v1/issuers/{g_strkey}.
	// Production wiring is timescale.Store directly. Nil makes
	// the endpoint return 503.
	Issuers IssuersReader

	// Cursors, when non-nil, backs GET /v1/diagnostics/cursors.
	// Production wiring is timescale.Store directly (it implements
	// ListCursors). Nil makes the endpoint return 503. Operator-
	// facing diagnostic; powers the explorer /diagnostics page.
	Cursors CursorsReader

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

	// Currencies, when non-nil, backs /v1/currencies — the world
	// fiat-currency rates listing. Leave nil and the handler serves
	// an empty currencies list ("warming up") with the source label
	// still populated.
	Currencies CurrenciesReader

	// FXHistory, when non-nil, lets /v1/currencies/{ticker} surface
	// long-form persisted history (fx_quotes hypertable) when the
	// request carries `?range=1y` etc. Leave nil to keep the handler
	// in 7d-only mode.
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
}

// New constructs a Server and mounts all v1 routes.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:            logger,
		checks:            opts.ReadyChecks,
		assets:            opts.Assets,
		prices:            opts.Prices,
		history:           opts.History,
		markets:           opts.Markets,
		oracle:            opts.Oracle,
		meta:              opts.Meta,
		accounts:          opts.Accounts,
		signups:           opts.Signups,
		stripe:            opts.Stripe,
		divergence:        opts.Divergence,
		freeze:            opts.Freeze,
		supply:            opts.Supply,
		volume:            opts.Volume,
		change24h:         opts.Change24h,
		changesum:         opts.ChangeSummary,
		coins:             opts.Coins,
		issuers:           opts.Issuers,
		cursors:           opts.Cursors,
		networkStats:      opts.NetworkStats,
		sourcesStats:      opts.SourcesStats,
		lending:           opts.Lending,
		currencies:        opts.Currencies,
		fxHistory:         opts.FXHistory,
		sessionPeeker:     opts.SessionPeeker,
		sep10:             opts.SEP10,
		cors:              opts.CORS,
		auth:              opts.Auth,
		rateLimit:         opts.RateLimit,
		usageTracker:      opts.UsageTracker,
		usageReader:       opts.UsageReader,
		hub:               opts.Hub,
		confidence:        opts.Confidence,
		triangulated:      opts.Triangulated,
		cdnEnabled:        opts.CDNEnabled,
		statusBackend:     opts.StatusBackend,
		regionName:        valueOr(opts.RegionName, "unknown"),
		regionDeployment:  valueOr(opts.RegionDeployment, "production"),
		dashboardAuth:     opts.DashboardAuth,
		dashboardKeys:     opts.DashboardKeys,
		sessionAuth:       opts.SessionAuth,
		sacWrappers:       opts.SACWrappers,
		usdPeggedClassics: opts.USDPeggedClassics,
		mux:               http.NewServeMux(),
		started:           time.Now().UTC(),
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
		// (e.g. /v1/coins/native/ → /v1/coins/native). Every v1
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

func (s *Server) mountRoutes() {
	// Health / meta endpoints. Deliberately NOT behind rate-limit
	// middleware — infra (k8s probes, load balancers) hits these.
	s.mux.HandleFunc("GET /v1/coins", s.handleCoins)
	s.mux.HandleFunc("GET /v1/coins/{slug}", s.handleCoin)
	s.mux.HandleFunc("GET /v1/issuers", s.handleIssuersList)
	s.mux.HandleFunc("GET /v1/issuers/{g_strkey}", s.handleIssuer)
	s.mux.HandleFunc("GET /v1/changes/{entity_type}/{id}", s.handleChangeSummary)
	s.mux.HandleFunc("GET /v1/diagnostics/cursors", s.handleCursors)
	s.mux.HandleFunc("GET /v1/incidents", s.handleIncidents)
	s.mux.HandleFunc("GET /v1/incidents.atom", s.handleIncidentsAtom)
	s.mux.HandleFunc("GET /v1/network/stats", s.handleNetworkStats)
	s.mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /v1/readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /v1/version", s.handleVersion)
	s.mux.HandleFunc("GET /v1/status", s.handleStatus)

	// Prometheus scrape endpoint. Deliberately unversioned — it's
	// operator-facing, not part of the public API contract.
	s.mux.Handle("GET /metrics", obs.Handler())

	// Asset catalogue.
	s.mux.HandleFunc("GET /v1/assets", s.handleAssetList)
	s.mux.HandleFunc("GET /v1/assets/{asset_id}", s.handleAssetGet)
	s.mux.HandleFunc("GET /v1/assets/{asset_id}/metadata", s.handleAssetMetadata)

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

	// Currencies — world fiat rates vs USD (currency-api shim).
	s.mux.HandleFunc("GET /v1/currencies", s.handleCurrencies)
	s.mux.HandleFunc("GET /v1/currencies/{ticker}", s.handleCurrencyDetail)

	// Source catalogue — every venue the aggregator knows about,
	// with class + IncludeInVWAP metadata.
	s.mux.HandleFunc("GET /v1/sources", s.handleSources)

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
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, healthResponse{
		Status: "ok",
		Uptime: s.Uptime().Truncate(time.Second).String(),
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
	var wg sync.WaitGroup
	for i, c := range s.checks {
		wg.Add(1)
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

	allOK := true
	for _, r := range results {
		if !r.OK {
			allOK = false
			break
		}
	}

	resp := healthResponse{
		Status: "ok",
		Uptime: s.Uptime().Truncate(time.Second).String(),
		Checks: results,
	}
	if !allOK {
		resp.Status = "degraded"
		env := Envelope{
			Data:  resp,
			AsOf:  time.Now().UTC(),
			Flags: Flags{Stale: true},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(env)
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
