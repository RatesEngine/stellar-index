package obs_test

import (
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
	obs.CursorLastLedger.WithLabelValues("_warmup").Set(0)
	obs.PriceStalenessSeconds.WithLabelValues("_warmup").Set(0)
	obs.OracleLastUpdateUnix.WithLabelValues("_warmup", "_warmup").Set(0)
	obs.OracleResolutionSeconds.WithLabelValues("_warmup").Set(0)

	ts := httptest.NewServer(obs.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
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
		"ratesengine_cursor_last_ledger",
		"ratesengine_price_staleness_seconds",
		"ratesengine_oracle_last_update_unix",
		"ratesengine_oracle_resolution_seconds",
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
		w.WriteHeader(200)
	})
	mux.HandleFunc("GET /bar", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	})

	h := obs.HTTPMetrics(mux)

	// Hit /foo twice, /bar once.
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest("GET", "/foo", nil))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/bar", nil))

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
		w.WriteHeader(200)
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

func TestHTTPMetrics_UnmatchedRouteLabelled(t *testing.T) {
	// Hit a path with no pattern registered — middleware labels it
	// "unmatched" to prevent cardinality blow-up.
	mux := http.NewServeMux()
	// No routes registered; every request is a 404 with empty pattern.

	h := obs.HTTPMetrics(mux)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/does-not-exist", nil))

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
