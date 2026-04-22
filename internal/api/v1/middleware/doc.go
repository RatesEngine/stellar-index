// Package middleware has the HTTP middleware the v1 API Server wraps
// its mux in. Order (outermost first, per docs/reference/api-design.md):
//
//	RequestID → Logger → Recoverer → RateLimit → CORS
//
// Each middleware is a tiny file. They're composable via [Chain]
// which wraps them innermost-last so the request-path order matches
// the declaration order.
//
// # Request context keys
//
// Middleware inject values into the request context via the keys in
// [context_keys.go]. Handlers read them via [FromRequest] accessors;
// never reach into the context bag directly.
//
// # Deliberately small
//
// This package does NOT wrap a router, does NOT implement auth, and
// does NOT do CORS (the chi/gorilla/negroni-style grab-bag is
// explicitly rejected). Narrow, focused middleware that you can
// read top-to-bottom in a minute.
package middleware
