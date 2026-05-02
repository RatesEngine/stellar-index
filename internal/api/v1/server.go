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
	logger       *slog.Logger
	checks       []ReadyChecker
	assets       AssetReader
	prices       PriceReader
	history      HistoryReader
	markets      MarketsReader
	oracle       OracleReader
	meta         MetadataResolver
	accounts     AccountStore
	divergence   DivergenceLooker
	freeze       FrozenLooker
	supply       SupplyLooker
	volume       VolumeReader
	sep10        auth.SEP10Validator
	cors         middleware.Middleware
	auth         middleware.Middleware
	rateLimit    middleware.Middleware
	hub          *streaming.Hub
	confidence   ConfidenceLooker
	triangulated TriangulatedPriceLooker
	cdnEnabled   bool
	mux          *http.ServeMux
	started      time.Time
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
}

// New constructs a Server and mounts all v1 routes.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:       logger,
		checks:       opts.ReadyChecks,
		assets:       opts.Assets,
		prices:       opts.Prices,
		history:      opts.History,
		markets:      opts.Markets,
		oracle:       opts.Oracle,
		meta:         opts.Meta,
		accounts:     opts.Accounts,
		divergence:   opts.Divergence,
		freeze:       opts.Freeze,
		supply:       opts.Supply,
		volume:       opts.Volume,
		sep10:        opts.SEP10,
		cors:         opts.CORS,
		auth:         opts.Auth,
		rateLimit:    opts.RateLimit,
		hub:          opts.Hub,
		confidence:   opts.Confidence,
		triangulated: opts.Triangulated,
		cdnEnabled:   opts.CDNEnabled,
		mux:          http.NewServeMux(),
		started:      time.Now().UTC(),
	}
	s.mountRoutes()
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
	return middleware.Chain(s.mux, stack...)
}

// Uptime returns how long this server has been running. Exposed
// for debugging / testing.
func (s *Server) Uptime() time.Duration { return time.Since(s.started) }

func (s *Server) mountRoutes() {
	// Health / meta endpoints. Deliberately NOT behind rate-limit
	// middleware — infra (k8s probes, load balancers) hits these.
	s.mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /v1/readyz", s.handleReadyz)
	s.mux.HandleFunc("GET /v1/version", s.handleVersion)

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

	// Single-pair activity summary.
	s.mux.HandleFunc("GET /v1/pairs", s.handlePairs)

	// Latest oracle readings per source for an asset.
	s.mux.HandleFunc("GET /v1/oracle/latest", s.handleOracleLatest)

	// SEP-40 passthrough surface — same data as /v1/price, reshaped
	// to the single-quote SEP-40 contract that on-chain oracle
	// readers expect. Quote fixed at fiat:USD on /lastprice;
	// /x_last_price takes explicit base + quote.
	s.mux.HandleFunc("GET /v1/oracle/lastprice", s.handleOracleLastPrice)
	s.mux.HandleFunc("GET /v1/oracle/prices", s.handleOraclePrices)
	s.mux.HandleFunc("GET /v1/oracle/x_last_price", s.handleOracleXLastPrice)

	// Source catalogue — every venue the aggregator knows about,
	// with class + IncludeInVWAP metadata.
	s.mux.HandleFunc("GET /v1/sources", s.handleSources)

	// Account self-service. /me and /usage require an authenticated
	// Subject; /keys (POST) additionally requires the AccountStore
	// to be wired (typically only when Redis is reachable). All
	// three return 401 for anonymous callers.
	s.mux.HandleFunc("GET /v1/account/me", s.handleAccountMe)
	s.mux.HandleFunc("GET /v1/account/usage", s.handleAccountUsage)
	s.mux.HandleFunc("POST /v1/account/keys", s.handleAccountKeysCreate)

	// SEP-10 Web Auth. Both endpoints are unauthenticated by design
	// — challenge bootstraps auth from a public Stellar G-strkey;
	// the JWT issued by /token is what authenticates subsequent
	// requests. The validator is wired only when the binary has
	// the server-signing seed + JWT secret configured.
	s.mux.HandleFunc("GET /v1/auth/sep10/challenge", s.handleSEP10Challenge)
	s.mux.HandleFunc("POST /v1/auth/sep10/token", s.handleSEP10Token)
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
