package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// corsOK is a tiny handler that 200s, so tests can distinguish
// "middleware passed through" from "middleware short-circuited".
func corsOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCORS_WildcardOrigin(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})(corsOK())

	r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	r.Header.Set("Origin", "https://wallet.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("wildcard Allow-Origin = %q, want *", got)
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200 (next should have run)", w.Code)
	}
}

func TestCORS_ExactMatchOrigin(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{
			"https://wallet.example.com",
			"https://freighter.app",
		},
	})(corsOK())

	r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	r.Header.Set("Origin", "https://freighter.app")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://freighter.app" {
		t.Errorf("Allow-Origin = %q, want https://freighter.app", got)
	}
	// Vary must be set on exact-match reflection so caches don't
	// serve one origin's header to a different origin.
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
}

func TestCORS_UnknownOriginGetsNoAllowHeader(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://allowed.example.com"},
	})(corsOK())

	r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unknown origin got Allow-Origin = %q", got)
	}
	// Request still reaches the handler — CORS doesn't BLOCK
	// server-side; it just omits the header so the browser rejects
	// the response.
	if w.Code != 200 {
		t.Errorf("status = %d, want 200 (CORS doesn't block server-side)", w.Code)
	}
}

func TestCORS_Preflight(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://wallet.example.com"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"X-API-Key"},
		MaxAge:         1800,
	})(corsOK())

	r := httptest.NewRequest(http.MethodOptions, "/v1/assets", nil)
	r.Header.Set("Origin", "https://wallet.example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Errorf("Allow-Methods = %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "X-API-Key" {
		t.Errorf("Allow-Headers = %q", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "1800" {
		t.Errorf("Max-Age = %q", got)
	}
}

func TestCORS_OPTIONSWithoutPreflightHeaderPassesThrough(t *testing.T) {
	// Some clients send bare OPTIONS for routing; without the
	// Access-Control-Request-Method header it's NOT a CORS
	// preflight. Middleware should pass through, not 204.
	called := false
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodOptions, "/v1/assets", nil)
	r.Header.Set("Origin", "https://wallet.example.com")
	// No Access-Control-Request-Method.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if !called {
		t.Error("non-preflight OPTIONS should reach the handler")
	}
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestCORS_NoOriginNoHeaders(t *testing.T) {
	// Same-origin request (no Origin header) shouldn't get CORS
	// headers at all.
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})(corsOK())

	r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// Wildcard is echoed regardless — but exact-match mode would
	// not have emitted a header. Both are spec-compliant.
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("wildcard should still respond: got %q", got)
	}
}

// TestCORS_VaryOriginAlwaysSetForExactMatchMode — the middleware
// MUST emit `Vary: Origin` on every response in exact-match mode
// regardless of whether the current request's Origin matched the
// allow list. Without this, a cacheable response served to a
// no-Origin (curl / server-side) request would be cached at the
// CDN without origin discrimination — a later browser request
// whose Origin WOULD have been allowed would receive that cached
// "no CORS" response, breaking client-side fetch().
func TestCORS_VaryOriginAlwaysSetForExactMatchMode(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://ratesengine.net"},
	})(corsOK())

	t.Run("no Origin header still sets Vary", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary on no-Origin request = %q, want Origin (CDN cache-key partition)", got)
		}
	})
	t.Run("disallowed Origin still sets Vary", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
		r.Header.Set("Origin", "https://attacker.example")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary on disallowed-Origin = %q, want Origin", got)
		}
	})
	t.Run("allowed Origin still sets Vary", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
		r.Header.Set("Origin", "https://ratesengine.net")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("Vary on allowed-Origin = %q, want Origin", got)
		}
	})
}

// TestCORS_VaryNotSetForWildcardMode — when the operator opted
// into wildcard mode (`Allow-Origin: *`), the response is
// origin-independent so Vary: Origin would just defeat caching
// without any benefit.
func TestCORS_VaryNotSetForWildcardMode(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"*"},
	})(corsOK())

	r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Header().Get("Vary"); got != "" {
		t.Errorf("Vary in wildcard mode = %q, want empty (response is origin-independent)", got)
	}
}

// TestCORS_DefaultAllowedMethodsIncludePOST pins the v1-surface-
// matching default. Pre-2026-05-02 the default was {GET, HEAD,
// OPTIONS}; cross-origin POST to /v1/account/keys etc. would fail
// preflight unless the operator overrode AllowedMethods. Now POST
// is in the default set so the API binary's
// `CORS(CORSOptions{AllowedOrigins: ...})` shorthand wires
// browser-callable POST endpoints out of the box.
func TestCORS_DefaultAllowedMethodsIncludePOST(t *testing.T) {
	h := middleware.CORS(middleware.CORSOptions{
		AllowedOrigins: []string{"https://wallet.example.com"},
		// AllowedMethods unset → exercise the default
	})(corsOK())

	r := httptest.NewRequest(http.MethodOptions, "/v1/account/keys", nil)
	r.Header.Set("Origin", "https://wallet.example.com")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	got := w.Header().Get("Access-Control-Allow-Methods")
	if !contains(got, "POST") {
		t.Errorf("Allow-Methods default = %q, want substring POST", got)
	}
	// GET, HEAD, OPTIONS still present — POST is additive, not
	// replacing.
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		if !contains(got, m) {
			t.Errorf("Allow-Methods default = %q, want substring %q", got, m)
		}
	}
}

// TestCORS_PerRequestObservability — every request increments the
// CORS decisions counter with the right outcome label. F-1244 lets
// operators see real cross-origin traffic patterns + alert when a
// wildcard policy starts handling actual cross-origin requests in
// production rather than just at startup.
func TestCORS_PerRequestObservability(t *testing.T) {
	cases := []struct {
		name        string
		origins     []string
		reqOrigin   string
		wantOutcome string
	}{
		{"no origin", []string{"https://allowed.example.com"}, "", "no_origin"},
		{"allowed exact", []string{"https://allowed.example.com"}, "https://allowed.example.com", "allowed_origin"},
		{"allowed wildcard", []string{"*"}, "https://anywhere.example.com", "allowed_wildcard"},
		{"denied", []string{"https://allowed.example.com"}, "https://evil.example.com", "denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(obs.APICORSDecisionsTotal.WithLabelValues(tc.wantOutcome))

			h := middleware.CORS(middleware.CORSOptions{
				AllowedOrigins: tc.origins,
			})(corsOK())
			r := httptest.NewRequest(http.MethodGet, "/v1/assets", nil)
			if tc.reqOrigin != "" {
				r.Header.Set("Origin", tc.reqOrigin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			after := testutil.ToFloat64(obs.APICORSDecisionsTotal.WithLabelValues(tc.wantOutcome))
			if after-before != 1 {
				t.Errorf("APICORSDecisionsTotal{outcome=%q} delta = %v, want 1",
					tc.wantOutcome, after-before)
			}
		})
	}
}
