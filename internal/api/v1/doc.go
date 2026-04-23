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
// Applied in order (outermost first). See [Server.Handler] for the
// authoritative ordering + rationale on each placement:
//
//	RequestID        → assigns X-Request-ID if absent / safe.
//	HTTPMetrics      → records http_requests_total + duration hist.
//	Logger           → structured access log per request (slog);
//	                   populates remote_ip into ctx for downstream.
//	Recoverer        → handler panics → 500 problem+json.
//	SecurityHeaders  → X-Content-Type-Options: nosniff on every resp.
//	CORS (optional)  → allow-list from [config.APIConfig.AllowedOrigins];
//	                   outside RateLimit so OPTIONS preflight is free.
//	RateLimit (opt.) → per-IP token bucket (internal/ratelimit); innermost
//	                   so it sees Logger-populated remote_ip.
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
