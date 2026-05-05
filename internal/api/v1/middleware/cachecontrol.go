package middleware

import (
	"net/http"
	"strings"
)

// CacheControl is a middleware that sets the Cache-Control response
// header per the route's caching policy. CDN tier (e.g. CloudFront)
// reads `s-maxage`; client tier reads `max-age`. The two-tier setup
// lets a hot path absorb a 100× burst at the CDN without filling
// the origin budget while still serving fresh-enough data to clients.
//
// Policy (per ADR-0018 surface model):
//
//   - **Health / version / metrics** → `no-store` (operator endpoints
//     change every probe; caching them would mask outages).
//   - **Account endpoints** → `private, no-store` (auth-tied; never
//     caches across users; CDN MUST NOT see them).
//   - **Tip / observations / diagnostics** → `private, no-cache,
//     must-revalidate` (tip surface intentionally has no cross-region
//     consistency contract per ADR-0018; caching shifts the contract.
//     `/v1/diagnostics/*` is operator-facing live data — the
//     showcase polls it every 15 s, so caching defeats the UX).
//   - **Closed-bucket historical + catalogues** (`/v1/history*`,
//     `/v1/ohlc`, `/v1/vwap`, `/v1/twap`, `/v1/markets`, `/v1/pairs`,
//     `/v1/oracle/*`, `/v1/sources`, `/v1/coins`, `/v1/issuers*`,
//     `/v1/changes/*`) → `public, max-age=60, s-maxage=300` (1 min
//     client / 5 min CDN). Closed buckets are immutable per
//     ADR-0015, but the trailing-edge boundary advances as time
//     passes — the s-maxage caps how long a CDN entry stays
//     before the boundary moves.
//   - **Current price + asset detail** → `public, max-age=30,
//     s-maxage=60` (more aggressive refresh; these update on every
//     bucket close).
//
// Handlers MAY override the middleware's directive by setting
// Cache-Control before they call writeJSON / writeProblem (the
// middleware sets the header BEFORE calling the inner handler).
// Override is the exception, not the rule — the middleware's
// directive is the right answer for >99% of requests.
//
// Errors override the route's directive at the writer side. All
// problem+json paths (writeProblem in v1/envelope.go, the rate
// limiter's writeRateLimitProblem, the recoverer's panic body, and
// the envelope404 middleware that rewrites the mux's text/plain
// 404/405) explicitly set `Cache-Control: no-store` before
// WriteHeader. Without that override an error response would inherit
// (e.g.) `public, max-age=60, s-maxage=300` from the catalogue
// surface and a CDN would happily cache the transient failure for
// 5 minutes against the same key as the success response.
//
// Backwards-compat shim: behaves like cdn_enabled=true. Operators
// who run the API behind no CDN should use [CacheControlWithCDN]
// to drop the `s-maxage` half of the directive.
func CacheControl(next http.Handler) http.Handler {
	return CacheControlWithCDN(true)(next)
}

// CacheControlWithCDN returns the cache-control middleware with the
// `s-maxage` (CDN-tier) directive controlled by `cdnEnabled`. When
// false, only `max-age` (client tier) is emitted on cacheable
// routes — appropriate for deployments without a CDN in front.
// `private, no-store` and `private, no-cache, must-revalidate`
// directives are unaffected (they were never CDN-cacheable).
func CacheControlWithCDN(cdnEnabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", policyForPath(r.URL.Path, cdnEnabled))
			next.ServeHTTP(w, r)
		})
	}
}

