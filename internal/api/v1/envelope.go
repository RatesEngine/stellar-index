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
type Flags struct {
	Stale             bool `json:"stale"`
	ReducedRedundancy bool `json:"reduced_redundancy"`
	Triangulated      bool `json:"triangulated"`
	DivergenceWarning bool `json:"divergence_warning"`
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
	if env.AsOf.IsZero() {
		env.AsOf = time.Now().UTC()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
