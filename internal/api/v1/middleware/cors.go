package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/RatesEngine/rates-engine/internal/obs"
)

// CORSOptions configures [CORS]. Pass a zero-valued struct for the
// conservative default (no origins allowed — same-origin only).
type CORSOptions struct {
	// AllowedOrigins is the exact-match allow-list of `Origin:`
	// values the middleware honours. Special form `*` means "allow
	// any origin" (wildcard); use sparingly — it's incompatible
	// with `Access-Control-Allow-Credentials: true`, which we don't
	// set anyway.
	//
	// Empty slice = no cross-origin access permitted.
	AllowedOrigins []string

	// AllowedMethods defaults to {GET, HEAD, OPTIONS, POST} when
	// empty — matches the v1 surface (POST /v1/account/keys,
	// POST /v1/auth/sep10/token, POST /v1/price/batch). Operators
	// who want a stricter cross-origin posture set the field
	// explicitly (e.g. drop POST when their browser clients only
	// read).
	AllowedMethods []string

	// AllowedHeaders is the list of non-safe-listed headers clients
	// may include on cross-origin requests. Defaults to
	// {Content-Type, Authorization, X-Request-Id}.
	AllowedHeaders []string

	// MaxAge is the cache duration for the preflight response, in
	// seconds. Defaults to 600 (10 min) — long enough to amortise
	// preflight overhead without going so far that rotating the
	// policy becomes slow. Browsers silently cap at 2h.
	MaxAge int
}

// CORS returns middleware that applies W3C CORS headers based on
// opts. Deliberately conservative: no credentialed requests, no
// dynamic origin reflection beyond the exact-match / wildcard
// allow-list. Sophisticated needs (per-route origins, pattern
// matching) are out of scope — callers that want those should use
// rs/cors or implement inline.
//
// Behaviour:
//
//   - On OPTIONS requests with an Origin header: emits the
//     preflight response (Allow-Origin/Methods/Headers/Max-Age) and
//     returns 204 without calling next.
//   - On all other requests: emits Access-Control-Allow-Origin iff
//     the request's Origin is in the allow-list, then calls next.
//   - When AllowedOrigins contains "*": the wildcard is echoed
//     back instead of reflecting the specific origin. Matches the
//     spec + keeps the middleware simple.
func CORS(opts CORSOptions) Middleware { //nolint:gocognit // origin allow-list + preflight branch + Vary handling are all part of one cohesive CORS contract; splitting would scatter the policy
	allowed := buildOriginSet(opts.AllowedOrigins)
	wildcard := allowed["*"]
	methods := strings.Join(defaultIfEmpty(opts.AllowedMethods,
		[]string{"GET", "HEAD", "OPTIONS", "POST"}), ", ")
	headers := strings.Join(defaultIfEmpty(opts.AllowedHeaders,
		[]string{"Content-Type", "Authorization", "X-Request-Id"}), ", ")
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = 600
	}
	maxAgeStr := strconv.Itoa(maxAge)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			originAllowed := wildcard || (origin != "" && allowed[origin])

			// Always add `Vary: Origin` (except in strict-wildcard
			// mode where `Allow-Origin: *` applies regardless of
			// the requesting origin). Without this, a cacheable
			// response served to a no-Origin request (curl, server-
			// side fetch) is keyed without origin discrimination —
			// a CDN can then return that cached "no CORS" response
			// to a later browser request whose Origin WOULD have
			// been allowed, breaking the second client's CORS.
			// Inverse poisoning is also possible: a response cached
			// with one allowed Origin's `Allow-Origin: <a>` could
			// be served to a request from a different allowed
			// Origin <b>, also breaking CORS.
			if !wildcard {
				w.Header().Add("Vary", "Origin")
			}

			if originAllowed {
				if wildcard {
					w.Header().Set("Access-Control-Allow-Origin", "*")
				} else {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}

			// Per-request CORS observability (F-1244). One increment
			// per request so operators can dashboard cross-origin
			// traffic patterns and alert when a wildcard policy
			// starts handling real cross-origin traffic in prod.
			switch {
			case origin == "":
				obs.APICORSDecisionsTotal.WithLabelValues("no_origin").Inc()
			case originAllowed && wildcard:
				obs.APICORSDecisionsTotal.WithLabelValues("allowed_wildcard").Inc()
			case originAllowed:
				obs.APICORSDecisionsTotal.WithLabelValues("allowed_origin").Inc()
			default:
				obs.APICORSDecisionsTotal.WithLabelValues("denied").Inc()
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				// Preflight. Emit the allow-methods/headers/max-age
				// response and don't forward to the mux.
				if originAllowed {
					w.Header().Set("Access-Control-Allow-Methods", methods)
					w.Header().Set("Access-Control-Allow-Headers", headers)
					w.Header().Set("Access-Control-Max-Age", maxAgeStr)
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func buildOriginSet(origins []string) map[string]bool {
	m := make(map[string]bool, len(origins))
	for _, o := range origins {
		m[o] = true
	}
	return m
}

func defaultIfEmpty(v, fallback []string) []string {
	if len(v) == 0 {
		return fallback
	}
	return v
}
