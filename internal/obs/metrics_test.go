package obs_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/obs"
)

func TestHandler_ExposesMetrics(t *testing.T) {
	// Warm up every registered Vec with at least one child so the
	// scrape output includes its HELP/TYPE header. Prometheus
	// CounterVec/GaugeVec without children don't appear in scrapes
	// (by design — there's nothing to show).
	obs.HTTPRequestsTotal.WithLabelValues("GET", "/_warmup", "200").Inc()
	obs.HTTPRequestDuration.WithLabelValues("GET", "/_warmup").Observe(0.001)
	obs.SourceEventsTotal.WithLabelValues("_warmup").Inc()
	obs.SourceLagLedgers.WithLabelValues("_warmup").Set(0)
	obs.SourceLastEventUnix.WithLabelValues("_warmup").Set(0)
	obs.SourceEnabled.WithLabelValues("_warmup").Set(0)
	obs.SourceDecodeErrorsTotal.WithLabelValues("_warmup").Inc()
	obs.SourceOrphanEventsTotal.WithLabelValues("_warmup").Inc()
	obs.SourceInsertErrorsTotal.WithLabelValues("_warmup", "trade").Inc()
	obs.RateLimitFailOpenTotal.Inc()
	obs.Sep1CacheOpsTotal.WithLabelValues("hit").Inc()
	obs.CursorLastLedger.WithLabelValues("_warmup").Set(0)
	obs.PriceStalenessSeconds.WithLabelValues("_warmup").Set(0)
	obs.OracleLastUpdateUnix.WithLabelValues("_warmup", "_warmup").Set(0)
	obs.OracleResolutionSeconds.WithLabelValues("_warmup").Set(0)
	obs.AggregatorTicksTotal.WithLabelValues("_warmup").Inc()
	obs.AggregatorVWAPWritesTotal.Inc()
	obs.AggregatorEmptyWindowsTotal.Inc()
	obs.AggregatorDroppedTradesTotal.WithLabelValues("_warmup").Inc()

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	expected := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"ratesengine_source_events_total",
		"ratesengine_source_lag_ledgers",
		"ratesengine_source_last_event_unix",
		"ratesengine_source_enabled",
		"ratesengine_source_decode_errors_total",
		"ratesengine_source_orphan_events_total",
		"ratesengine_source_insert_errors_total",
		"ratesengine_ratelimit_fail_open_total",
		"ratesengine_sep1_cache_ops_total",
		"ratesengine_cursor_last_ledger",
		"ratesengine_price_staleness_seconds",
		"ratesengine_oracle_last_update_unix",
		"ratesengine_oracle_resolution_seconds",
		"ratesengine_aggregator_ticks_total",
		"ratesengine_aggregator_vwap_writes_total",
		"ratesengine_aggregator_empty_windows_total",
		"ratesengine_aggregator_dropped_trades_total",
		// Language-native + process metrics from collectors.
		"go_goroutines",
		"process_open_fds",
	}
	for _, metric := range expected {
		if !strings.Contains(text, metric) {
			t.Errorf("metric %q missing from scrape output", metric)
		}
	}
}

