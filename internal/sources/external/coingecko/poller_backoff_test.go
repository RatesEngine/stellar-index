package coingecko

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Pre-fix, an upstream 429 returned an error and the poll loop's
// fixed-cadence ticker would re-fire 60 s later, hitting the venue
// again — observed live on r1 2026-05-09 as one WARN per minute.
// These tests pin the new behaviour:
//   - 429 arms a cooldown using Retry-After (or exponential backoff).
//   - Subsequent PollOnce calls during cooldown skip the HTTP request
//     entirely and return (nil, nil, nil) — distinct from an error.
//   - A successful response resets the backoff.
//   - 403 is treated the same as 429 (CoinGecko's post-2024 demo-key-
//     required path returns 403, not 429).

func newCountingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestPollOnce_429_ArmsCooldown_SkipsNextCall(t *testing.T) {
	srv, hits := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"throttled"}`))
	})

	p := NewPoller()
	p.Endpoint = srv.URL

	// First call hits the venue and gets 429 — error returned, cooldown armed.
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Fatal("expected error from 429")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 after first 429", got)
	}
	if p.cooldownRemaining() <= 0 {
		t.Fatal("cooldown should be armed after 429")
	}

	// Second call must be a silent no-op (no HTTP, no error).
	trades, updates, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Errorf("second call should not error during cooldown: %v", err)
	}
	if trades != nil || updates != nil {
		t.Errorf("second call should return (nil, nil), got %v / %v", trades, updates)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d after second call, want still 1 (cooldown skipped HTTP)", got)
	}
}

func TestPollOnce_429_HonorsRetryAfter(t *testing.T) {
	const retrySeconds = 90 // > MinBackoff so we can verify it overrides the floor
	srv, _ := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
		w.WriteHeader(http.StatusTooManyRequests)
	})

	p := NewPoller()
	p.Endpoint = srv.URL

	_, _, _ = p.PollOnce(context.Background(), buildPairs(t))

	cooldown := p.cooldownRemaining()
	// Retry-After of 90 s, with a small slack for test scheduling.
	if cooldown < 85*time.Second || cooldown > retrySeconds*time.Second {
		t.Errorf("cooldown = %v, want ~%ds (Retry-After honoured)", cooldown, retrySeconds)
	}
}

func TestPollOnce_429_NoRetryAfter_UsesMinBackoff(t *testing.T) {
	srv, _ := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})

	p := NewPoller()
	p.Endpoint = srv.URL

	_, _, _ = p.PollOnce(context.Background(), buildPairs(t))

	cooldown := p.cooldownRemaining()
	if cooldown < (MinBackoff-2*time.Second) || cooldown > MinBackoff {
		t.Errorf("cooldown = %v, want ~%v (no Retry-After → MinBackoff)", cooldown, MinBackoff)
	}
}

func TestPollOnce_SuccessResetsBackoff(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusTooManyRequests)

	srv, _ := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			_, _ = w.Write([]byte(`{"stellar":{"usd":0.17}}`))
		}
	})

	p := NewPoller()
	p.Endpoint = srv.URL

	// First poll: 429, backoff armed.
	_, _, _ = p.PollOnce(context.Background(), buildPairs(t))
	if p.currentBackoff == 0 {
		t.Fatal("expected currentBackoff > 0 after 429")
	}

	// Manually expire the cooldown (test scaffolding — production
	// waits real time, but we don't want the test to sleep 60s).
	p.mu.Lock()
	p.nextAllowedAt = time.Time{}
	p.mu.Unlock()

	// Flip the server to 200 OK; poll again.
	status.Store(http.StatusOK)
	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if p.currentBackoff != 0 {
		t.Errorf("currentBackoff = %v after success, want 0 (reset)", p.currentBackoff)
	}
	if !p.nextAllowedAt.IsZero() {
		t.Errorf("nextAllowedAt should be zero after success reset")
	}
}

func TestPollOnce_403_TreatedAsThrottling(t *testing.T) {
	// CoinGecko's post-2024 free-tier-without-demo-key returns 403,
	// not 429. The cooldown must apply either way so we don't hammer
	// the venue with refusals.
	srv, hits := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":{"error_code":429,"error_message":"You've exceeded the Rate Limit"}}`))
	})

	p := NewPoller()
	p.Endpoint = srv.URL

	_, _, err := p.PollOnce(context.Background(), buildPairs(t))
	if err == nil {
		t.Fatal("expected error from 403")
	}
	if p.cooldownRemaining() <= 0 {
		t.Error("cooldown should be armed after 403")
	}

	// Second call: skipped by cooldown.
	_, _, _ = p.PollOnce(context.Background(), buildPairs(t))
	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d, want 1 (cooldown skipped second HTTP)", got)
	}
}

func TestBackoffFromRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		hdr  string
		want time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "30", 30 * time.Second},
		{"seconds-with-whitespace", "  60 ", 60 * time.Second},
		{"zero", "0", 0},
		{"negative", "-5", 0},
		{"garbage", "not-a-number", 0},
		{"http-date-past", "Mon, 01 Jan 2000 00:00:00 GMT", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := backoffFromRetryAfter(tc.hdr)
			if got != tc.want {
				t.Errorf("backoffFromRetryAfter(%q) = %v, want %v", tc.hdr, got, tc.want)
			}
		})
	}
}

func TestPollOnce_DemoAPIKeyParameter(t *testing.T) {
	var seenQuery string
	srv, _ := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	p := NewPoller()
	p.Endpoint = srv.URL
	p.DemoAPIKey = "demo-test-key-123"

	_, _, _ = p.PollOnce(context.Background(), []canonical.Pair{
		mustPair(t, "XLM", "USD"),
	})

	if !contains(seenQuery, "x_cg_demo_api_key=demo-test-key-123") {
		t.Errorf("query missing demo key param; got %q", seenQuery)
	}
	if contains(seenQuery, "x_cg_pro_api_key") {
		t.Error("Pro key param should not be set when only DemoAPIKey is configured")
	}
}

func TestPollOnce_ProAPIKeyWinsOverDemo(t *testing.T) {
	var seenQuery string
	srv, _ := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	p := NewPoller()
	p.Endpoint = srv.URL
	p.APIKey = "pro-key"
	p.DemoAPIKey = "demo-key"

	_, _, _ = p.PollOnce(context.Background(), []canonical.Pair{
		mustPair(t, "XLM", "USD"),
	})

	if !contains(seenQuery, "x_cg_pro_api_key=pro-key") {
		t.Errorf("Pro key should be sent when both are set; got %q", seenQuery)
	}
	if contains(seenQuery, "x_cg_demo_api_key") {
		t.Error("Demo key should NOT be sent when Pro key is set")
	}
}

func mustPair(t *testing.T, base, quote string) canonical.Pair {
	t.Helper()
	b, err := canonical.NewCryptoAsset(base)
	if err != nil {
		t.Fatalf("NewCryptoAsset(%q): %v", base, err)
	}
	q, err := canonical.NewFiatAsset(quote)
	if err != nil {
		t.Fatalf("NewFiatAsset(%q): %v", quote, err)
	}
	pair, err := canonical.NewPair(b, q)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return pair
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
