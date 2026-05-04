package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPercentile_LinearInterp(t *testing.T) {
	cases := []struct {
		name string
		xs   []float64
		p    float64
		want float64
	}{
		{"empty", nil, 0.5, 0},
		{"single", []float64{42}, 0.95, 42},
		{"sorted-five-p50", []float64{1, 2, 3, 4, 5}, 0.50, 3},
		{"sorted-five-p95", []float64{1, 2, 3, 4, 5}, 0.95, 4.8},
		{"unsorted", []float64{5, 1, 3, 2, 4}, 0.50, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := percentile(append([]float64(nil), tc.xs...), tc.p)
			if abs(got-tc.want) > 1e-9 {
				t.Errorf("percentile(%v, %g) = %g, want %g", tc.xs, tc.p, got, tc.want)
			}
		})
	}
}

func TestRunProbe_PassPath(t *testing.T) {
	// Fake API: every request returns 200 + a healthz-shaped body
	// + an observed_at near now (so freshness < 30s).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"observed_at":"` + time.Now().UTC().Format(time.RFC3339) + `","price":"1.0"}}`))
	}))
	defer srv.Close()

	endpoints := []endpoint{
		{Name: "healthz", Path: "/healthz"},
		{Name: "price", Path: "/price", Query: map[string]string{"asset": "native", "quote": "fiat:USD"}},
	}
	rep := runProbe(srv.URL, "", endpoints, 200*time.Millisecond, 2, slaTargets{
		P95MS:           500, // very generous so the test isn't flaky
		P99MS:           1000,
		FreshnessSec:    30,
		AvailabilityPct: 99.0,
	})
	if rep.Verdict != "pass" {
		t.Errorf("verdict=%q want pass; failed=%v", rep.Verdict, rep.FailedReasons)
	}
	if len(rep.PerEndpoint) != 2 {
		t.Fatalf("PerEndpoint len=%d want 2", len(rep.PerEndpoint))
	}
	for _, st := range rep.PerEndpoint {
		if st.Samples == 0 {
			t.Errorf("%s: no samples collected", st.Endpoint)
		}
		if st.AvailabilityPct < 99.0 {
			t.Errorf("%s: availability=%g unexpectedly low", st.Endpoint, st.AvailabilityPct)
		}
	}
}

func TestRunProbe_FailsOnSlowEndpoint(t *testing.T) {
	// Fake API that delays 600ms — definitely > 200ms p95 target.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond) // moderate; we set tight target below to simulate fail
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	endpoints := []endpoint{{Name: "healthz", Path: "/healthz"}}
	rep := runProbe(srv.URL, "", endpoints, 200*time.Millisecond, 2, slaTargets{
		P95MS:           1, // 1ms target — we'll definitely exceed
		P99MS:           1,
		FreshnessSec:    30,
		AvailabilityPct: 99.0,
	})
	if rep.Verdict == "pass" {
		t.Errorf("verdict=pass but slow endpoint should fail tight latency target")
	}
	if len(rep.FailedReasons) == 0 {
		t.Errorf("FailedReasons empty but verdict=fail")
	}
}

func TestRunProbe_FailsOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	endpoints := []endpoint{{Name: "healthz", Path: "/healthz"}}
	rep := runProbe(srv.URL, "", endpoints, 100*time.Millisecond, 1, slaTargets{
		P95MS:           1000,
		P99MS:           5000,
		FreshnessSec:    300,
		AvailabilityPct: 99.0,
	})
	if rep.Verdict == "pass" {
		t.Errorf("verdict=pass but 5xx should fail availability")
	}
	if rep.PerEndpoint[0].AvailabilityPct >= 1 {
		t.Errorf("availability=%g but server always 500'd", rep.PerEndpoint[0].AvailabilityPct)
	}
}

func TestHit_ParsesObservedAt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"observed_at":"` + now.Format(time.RFC3339) + `"}}`))
	}))
	defer srv.Close()
	c := &http.Client{Timeout: time.Second}
	_, ok, observed := hit(context.Background(), c, srv.URL, "", endpoint{Path: "/x"})
	if !ok {
		t.Fatal("hit returned not-ok")
	}
	if !observed.Equal(now) {
		t.Errorf("observed=%v want %v", observed, now)
	}
}

func TestHit_NoObservedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`)) // no data.observed_at
	}))
	defer srv.Close()
	c := &http.Client{Timeout: time.Second}
	_, ok, observed := hit(context.Background(), c, srv.URL, "", endpoint{Path: "/x"})
	if !ok {
		t.Fatal("hit returned not-ok on 200")
	}
	if !observed.IsZero() {
		t.Errorf("observed=%v want zero", observed)
	}
}

func TestHit_AttachesAuthorizationWhenAPIKeySet(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &http.Client{Timeout: time.Second}
	_, ok, _ := hit(context.Background(), c, srv.URL, "rek_test_xyz", endpoint{Path: "/x"})
	if !ok {
		t.Fatal("hit returned not-ok")
	}
	if sawAuth != "Bearer rek_test_xyz" {
		t.Errorf("Authorization = %q, want %q", sawAuth, "Bearer rek_test_xyz")
	}
}

func TestHit_OmitsAuthorizationWhenAPIKeyEmpty(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &http.Client{Timeout: time.Second}
	_, _, _ = hit(context.Background(), c, srv.URL, "", endpoint{Path: "/x"})
	if sawAuth != "" {
		t.Errorf("Authorization = %q, want empty (no key passed)", sawAuth)
	}
}

func TestStaticEndpoints_AllCriticalIncluded(t *testing.T) {
	es := staticEndpoints()
	want := map[string]bool{"healthz": false, "readyz": false, "version": false}
	for _, e := range es {
		want[e.Name] = true
	}
	for n, found := range want {
		if !found {
			t.Errorf("staticEndpoints missing %q", n)
		}
	}
}

func TestPairEndpoints_BuildsExpected(t *testing.T) {
	es := pairEndpoints("native", "fiat:USD")
	names := make(map[string]bool)
	for _, e := range es {
		names[e.Name] = true
		if e.Query["asset"] != "native" {
			t.Errorf("%s: asset=%q want native", e.Name, e.Query["asset"])
		}
	}
	if !names["price"] {
		t.Error("pair endpoints missing 'price'")
	}
}

// JSON round-trip sanity for the report shape — anything that
// silently breaks the JSON output would surface here.
func TestReport_JSONRoundTrip(t *testing.T) {
	rep := report{
		BaseURL:     "https://api.example.com/v1",
		StartedAt:   time.Now().UTC(),
		DurationSec: 30,
		Concurrency: 4,
		SLA:         slaTargets{P95MS: 200, P99MS: 500, FreshnessSec: 30, AvailabilityPct: 99.9},
		PerEndpoint: []stats{
			{
				Endpoint: "price", Path: "/price", Samples: 100, Successes: 100, AvailabilityPct: 100,
				LatencyMS: latencyStats{P50: 12, P95: 45, P99: 78, Max: 102, Mean: 18},
			},
		},
		Verdict: "pass",
	}
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"verdict":"pass"`) {
		t.Errorf("marshalled JSON missing verdict: %s", b)
	}
	var rt report
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rt.PerEndpoint[0].LatencyMS.P95 != 45 {
		t.Errorf("round-trip lost p95: %v", rt.PerEndpoint[0])
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
