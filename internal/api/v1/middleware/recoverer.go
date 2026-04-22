package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recoverer turns a panicking handler into a 500 response + a
// structured log with the stack trace. Without this, one bad
// handler kills the whole HTTP server goroutine and crashes the
// binary — net/http does not catch panics from user handlers
// by default.
//
// Body is RFC 9457 problem+json with a generic "internal error"
// message. The actual panic value + stack go to the log, never to
// the client (attack-surface containment).
func Recoverer(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler is the stdlib signal for
				// "abort but don't log as a panic" — honour it.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				logger.Error("handler panic",
					"request_id", RequestIDFrom(r),
					"method", r.Method,
					"path", r.URL.Path,
					"remote_ip", RemoteIPFrom(r),
					"panic", rec,
					"stack", string(debug.Stack()),
				)

				writeProblem(w, r, rec)
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// writeProblem emits an RFC 9457 problem+json body for a recovered
// panic. Kept local to avoid pulling the v1 package as a dependency
// (which would create a cycle — v1 imports middleware).
func writeProblem(w http.ResponseWriter, r *http.Request, _ any) {
	// Only write the header if none has been sent yet.
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	body := map[string]any{
		"type":     "https://api.ratesengine.net/errors/internal",
		"title":    "Internal error",
		"status":   http.StatusInternalServerError,
		"instance": r.URL.RequestURI(),
	}
	if id := RequestIDFrom(r); id != "" {
		body["request_id"] = id
	}
	_ = json.NewEncoder(w).Encode(body)
}
