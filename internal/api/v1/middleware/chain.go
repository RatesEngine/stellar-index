package middleware

import "net/http"

// Middleware wraps an http.Handler with some pre/post behaviour.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware to h. The first entry in mws is the
// OUTERMOST wrapper (runs first on the request path), so the order
// matches the request flow:
//
//	Chain(mux, RequestID, Logger, Recoverer)
//	            ^^^^^^^^^ outermost      ^^^^^^^^^ innermost
//
// A request hits RequestID first, then Logger, then Recoverer, then
// the mux. Reverse for responses.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
