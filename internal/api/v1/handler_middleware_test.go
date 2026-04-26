package v1_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
)

// noopMiddleware passes the request through unchanged. Sufficient
// to exercise the optional-middleware append branches in
// Server.Handler — we don't need real CORS/RateLimit behaviour for
// this coverage.
func noopMiddleware(label string, hits *int) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*hits++
			w.Header().Set("X-Test-MW", label)
			next.ServeHTTP(w, r)
		})
	}
}

// Server.Handler conditionally appends CORS + RateLimit middleware
// when their Options fields are non-nil. Neither branch was
// exercised by existing tests — Server constructions all left
// CORS/RateLimit nil.

func TestServer_Handler_includesOptionalCORS(t *testing.T) {
	corsHits := 0
	srv := v1.New(v1.Options{
		CORS: noopMiddleware("cors", &corsHits),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if corsHits == 0 {
		t.Error("CORS middleware was not invoked — Handler didn't append it")
	}
	if resp.Header.Get("X-Test-MW") != "cors" {
		t.Errorf("expected X-Test-MW=cors, got %q", resp.Header.Get("X-Test-MW"))
	}
}

func TestServer_Handler_includesOptionalRateLimit(t *testing.T) {
	rlHits := 0
	srv := v1.New(v1.Options{
		RateLimit: noopMiddleware("ratelimit", &rlHits),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if rlHits == 0 {
		t.Error("RateLimit middleware was not invoked — Handler didn't append it")
	}
}

func TestServer_Handler_bothMiddlewareOrdering(t *testing.T) {
	// When BOTH CORS and RateLimit are wired, Server.Handler appends
	// CORS BEFORE RateLimit so preflight OPTIONS short-circuits
	// without consuming rate-limit budget. Verify both run.
	corsHits, rlHits := 0, 0
	srv := v1.New(v1.Options{
		CORS:      noopMiddleware("cors", &corsHits),
		RateLimit: noopMiddleware("ratelimit", &rlHits),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if corsHits == 0 || rlHits == 0 {
		t.Errorf("expected both middlewares invoked; got cors=%d ratelimit=%d",
			corsHits, rlHits)
	}
}
