// Package middleware has the HTTP middleware the v1 API Server wraps
// its mux in. Order (outermost first, per
// `internal/api/v1/server.go`'s `Server.Handler` build):
//
//	RequestID → HTTPMetrics → Logger → Recoverer → SecurityHeaders → CacheControl → CORS → Auth → RateLimit
//
// Each middleware is a tiny file. They're composable via [Chain]
// which wraps them innermost-last so the request-path order matches
// the declaration order.
//
// # Request context keys
//
// Middleware inject values into the request context via the keys in
// `context.go`. Handlers read them via the `FromRequest`-style
// accessors (e.g. [auth.SubjectFrom]); never reach into the
// context bag directly.
//
// # Auth
//
// [Auth] is the unified authentication middleware. It selects an
// `auth.AuthMode` (`anonymous` / `apikey` / `sep10`) per
// `[api].auth_mode`, identifies the subject via the configured
// validator (an `auth.APIKeyValidator` or `auth.SEP10Validator`
// from the parent package), stamps the resulting `auth.Subject` on
// the request context, and lets the rate-limit middleware key on
// it for per-tier budgets. SEP-10 challenge / verify endpoints are
// served by the v1 server directly, not by this middleware.
//
// # Deliberately small
//
// This package does NOT wrap a router. CORS support is intentionally
// minimal — exact-match origin allow-list plus wildcard, no dynamic
// origin reflection beyond that. Callers wanting per-route policies
// or pattern matching should reach for rs/cors instead.
package middleware
