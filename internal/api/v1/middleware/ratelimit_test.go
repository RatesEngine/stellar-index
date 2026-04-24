package middleware_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/api/v1/middleware"
	"github.com/RatesEngine/rates-engine/internal/ratelimit"
)

func newRLRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

// okHandler always returns 200 OK so the middleware's effect is
// easy to observe.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// fixedKeyFn returns a constant key — easier than threading a real
// remote-IP through the test server.
func fixedKeyFn(k string) func(*http.Request) string {
	return func(*http.Request) string { return k }
}

func TestRateLimit_AllowsUnderLimit(t *testing.T) {
	rdb, _ := newRLRedis(t)
	b := ratelimit.New(rdb, 3, time.Minute)

	h := middleware.RateLimit(b, fixedKeyFn("k1"), nil, nil)(okHandler())

	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("attempt %d: status = %d", i+1, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Limit"); got != "3" {
			t.Errorf("X-RateLimit-Limit = %q, want 3", got)
		}
		wantRemaining := strconv.Itoa(3 - (i + 1))
		if got := w.Header().Get("X-RateLimit-Remaining"); got != wantRemaining {
			t.Errorf("attempt %d: X-RateLimit-Remaining = %q, want %q", i+1, got, wantRemaining)
		}
	}
}

func TestRateLimit_Rejects429AfterLimit(t *testing.T) {
	rdb, _ := newRLRedis(t)
	b := ratelimit.New(rdb, 2, time.Minute)

	h := middleware.RateLimit(b, fixedKeyFn("k2"), nil, nil)(okHandler())

	// Exhaust the budget.
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
	}

	// Over-limit request.
	r := httptest.NewRequest(http.MethodGet, "/some-path", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Error("Retry-After header missing")
	}
	var p struct {
		Type, Title, Instance string
		Status                int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem body: %v", err)
	}
	if p.Status != 429 {
		t.Errorf("problem.status = %d, want 429", p.Status)
	}
	if p.Instance != "/some-path" {
		t.Errorf("problem.instance = %q, want /some-path", p.Instance)
	}
}

func TestRateLimit_EmptyKeyBypasses(t *testing.T) {
	rdb, _ := newRLRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)

	called := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.RateLimit(b, fixedKeyFn(""), nil, nil)(inner)

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("empty-key request %d rejected: %d", i+1, w.Code)
		}
	}
	if called != 5 {
		t.Errorf("inner called %d times, want 5", called)
	}
}

func TestRateLimit_SkipsWhenSkipReturnsTrue(t *testing.T) {
	rdb, _ := newRLRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)

	h := middleware.RateLimit(
		b, fixedKeyFn("k3"),
		func(r *http.Request) bool { return r.URL.Path == "/v1/healthz" },
		nil,
	)(okHandler())

	// Budget is 1. Call /v1/healthz 10× — none should count.
	for i := 0; i < 10; i++ {
		r := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("healthz request %d rejected: %d", i+1, w.Code)
		}
	}
	// Now a regular request should still get its one allowance.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("first non-skipped request rejected: %d", w.Code)
	}
}

func TestRateLimit_FailsOpenOnRedisError(t *testing.T) {
	rdb, mr := newRLRedis(t)
	b := ratelimit.New(rdb, 1, time.Minute)

	h := middleware.RateLimit(b, fixedKeyFn("k4"), nil, nil)(okHandler())

	// Blow up the backing miniredis — future Take() calls error.
	mr.Close()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("fail-open violated: status = %d", w.Code)
	}
	// Rate-limit headers should be absent (we didn't compute them).
	if got := w.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("X-RateLimit-Limit should be absent on failure, got %q", got)
	}
}

func TestSkipHealthAndMetrics(t *testing.T) {
	cases := map[string]bool{
		"/v1/healthz":      true,
		"/v1/readyz":       true,
		"/v1/version":      true,
		"/metrics":         true,
		"/v1/assets":       false,
		"/v1/price":        false,
		"/v1/metrics-fake": false,
	}
	for path, want := range cases {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if got := middleware.SkipHealthAndMetrics(r); got != want {
			t.Errorf("SkipHealthAndMetrics(%q) = %v, want %v", path, got, want)
		}
	}
}
