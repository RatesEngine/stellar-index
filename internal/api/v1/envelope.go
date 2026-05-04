package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
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
// Returns true for either (a) the raw ctx error wrapping context.
// Canceled / DeadlineExceeded, or (b) the request's own context
// being done (handles the case where a downstream wrapped the
// error).
func clientAborted(r *http.Request, err error) bool {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return true
	}
	return r.Context().Err() != nil
}