func TestHTTPMetrics_CountsRequests(t *testing.T) {
	// Use a fresh sub-mux to avoid polluting counters across tests.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /foo", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /bar", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	h := obs.HTTPMetrics(mux)

	// Hit /foo twice, /bar once.
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/foo", nil))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/bar", nil))

	// Scrape the registry + look for the counts.
	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	// Assert both counter rows landed. Exact format:
	//   http_requests_total{method="GET",route="/foo",status="200"} 2
	//   http_requests_total{method="GET",route="/bar",status="500"} 1
	for _, want := range []string{
		`http_requests_total{method="GET",route="/foo",status="200"} 2`,
		`http_requests_total{method="GET",route="/bar",status="500"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("scrape missing %q; body:\n%s", want, text)
		}
	}
}

func TestHTTPMetrics_LowercaseMethodIsCanonicalised(t *testing.T) {
	// An attacker sending "get" instead of "GET" would otherwise
	// double the method-label cardinality. Middleware uppercases
	// known methods before stamping the label.
	//
	// Handler catches everything with no pattern so the method label
	// is the only axis varying across requests.
	h := obs.HTTPMetrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, verb := range []string{"get", "GeT", "GET"} {
		r := httptest.NewRequest(verb, "/anything", nil)
		r.Method = verb // httptest.NewRequest uppercases — override.
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, r)
	}

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	// All three requests must collapse into the same method="GET"
	// series — i.e. the counter should reach 3.
	want := `method="GET"`
	if !strings.Contains(text, want) {
		t.Errorf("canonical method label missing; expected %q in scrape", want)
	}
	// Crucially: "get" and "GeT" variants MUST NOT appear
	// separately — that would signal the cardinality leak.
	for _, bad := range []string{`method="get"`, `method="GeT"`} {
		if strings.Contains(text, bad) {
			t.Errorf("non-canonical method leaked into labels: %q", bad)
		}
	}
}

func TestHTTPMetrics_ClientAbortLabelled499(t *testing.T) {
	// A handler that never calls WriteHeader combined with a
	// ctx-cancelled request simulates the "client hung up before
	// we wrote anything" case. Without the 499 label it'd record
	// as 200 (statusRecorder default).
	h := obs.HTTPMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't write anything; rely on ctx cancellation to
		// indicate client abort.
		<-r.Context().Done()
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled
	r := httptest.NewRequest(http.MethodGet, "/v1/slow-op", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), `status="499"`) {
		t.Errorf("expected status=\"499\" for aborted request, got:\n%s", string(body))
	}
}

func TestHTTPMetrics_UnmatchedRouteLabelled(t *testing.T) {
	// Hit a path with no pattern registered — middleware labels it
	// "unmatched" to prevent cardinality blow-up.
	mux := http.NewServeMux()
	// No routes registered; every request is a 404 with empty pattern.

	h := obs.HTTPMetrics(mux)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/does-not-exist", nil))

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), `route="unmatched"`) {
		t.Errorf("expected route=\"unmatched\" label, got:\n%s", string(body))
	}
}

// TestHTTPMetrics_RouteSurvivesWithContextChain pins the regression
// behind /v1/status's broken SLO burn-rate alerts: an inner
// middleware that calls r.WithContext(...) creates a new
// http.Request struct, and ServeMux sets Pattern on that struct —
// not the one HTTPMetrics holds. Without obs.CaptureRoute wired
// innermost, every request labels as "unmatched" in production.
func TestHTTPMetrics_RouteSurvivesWithContextChain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Inner middleware that mimics Logger's r.WithContext shadowing.
	withContextMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), struct{ k string }{"req_id"}, "abc")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	// Production-equivalent stack: HTTPMetrics → withContextMW →
	// CaptureRoute → mux. CaptureRoute is innermost.
	h := obs.HTTPMetrics(withContextMW(obs.CaptureRoute(mux)))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/probe", nil))

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)

	// Positive assertion is sufficient: if the route was lost, this
	// counter would be route="unmatched" instead.
	want := `http_requests_total{method="GET",route="/v1/probe",status="200"} 1`
	if !strings.Contains(text, want) {
		t.Errorf("expected %q, body:\n%s", want, text)
	}
}

// TestHTTPMetrics_SyntheticUASkipsHistogram pins the SLO-noise
// fix: requests with `User-Agent: ratesengine-smoke/N` (the smoke
// timer) MUST NOT contribute to the http_requests_total counter
// or http_request_duration_seconds histogram. Otherwise every
// smoke fire pollutes the SLO recording rule with cold-cache
// samples customers never see.
func TestHTTPMetrics_SyntheticUASkipsHistogram(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/synthetic-probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := obs.HTTPMetrics(mux)

	// Real customer request — should land in the histogram.
	rr := httptest.NewRecorder()
	customerReq := httptest.NewRequest(http.MethodGet, "/v1/synthetic-probe", nil)
	customerReq.Header.Set("User-Agent", "Mozilla/5.0")
	h.ServeHTTP(rr, customerReq)

	// Synthetic probe — should be skipped.
	rr2 := httptest.NewRecorder()
	smokeReq := httptest.NewRequest(http.MethodGet, "/v1/synthetic-probe", nil)
	smokeReq.Header.Set("User-Agent", "ratesengine-smoke/1")
	h.ServeHTTP(rr2, smokeReq)

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Exactly 1 sample for /v1/synthetic-probe — the customer
	// request. The smoke request should be invisible.
	want := `http_requests_total{method="GET",route="/v1/synthetic-probe",status="200"} 1`
	if !strings.Contains(string(body), want) {
		t.Errorf("expected %q, body:\n%s", want, string(body))
	}
	// Defensive: if the smoke request had been counted, the value
	// would be 2. Pin the negative.
	if strings.Contains(string(body), `http_requests_total{method="GET",route="/v1/synthetic-probe",status="200"} 2`) {
		t.Errorf("regression: smoke traffic counted in customer histogram")
	}
}

// TestHTTPMetrics_StreamRouteSkipsDurationHistogram pins the
// latency-SLO fix: an SSE / streaming route (pattern ending in
// /stream) MUST still be counted in http_requests_total but kept
// OUT of the http_request_duration_seconds histogram. The handler
// returns only when the client disconnects, so its "duration" is
// the connection lifetime — feeding that to the latency histogram
// pins p99 at the +Inf bucket and burns the latency SLO.
func TestHTTPMetrics_StreamRouteSkipsDurationHistogram(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/streamtest/stream", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := obs.HTTPMetrics(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/streamtest/stream", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	h.ServeHTTP(rr, req)

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	// The request IS counted in http_requests_total.
	if !strings.Contains(s, `http_requests_total{method="GET",route="/v1/streamtest/stream",status="200"} 1`) {
		t.Errorf("stream request not counted in http_requests_total; body:\n%s", s)
	}
	// The duration is NOT observed — no histogram series for the route.
	if strings.Contains(s, `http_request_duration_seconds_count{method="GET",route="/v1/streamtest/stream"}`) {
		t.Errorf("regression: stream route landed in the latency histogram")
	}
}

// TestZeroSeed_F0033 verifies that the bounded counters whose alert
// rules reference rate()/increase() have all their label combos
// pre-registered at zero, so PromQL queries return 0 rather than
// "no data" before the first event fires. This was F-0033 in the
// 2026-05-26 audit: operators saw the alert reference but no series
// in /metrics output and couldn't tell whether the metric was
// "intentionally zero" or "alert references a dead metric."
func TestZeroSeed_F0033(t *testing.T) {
	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	mustContain := []string{
		`ratesengine_aggregator_triangulations_total{outcome="ok"} 0`,
		`ratesengine_aggregator_triangulations_total{outcome="missing_leg"} 0`,
		`ratesengine_aggregator_triangulations_total{outcome="parse_error"} 0`,
		`ratesengine_aggregator_triangulations_total{outcome="redis_error"} 0`,
		`ratesengine_stripe_platform_sync_errors_total{operation="get_account"} 0`,
		`ratesengine_stripe_platform_sync_errors_total{operation="upsert_subscription"} 0`,
		`ratesengine_stripe_platform_sync_errors_total{operation="account_update"} 0`,
		`ratesengine_stripe_platform_sync_errors_total{operation="list_keys"} 0`,
		`ratesengine_stripe_platform_sync_errors_total{operation="key_update"} 0`,
	}
	for _, want := range mustContain {
		if !strings.Contains(s, want) {
			t.Errorf("scrape body missing pre-seeded series: %q", want)
		}
	}
}
