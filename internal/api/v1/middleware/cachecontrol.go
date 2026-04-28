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
//   - **Tip / observations** → `private, no-cache, must-revalidate`
//     (tip surface intentionally has no cross-region consistency
//     contract per ADR-0018; caching shifts the contract).
//   - **Closed-bucket historical** (`/v1/history*`, `/v1/ohlc`,
//     `/v1/vwap`, `/v1/twap`, `/v1/markets`, `/v1/pairs`,
//     `/v1/oracle/*`, `/v1/sources`) → `public, max-age=60,
//     s-maxage=300` (1 min client / 5 min CDN). Closed buckets are
//     immutable per ADR-0015, but the trailing-edge boundary
//     advances as time passes — the s-maxage caps how long a CDN
//     entry stays before the boundary moves.
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
// Errors (4xx / 5xx) inherit the route's cache directive. The
// middleware doesn't post-process responses to change directives
// after the fact — that adds latency for no reliability gain.
// CDN configs are expected to refuse to cache 5xx responses
// regardless of header (`origin-error-min-ttl: 0`).
func CacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", policyForPath(r.URL.Path))
		next.ServeHTTP(w, r)
	})
}

// policyForPath classifies a request path into a Cache-Control
// directive. Exposed at package scope so tests can pin the policy
// table without spinning up a full handler.
//
// Order matters — the more-specific prefix MUST win over the
// less-specific. `/v1/price/tip` is private; `/v1/price` is public —
// both share the prefix `/v1/price` so the tip rule must run first.
func policyForPath(path string) string {
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

	// ─── Tip + observations — private surfaces (ADR-0018) ───────
	// Tip has no cross-region consistency contract; caching
	// would shift the contract. Same for observations.
	case path == "/v1/price/tip",
		strings.HasPrefix(path, "/v1/price/tip/"),
		path == "/v1/observations",
		strings.HasPrefix(path, "/v1/observations/"):
		return "private, no-cache, must-revalidate"

	// ─── Current price + asset detail — short cache ─────────────
	// Updates on every bucket close; CDN entry should turn over
	// inside one bucket so consumers see fresh closed-bucket data.
	case path == "/v1/price",
		strings.HasPrefix(path, "/v1/price/batch"),
		path == "/v1/assets",
		strings.HasPrefix(path, "/v1/assets/"):
		return "public, max-age=30, s-maxage=60"

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
		strings.HasPrefix(path, "/v1/oracle/"):
		return "public, max-age=60, s-maxage=300"

	// ─── Default — be conservative ──────────────────────────────
	// Unknown path: don't accidentally let the CDN cache something
	// that turns out to be auth-tied later. Matches /v1/account/*
	// stance.
	default:
		return "private, no-store"
	}
}
