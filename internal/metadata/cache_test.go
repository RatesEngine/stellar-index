package metadata_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/metadata"
)

func newCacheRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c, mr
}

// newCacheFixtureServer returns a TLS test server that counts how
// many times it served stellar.toml.
func newCacheFixtureServer(t *testing.T, hits *int64) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/stellar.toml" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt64(hits, 1)
		_, _ = w.Write([]byte(fixtureTOML))
	}))
}

func TestCache_ReadThroughAndReuse(t *testing.T) {
	var hits int64
	srv := newCacheFixtureServer(t, &hits)
	defer srv.Close()

	rdb, _ := newCacheRedis(t)
	r := newLocalResolver(t, srv)
	c := metadata.NewCache(r, rdb)

	ctx := context.Background()
	dom := hostOf(t, srv)

	// First call — miss, fetches upstream.
	sep, err := c.Resolve(ctx, dom)
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if sep.OrgName != "Circle Internet Financial Limited" {
		t.Errorf("OrgName = %q", sep.OrgName)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("upstream hits after first call: %d (want 1)", got)
	}

	// Second call — should land in redis.
	sep2, err := c.Resolve(ctx, dom)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if sep2.OrgName != sep.OrgName {
		t.Errorf("OrgName mismatch across calls: %q vs %q", sep.OrgName, sep2.OrgName)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("upstream hits after second call: %d (want still 1 — cached)", got)
	}
}

func TestCache_InvalidateForcesRefresh(t *testing.T) {
	var hits int64
	srv := newCacheFixtureServer(t, &hits)
	defer srv.Close()

	rdb, _ := newCacheRedis(t)
	c := metadata.NewCache(newLocalResolver(t, srv), rdb)

	ctx := context.Background()
	dom := hostOf(t, srv)

	if _, err := c.Resolve(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if err := c.Invalidate(ctx, dom); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := c.Resolve(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("upstream hits after invalidate+re-resolve: %d (want 2)", got)
	}
}

func TestCache_RespectsCallerCtxCancellation(t *testing.T) {
	// Regression: singleflight.Group.Do blocks the waiting caller
	// past its own ctx deadline because Do doesn't accept a ctx.
	// Switched to DoChan with a per-caller select so a caller
	// whose ctx cancels returns promptly with ctx.Err, regardless
	// of whether the underlying fetch is still in flight.
	// Server blocks "long enough" — 2s — not forever, so the test
	// doesn't hang at srv.Close() drain time.
	slowSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slowSrv.Close()

	rdb, _ := newCacheRedis(t)
	c := metadata.NewCache(newLocalResolver(t, slowSrv), rdb)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.Resolve(ctx, hostOf(t, slowSrv))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx cancellation error")
	}
	// Caller must return within a small multiple of the ctx deadline
	// — NOT wait for the resolver's 8s timeout.
	if elapsed > 500*time.Millisecond {
		t.Errorf("Resolve took %v — should return within ctx deadline (~50ms)", elapsed)
	}
}

func TestCache_NegativeResultsNotCached(t *testing.T) {
	// 404-only server; cache should fall through every call.
	var hits int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rdb, _ := newCacheRedis(t)
	c := metadata.NewCache(newLocalResolver(t, srv), rdb)

	ctx := context.Background()
	dom := hostOf(t, srv)

	if _, err := c.Resolve(ctx, dom); err == nil {
		t.Fatal("first Resolve: want error")
	}
	if _, err := c.Resolve(ctx, dom); err == nil {
		t.Fatal("second Resolve: want error")
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("upstream hits (negative-cache check): %d (want 2 — errors must not cache)", got)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	var hits int64
	srv := newCacheFixtureServer(t, &hits)
	defer srv.Close()

	rdb, mr := newCacheRedis(t)
	c := metadata.NewCache(newLocalResolver(t, srv), rdb)

	ctx := context.Background()
	dom := hostOf(t, srv)

	if _, err := c.Resolve(ctx, dom); err != nil {
		t.Fatal(err)
	}
	// Fast-forward past TTL.
	mr.FastForward(cachekeys.TOMLTTL + time.Second)

	if _, err := c.Resolve(ctx, dom); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("upstream hits after TTL expiry: %d (want 2)", got)
	}
}

func TestCache_CoalescesConcurrentFetches(t *testing.T) {
	// singleflight contract: N concurrent callers for the SAME domain
	// share ONE upstream fetch. Proves we don't hammer the issuer's
	// server when a popular asset fans out across request handlers.
	//
	// The handler waits 50 ms before responding so all callers are
	// provably in the singleflight window concurrently. Without
	// coalescing, hits = 10 (or close). With it, hits = 1.
	var hits int64
	slowSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/stellar.toml" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(fixtureTOML))
	}))
	defer slowSrv.Close()

	rdb, _ := newCacheRedis(t)
	c := metadata.NewCache(newLocalResolver(t, slowSrv), rdb)
	dom := hostOf(t, slowSrv)

	const N = 10
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			_, err := c.Resolve(context.Background(), dom)
			errs <- err
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-errs; err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("upstream hits with %d concurrent callers: %d (want 1 — singleflight should coalesce)", N, got)
	}
}

func TestCache_NilRedisFallsThrough(t *testing.T) {
	// NewCache(r, nil) should still work — it just always calls
	// upstream. Useful for local dev without redis.
	var hits int64
	srv := newCacheFixtureServer(t, &hits)
	defer srv.Close()

	c := metadata.NewCache(newLocalResolver(t, srv), nil)

	ctx := context.Background()
	dom := hostOf(t, srv)

	for i := 0; i < 3; i++ {
		if _, err := c.Resolve(ctx, dom); err != nil {
			t.Fatalf("Resolve %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(&hits); got != 3 {
		t.Errorf("upstream hits with nil redis: %d (want 3 — no caching)", got)
	}
}
