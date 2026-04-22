package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/version"
)

// ReadyChecker is the interface /readyz polls to decide whether
// the serving-plane dependencies are responsive. Implementations:
//
//   - *timescale.Store (wraps PingContext).
//   - a redis-client adapter (future).
//
// Kept narrow so tests can plug in stubs.
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
	logger  *slog.Logger
	checks  []ReadyChecker
	assets  AssetReader
	prices  PriceReader
	mux     *http.ServeMux
	started time.Time
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
}

// New constructs a Server and mounts all v1 routes.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		logger:  logger,
		checks:  opts.ReadyChecks,
		assets:  opts.Assets,
		prices:  opts.Prices,
		mux:     http.NewServeMux(),
		started: time.Now().UTC(),
	}
	s.mountRoutes()
	return s
}

// Handler returns the mux wrapped in the standard middleware stack
// (outermost-first): RequestID → Logger → Recoverer. RateLimit +
// CORS are applied by callers that have those pieces wired
// (typically the api binary, per docs/reference/api-design.md
// §6–§7).
func (s *Server) Handler() http.Handler {
	return middleware.Chain(s.mux,
		middleware.RequestID,
		middleware.Logger(s.logger),
		middleware.Recoverer(s.logger),
	)
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

	// Asset catalogue.
	s.mux.HandleFunc("GET /v1/assets", s.handleAssetList)
	s.mux.HandleFunc("GET /v1/assets/{asset_id}", s.handleAssetGet)

	// Current price — last-trade fallback today; VWAP path when
	// the aggregator ships.
	s.mux.HandleFunc("GET /v1/price", s.handlePrice)

	// TODO(#0): /v1/history, /v1/ohlc, /v1/markets, /v1/pairs,
	// /v1/oracle/*, /v1/account/* — follow-up PRs per
	// docs/reference/api-design.md §5.
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
// ReadyChecker in parallel with a short timeout. 200 only if all
// pass; 503 otherwise.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	results := make([]checkResult, len(s.checks))
	allOK := true
	for i, c := range s.checks {
		err := c.Ping(ctx)
		results[i] = checkResult{Name: c.Name(), OK: err == nil}
		if err != nil {
			allOK = false
			results[i].Error = err.Error()
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

// handleVersion reports binary version + build date.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"version":    version.Version,
		"build_date": version.BuildDate,
	}, Flags{})
}
