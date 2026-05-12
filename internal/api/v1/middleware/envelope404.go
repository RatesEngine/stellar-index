package middleware

import (
	"encoding/json"
	"net/http"
)

// Envelope404 converts Go's default text/plain "404 page not found"
// and "Method Not Allowed" responses (emitted by net/http's ServeMux
// when no pattern matches a path, or when a path's only registered
// pattern uses a different method) into RFC 9457 problem+json so
// the wire shape stays consistent with the rest of the v1 error
// surface.
//
// All v1 handlers write JSON via writeJSON / writeProblem and set
// Content-Type explicitly. The only way a response leaves with
// Content-Type "text/plain; charset=utf-8" is the mux's default
// error path. We detect that combination at WriteHeader time,
// override the Content-Type to application/problem+json, write
// our envelope, and suppress the trailing default-handler body.
//
// This middleware only buffers the WriteHeader decision (a small,
// allocation-free wrapper) — Write passes straight through for the
// non-overridden case, so SSE handlers and large responses are
// untouched.
func Envelope404(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &envelope404Recorder{ResponseWriter: w, r: r}
		next.ServeHTTP(rec, r)
	})
}

// envelope404Recorder rewrites text/plain 404/405 responses into
// problem+json. Other responses pass through verbatim.
type envelope404Recorder struct {
	http.ResponseWriter
	r          *http.Request
	overridden bool
}

// WriteHeader inspects (status, Content-Type) and overrides when
// the combination matches Go's default 404 / 405 handlers.
func (e *envelope404Recorder) WriteHeader(status int) {
	if !e.overridden && shouldOverride(status, e.Header().Get("Content-Type")) {
		e.overridden = true
		e.Header().Set("Content-Type", "application/problem+json")
		// Length depends on the body we're about to write — let Go
		// recompute it. The default handler may have set a stale value.
		e.Header().Del("Content-Length")
		// Errors override the cache-control middleware's per-route
		// directive: never cache an unknown-path 404 or method-mismatch
		// 405. A CDN serving /v1/assets (tagged `public, max-age=60,
		// s-maxage=300` by the middleware) would otherwise cache a
		// transient 405 from a misconfigured client and replay it to
		// other anonymous clients on the same key.
		e.Header().Set("Cache-Control", "no-store")
		e.ResponseWriter.WriteHeader(status)
		e.writeProblem(status)
		return
	}
	e.ResponseWriter.WriteHeader(status)
}

// Write swallows the default-handler body when we've already written
// our problem+json envelope; otherwise it passes through.
func (e *envelope404Recorder) Write(p []byte) (int, error) {
	if e.overridden {
		return len(p), nil
	}
	return e.ResponseWriter.Write(p)
}

// Flush preserves http.Flusher for SSE handlers — without this,
// wrapping breaks chunked streaming.
func (e *envelope404Recorder) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying ResponseWriter so
// http.NewResponseController can reach SetWriteDeadline / Hijack
// on it. F-1228 (codex audit-2026-05-12). See statusRecorder.Unwrap
// for the full rationale.
func (e *envelope404Recorder) Unwrap() http.ResponseWriter {
	return e.ResponseWriter
}

func shouldOverride(status int, contentType string) bool {
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return false
	}
	// Go's defaults set "text/plain; charset=utf-8" — match that
	// exactly, not a prefix, so a future text/plain handler we WANT
	// to ship is unaffected.
	return contentType == "text/plain; charset=utf-8"
}

// writeProblem writes the RFC 9457 envelope. Mirrors the v1
// package's writeProblem shape but stays in middleware so we don't
// pull a circular import.
func (e *envelope404Recorder) writeProblem(status int) {
	typeURL := "https://api.ratesengine.net/errors/not-found"
	title := "Not found"
	detail := "No handler is registered for this path. See https://docs.ratesengine.net for the API surface."
	if status == http.StatusMethodNotAllowed {
		typeURL = "https://api.ratesengine.net/errors/method-not-allowed"
		title = "Method not allowed"
		detail = "The path exists but the request method is not supported. See https://docs.ratesengine.net for the API surface."
	}
	_ = json.NewEncoder(e.ResponseWriter).Encode(map[string]any{
		"type":       typeURL,
		"title":      title,
		"status":     status,
		"detail":     detail,
		"instance":   e.r.URL.RequestURI(),
		"request_id": RequestIDFrom(e.r),
	})
}
