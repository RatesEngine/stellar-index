// Package v1 is the HTTP serving plane for the Rates Engine public
// API v1.
//
// # Source of truth
//
// The OpenAPI specification at openapi/rates-engine.v1.yaml is the
// wire contract. This package implements it. If the two diverge,
// either (a) update the spec + regenerate docs, or (b) fix the
// handler. Never silently ship a handler that disagrees with the
// spec.
//
// # Response envelope
//
// Every 2xx JSON response carries the same envelope (see [Envelope]):
//
//	{
//	  "data":       {...},
//	  "as_of":      "2026-04-22T14:30:15.842Z",
//	  "sources":    ["soroswap", "aquarius"],
//	  "flags":      {...},
//	  "pagination": {"next": "..."}   // optional
//	}
//
// Clients never have to branch on "is this key present?" — the
// envelope fields are always there (barring pagination, which only
// appears on list endpoints).
//
// # Errors
//
// Every 4xx/5xx is RFC 9457 problem+json. See [Problem].
//
// # Middleware stack
//
// Applied in order (outermost first):
//
//	RequestID  → assigns X-Request-ID if absent.
//	Logger     → structured access log per request (slog).
//	Recoverer  → recovers from handler panics → 500 + incident page.
//	RateLimit  → per-API-key / per-IP token bucket (internal/ratelimit).
//	CORS       → allow-list from [config.APIConfig.AllowedOrigins].
//
// # What this package doesn't do
//
//   - No auth logic — [middleware.APIKey] (future) handles that.
//   - No serialisation of canonical types — they handle themselves
//     via their [encoding/json.Marshaler] implementations.
//   - No business logic — that lives in [internal/aggregate] and
//     [internal/storage/timescale].
//
// The handlers are thin: parse input → call a storage or cache
// method → wrap in an Envelope → write JSON.
//
// # References
//
//   - [docs/reference/api-design.md] — design doc.
//   - [openapi/rates-engine.v1.yaml] — wire contract.
//   - [ADR-0007] — Redis cache schema (cachekeys).
package v1
