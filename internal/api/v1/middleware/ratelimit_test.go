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
	"github.com/RatesEngine/rates-engine/internal/auth"
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
	// 429s must override any per-route Cache-Control directive
	// (e.g. /v1/assets is `public, max-age=60, s-maxage=300`); a CDN
	// would otherwise replay the denial to other clients on the same
	// cache key.
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on a 429", cc)
	}
}

// X-RateLimit-Reset is required for clients to back off proactively
// (GitHub/Twitter convention: Unix-epoch seconds when the bucket
// resets). Pin the header presence + future-tense semantics so a
// refactor can't silently regress the value to "now" or omit it.
func TestRateLimit_EmitsXRateLimitResetHeader(t *testing.T) {
	rdb, _ := newRLRedis(t)
	b := ratelimit.New(rdb, 5, time.Minute)

	h := middleware.RateLimit(b, fixedKeyFn("k-reset"), nil, nil)(okHandler())

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	now := time.Now().Unix()
	h.ServeHTTP(w, r)

	got := w.Header().Get("X-RateLimit-Reset")
	if got == "" {
		t.Fatal("X-RateLimit-Reset header missing")
	}
	resetAt, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("X-RateLimit-Reset = %q: not an integer", got)
	}
	// For a 60s window, reset must be in (now, now + 60] seconds.
	// Equality on the upper bound covers the edge case where the
	// request lands exactly at a window boundary.
	if resetAt <= now {
		t.Errorf("X-RateLimit-Reset = %d, want > %d (must be future)", resetAt, now)
	}
	if resetAt > now+60 {
		t.Errorf("X-RateLimit-Reset = %d, want ≤ %d (≤ window length)", resetAt, now+60)
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

func TestRateLimitBySubject_AuthenticatedUsesAuthBucket(t *testing.T) {
	rdb, _ := newRLRedis(t)
	anonBucket := ratelimit.New(rdb, 1, time.Minute)
	authBucket := ratelimit.New(rdb, 2, time.Minute)

	h := middleware.RateLimitBySubject(anonBucket, authBucket, nil, nil)(okHandler())
	reqWithSubject := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		subject := auth.Subject{
			Identifier: "GABC123",
			Tier:       auth.TierSEP10,
		}
		return r.WithContext(auth.WithSubject(r.Context(), subject))
	}

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqWithSubject())
		if w.Code != http.StatusOK {
			t.Fatalf("attempt %d: status = %d, want 200", i+1, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Limit"); got != "2" {
			t.Fatalf("attempt %d: X-RateLimit-Limit = %q, want 2", i+1, got)
		}
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSubject())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, want 429", w.Code)
	}
}

func TestRateLimitBySubject_PerKeyOverrideRaisesLimit(t *testing.T) {
	// Default authBucket caps at 2/min; the subject's APIKey record
	// carries RateLimitPerMin=5 (paid-tier custom plan). The
	// middleware must honour the override — both in the wire-side
	// X-RateLimit-Limit header and the actual allow/deny decision.
	rdb, _ := newRLRedis(t)
	anonBucket := ratelimit.New(rdb, 1, time.Minute)
	authBucket := ratelimit.New(rdb, 2, time.Minute)

	h := middleware.RateLimitBySubject(anonBucket, authBucket, nil, nil)(okHandler())
	reqWithSubject := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		subject := auth.Subject{
			Identifier:      "owner-paid",
			Tier:            auth.TierAPIKey,
			KeyID:           "kid_paid",
			RateLimitPerMin: 5,
		}
		return r.WithContext(auth.WithSubject(r.Context(), subject))
	}

	for i := 1; i <= 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqWithSubject())
		if w.Code != http.StatusOK {
			t.Fatalf("hit %d: status = %d, want 200 (override should permit 5/min)", i, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Limit"); got != "5" {
			t.Errorf("hit %d: X-RateLimit-Limit = %q, want 5 (override surfaced on wire)", i, got)
		}
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSubject())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th hit status = %d, want 429 (override should still cap at 5)", w.Code)
	}
}

func TestRateLimitBySubject_AnonymousUsesAnonBucket(t *testing.T) {
	rdb, _ := newRLRedis(t)
	anonBucket := ratelimit.New(rdb, 1, time.Minute)
	authBucket := ratelimit.New(rdb, 3, time.Minute)

	h := middleware.RateLimitBySubject(anonBucket, authBucket, nil, nil)(okHandler())
	reqWithSubject := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		subject := auth.Anonymous("anon-hash-1")
		return r.WithContext(auth.WithSubject(r.Context(), subject))
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSubject())
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("X-RateLimit-Limit = %q, want 1", got)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithSubject())
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429", w.Code)
	}
}

// TestRateLimitBySubject_AnonymousSameIPDifferentUADoesNotBypass is
// the F-1335 regression: the anonymous bucket must be keyed on the
// resolved client IP ALONE. Pre-fix the key folded in a
// sha256(IP|User-Agent) hash, so a client could rotate its
// User-Agent on every request to mint unlimited distinct buckets and
// sail past the per-IP anonymous throttle. Here two requests from the
// SAME IP carry DIFFERENT User-Agents; with a budget of 1 the second
// MUST be 429'd because both share one bucket.
func TestRateLimitBySubject_AnonymousSameIPDifferentUADoesNotBypass(t *testing.T) {
	rdb, _ := newRLRedis(t)
	anonBucket := ratelimit.New(rdb, 1, time.Minute)
	authBucket := ratelimit.New(rdb, 3, time.Minute)

	h := middleware.RateLimitBySubject(anonBucket, authBucket, nil, nil)(okHandler())

	// Same source IP; the two requests differ ONLY in User-Agent. The
	// anonymous Subject.Identifier folds in the UA (sha256(IP|UA)), so
	// if the bucket keyed on Identifier these would be two buckets.
	reqWithUA := func(ua string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "203.0.113.7:5555"
		r.Header.Set("User-Agent", ua)
		// Mirror how the Auth middleware attaches the anonymous
		// Subject: Identifier = per-IP+UA hash, Tier = anonymous.
		subject := auth.Anonymous("anon-hash-for-" + ua)
		return r.WithContext(auth.WithSubject(r.Context(), subject))
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqWithUA("curl/8.0"))
	if w.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqWithUA("Mozilla/5.0 (rotated)"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (rotated UA, same IP) status = %d, want 429 — "+
			"UA rotation bypassed the per-IP anonymous throttle (F-1335)", w.Code)
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
