package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
)

// Envelope is the shape of every 2xx JSON response. See
// docs/reference/api-design.md §4.
type Envelope struct {
	Data       any         `json:"data"`
	AsOf       time.Time   `json:"as_of"`
	Sources    []string    `json:"sources,omitempty"`
	Flags      Flags       `json:"flags"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Flags are the advisory quality markers per HA plan §9.
//
// Field semantics:
//
//   - Stale: response is below this surface's documented baseline
//     contract — e.g. on /v1/price the closed-bucket VWAP wasn't
//     available so we degraded to last-trade. NOT used on
//     /v1/price/tip's last-good-price fallback (that's in-contract;
//     see ADR-0018 §"flags.stale semantic").
//   - ReducedRedundancy: cross-region redundancy is degraded —
//     R2/R3 set this when R1's last successful completeness run is
//     stale per ADR-0017.
//   - Triangulated: rate was computed via chain-pricing through a
//     pivot (typically USD), not from a directly-traded pair.
//   - DivergenceWarning: anomaly-detection or cross-reference
//     observed a meaningful divergence; consumers should treat the
//     value with caution. Fires per ADR-0019 anomaly.ActionWarn AND
//     per future internal/divergence/ cross-reference checks.
//   - Frozen: anomaly detection refused to publish the new bucket;
//     this response carries the previous bucket's last-known-good
//     value (ADR-0019 freeze policy). Only fires on /v1/price; the
//     tip + observations surfaces ignore freeze.
//   - SingleSource: the bucket had only one contributing source.
//     Informational; combined with Frozen this is the manipulation
//     signature.
type Flags struct {
	Stale             bool `json:"stale"`
	ReducedRedundancy bool `json:"reduced_redundancy"`
	Triangulated      bool `json:"triangulated"`
	DivergenceWarning bool `json:"divergence_warning"`
	Frozen            bool `json:"frozen,omitempty"`
	SingleSource      bool `json:"single_source,omitempty"`
	// UnverifiedTickerCollision fires on `/v1/assets/{id}` when the
	// requested asset's code matches a verified currency's Stellar
	// ticker but its issuer doesn't match the verified entry — i.e.
	// someone issued their own "USDC" on Stellar. The matching
	// `unverified_warning` payload on the AssetDetail body carries
	// the pointer to the verified asset. See R-018 /
	// docs/architecture/multi-network-assets-migration.md.
	UnverifiedTickerCollision bool `json:"unverified_ticker_collision,omitempty"`
}

// Pagination is present on list-returning endpoints only.
type Pagination struct {
	Next string `json:"next,omitempty"`
}

// Problem is the RFC 9457 error payload. Custom fields are
// snake_case; `Instance` is typically the request URL.
//
// RequestID is an extension field per RFC 9457 §3.2 (unknown
// members allowed). It echoes the X-Request-ID header so clients
// can correlate a failure they saw with server logs without
// parsing headers separately — and so bug reports that include
// the body are sufficient for support to find the trace.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// writeJSON writes the Envelope + 200. The convention everywhere in
// v1 handlers.
func writeJSON(w http.ResponseWriter, data any, flags Flags, sources ...string) {
	writeEnvelope(w, Envelope{
		Data:    data,
		AsOf:    time.Now().UTC(),
		Sources: sources,
		Flags:   flags,
	})
}

// writeEnvelope writes a pre-constructed Envelope. Used by handlers
// that need to set Pagination or other fields writeJSON doesn't
// accept as params.
func writeEnvelope(w http.ResponseWriter, env Envelope) {
	writeEnvelopeStatus(w, http.StatusOK, env)
}

// writeEnvelopeStatus writes a pre-constructed Envelope with an
// explicit 2xx status code. Used by handlers whose public contract
// is not plain 200 OK.
func writeEnvelopeStatus(w http.ResponseWriter, status int, env Envelope) {
	if env.AsOf.IsZero() {
		env.AsOf = time.Now().UTC()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(env)
}

// writeProblem writes an RFC 9457 error response. Handlers call
// this instead of http.Error to keep the wire contract consistent.
//
// typeURL is the stable error-type URL (document the taxonomy at
// https://api.ratesengine.net/errors/<name>); title is a short
// human headline; status is the HTTP code; detail is the freeform
// per-request message (optional).
func writeProblem(w http.ResponseWriter, r *http.Request, typeURL, title string, status int, detail string) {
	p := Problem{
		Type:      typeURL,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  r.URL.RequestURI(),
		RequestID: middleware.RequestIDFrom(r),
	}
	w.Header().Set("Content-Type", "application/problem+json")
	// Errors override the cache-control middleware's per-route
	// directive: never cache an error. Otherwise a CDN serving
	// /v1/coins (which the middleware tags `public, max-age=60,
	// s-maxage=300`) would cache a transient 400/404/500 for the
	// next 5 minutes and replay it to other anonymous clients on
	// the same cache key.
	w.Header().Set("Cache-Control", "no-store")
	// RFC 7235 §3.1: every 401 response MUST include a
	// WWW-Authenticate header naming at least one challenge the
	// client can use. Pre-fix our 401s emitted the problem+json
	// envelope but no WWW-Authenticate, leaving programmatic
	// clients without a way to discover the accepted scheme. Our
	// authenticated endpoints all accept Bearer (API key + SEP-10
	// token); the magic-link cookie path is parallel and doesn't
	// have a standard challenge token, so we advertise Bearer only.
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="ratesengine.net"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// clientAborted reports whether a reader-returned error came from
// the client cancelling its request. When true, handlers SHOULD
// return without writing a response — the client has already gone,
// and the obs.HTTPMetrics middleware will then label the request
// 499 (NGINX-style "client closed request") rather than the
// misleading 500 a writeProblem would produce.
//
// Decision rule: the request's own context being done is the only
// signal that means "client gone." A reader returning
// context.DeadlineExceeded while r.Context() is still alive is a
// SERVER-side deadline (one of the cold-path context.WithTimeout
// guards added in #1082, #1099-#1105) — the client is still
// waiting and deserves a 503 problem+json, not a silent abort.
//
// Handlers should structure error handling as:
//
//	if err != nil {
//	    if clientAborted(r, err) { return }
//	    if errors.Is(err, context.DeadlineExceeded) {
//	        // 503 timeout response
//	    }
//	    // 500 internal
//	}
//
// `err` is unused for the abort decision but kept in the signature
// because it's the natural call site (handlers always have it) and
// keeps the call sites stable.
func clientAborted(r *http.Request, _ error) bool {
	return r.Context().Err() != nil
}

// handlerTimedOut reports whether a handler-scoped context (created
// via context.WithTimeout to cap an individual storage call) hit
// its deadline. Use this on the per-call context — NOT
// r.Context() — so genuine deadline-exceeded paths are recognised
// even when the upstream driver returns its own
// statement-cancellation error rather than wrapping
// context.DeadlineExceeded.
//
// Background: lib/pq propagates a Go context cancellation to
// PostgreSQL via the v3 cancel-request protocol, then returns the
// resulting `pq: canceling statement due to user request` (SQLSTATE
// 57014) — which does NOT unwrap to [context.DeadlineExceeded].
// `errors.Is(err, context.DeadlineExceeded)` therefore misses every
// case where a per-call deadline fired and the driver beat the
// caller to noticing. The cleanest signal is the per-call context
// itself: if its Err() is DeadlineExceeded, the request DID time
// out regardless of how the driver phrased the resulting error.
//
// The OR with errors.Is keeps drivers that DO wrap correctly
// (Timescale's hypercore extension does in some paths) on the same
// branch.
//
// R-021 in `docs/review-2026-05-10.md` — pre-fix, /v1/markets cold
// cache returned `500 Internal error` instead of `503 markets-timeout`.
func handlerTimedOut(callCtx context.Context, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return callCtx.Err() == context.DeadlineExceeded
}

// transientStorageErr reports whether a storage-layer error looks
// like a transient infrastructure hiccup rather than a real server
// bug. Handlers SHOULD map true to a 503 problem+json (retryable)
// rather than the misleading 500 a writeProblem default would
// produce; the sla-probe's availability_pct counts 5xx as failure,
// so a single transient driver-level error costs an availability
// point even when the underlying request was processable on a
// retry.
//
// Examples this catches:
//
//   - **SQLSTATE 57014 NOT carried by a context cancellation.**
//     Postgres can issue `canceling statement due to user request`
//     from server-side statement_timeout, lock_timeout, or
//     idle_in_transaction_session_timeout — none of which trip
//     [clientAborted] (the http request context is alive) or
//     [handlerTimedOut] (the per-call context hasn't deadlined).
//     The result reaches the handler as a bare 57014; without this
//     helper it returns 500.
//   - **lib/pq driver-bad-conn errors.** `driver: bad connection`
//     surfaces when a Postgres backend was killed (admin restart,
//     OOM killer, idle-connection reaper) between checkout and
//     query execution. The connection-pool retry would normally
//     paper over it; surfaces only when retries are exhausted.
//   - **EOF / broken pipe.** Network-level transient between the
//     api binary and postgres or redis. Re-running the same
//     request would typically succeed.
//
// The classifier is INTENTIONALLY string-based for the SQLSTATE
// match — lib/pq's typed `*pq.Error.Code` would require importing
// the driver into the handler layer (already a dep, but a wider
// surface than strict). The substring `57014` is stable across
// lib/pq versions (it's wire-format from postgres itself); the
// 'canceling statement' fragment is the human-readable companion
// the driver always includes.
//
// Caller pattern (mirrors clientAborted / handlerTimedOut order):
//
//	if err != nil {
//	    if clientAborted(r, err) { return }
//	    if handlerTimedOut(callCtx, err) { /* 503 timeout */ }
//	    if transientStorageErr(err) { /* 503 retry-later */ }
//	    /* 500 internal */
//	}
//
// Refs: #34 residual ("/v1/issuers returns HTTP 500 (fast ~50ms)
// on the sla-probe's request shape — real bug, low severity").
func transientStorageErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// SQLSTATE 57014 from postgres-side cancellations (not the
	// client-side context cancellation flavour, which clientAborted
	// already handles).
	if strings.Contains(s, "57014") || strings.Contains(s, "canceling statement") {
		return true
	}
	// lib/pq + the standard database/sql driver-bad-connection
	// surface. Pool retry exhausted by this point.
	if strings.Contains(s, "driver: bad connection") || strings.Contains(s, "bad connection") {
		return true
	}
	// Network-level transients between the api and postgres / redis.
	if strings.Contains(s, "broken pipe") || strings.Contains(s, "connection reset") ||
		strings.Contains(s, "unexpected EOF") || strings.Contains(s, "EOF") {
		return true
	}
	return false
}