// policyForPath classifies a request path into a Cache-Control
// directive. Exposed at package scope so tests can pin the policy
// table without spinning up a full handler.
//
// Order matters — the more-specific prefix MUST win over the
// less-specific. `/v1/price/tip` is private; `/v1/price` is public —
// both share the prefix `/v1/price` so the tip rule must run first.
//
// `cdnEnabled` controls whether `s-maxage` (CDN-tier) directives
// are emitted on cacheable routes. When false, only `max-age`
// (client tier) survives — operators without a CDN in front of
// the API set this so a CDN they don't have can't cache anything.
func policyForPath(path string, cdnEnabled bool) string {
	switch {
	// ─── Operator endpoints — never cached ──────────────────────
	case path == "/v1/healthz",
		path == "/v1/readyz",
		path == "/v1/version",
		path == "/metrics":
		return "no-store"

	// ─── Account endpoints — auth-tied, MUST NOT hit CDN ────────
	case strings.HasPrefix(path, "/v1/account/"):
		return "private, no-store"

	// ─── SEP-10 Web Auth — credential exchange MUST NOT hit CDN ─
	// Caching the challenge would let a future request reuse a
	// nonce; caching the token would expose it to anyone the CDN
	// serves. Both unconditionally bypass cache.
	case strings.HasPrefix(path, "/v1/auth/sep10"):
		return "private, no-store"

	// ─── Tip + observations — private surfaces (ADR-0018) ───────
	// Tip has no cross-region consistency contract; caching
	// would shift the contract. Same for observations.
	case path == "/v1/price/tip",
		strings.HasPrefix(path, "/v1/price/tip/"),
		path == "/v1/observations",
		strings.HasPrefix(path, "/v1/observations/"):
		return "private, no-cache, must-revalidate"

	// ─── Diagnostics — operator-facing live data ────────────────
	// /v1/diagnostics/cursors is polled every 15s by the showcase
	// /diagnostics page; caching would defeat the "watch the
	// indexer tick" UX. Same shape as tip/observations: tight
	// freshness, never CDN-cached.
	case strings.HasPrefix(path, "/v1/diagnostics/"):
		return "private, no-cache, must-revalidate"

	// ─── Status — customer-facing health rollup ─────────────────
	// /v1/status is what the showcase /status page polls every 10 s
	// and what monitoring dashboards (and the smoke timer) poll on a
	// longer interval. A 10 s cache absorbs the polling fan-out
	// without delaying alert-state propagation enough to matter —
	// the underlying signals (Prometheus heartbeats, incident counts)
	// already have 15 s scrape granularity.
	case path == "/v1/status":
		if cdnEnabled {
			return "public, max-age=10, s-maxage=15"
		}
		return "public, max-age=10"

	// ─── Current price + asset detail — short cache ─────────────
	// Updates on every bucket close; CDN entry should turn over
	// inside one bucket so consumers see fresh closed-bucket data.
	case path == "/v1/price",
		strings.HasPrefix(path, "/v1/price/batch"),
		path == "/v1/assets",
		strings.HasPrefix(path, "/v1/assets/"):
		if cdnEnabled {
			return "public, max-age=30, s-maxage=60"
		}
		return "public, max-age=30"

	// ─── Historical / closed-bucket / catalogue — longer cache ──
	// Closed buckets are immutable per ADR-0015 but the
	// trailing-edge boundary advances; s-maxage=300 caps how long
	// a CDN entry can lag the boundary.
	case strings.HasPrefix(path, "/v1/history"),
		path == "/v1/ohlc",
		path == "/v1/vwap",
		path == "/v1/twap",
		path == "/v1/markets",
		path == "/v1/pairs",
		path == "/v1/sources",
		strings.HasPrefix(path, "/v1/oracle/"),
		// Registry catalogues — coin / issuer directories. Same
		// shape as /v1/markets: large enumerable lists that change
		// gradually as new assets/issuers are observed.
		path == "/v1/coins",
		path == "/v1/issuers",
		strings.HasPrefix(path, "/v1/issuers/"),
		// Multi-window delta strip. Refreshed every 5 min by the
		// change-summary worker; 60s edge cache stays well inside
		// that boundary, and 5 min s-maxage matches.
		strings.HasPrefix(path, "/v1/changes/"):
		if cdnEnabled {
			return "public, max-age=60, s-maxage=300"
		}
		return "public, max-age=60"

	// ─── Default — be conservative ──────────────────────────────
	// Unknown path: don't accidentally let the CDN cache something
	// that turns out to be auth-tied later. Matches /v1/account/*
	// stance.
	default:
		return "private, no-store"
	}
}
