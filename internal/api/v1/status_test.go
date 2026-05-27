package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeStatusBackend is a hand-rolled StatusBackend that lets tests
// stub each signal independently. Mirrors the four backend methods
// without touching Prometheus.
type fakeStatusBackend struct {
	heartbeats map[string]time.Time
	latency    StatusLatency
	freshness  StatusFreshness
	incidents  StatusIncidents

	hbErr, latErr, freErr, incErr error
}

func (f *fakeStatusBackend) Heartbeats(context.Context) (map[string]time.Time, error) {
	return f.heartbeats, f.hbErr
}

func (f *fakeStatusBackend) Latency(context.Context) (StatusLatency, error) {
	return f.latency, f.latErr
}

func (f *fakeStatusBackend) Freshness(context.Context) (StatusFreshness, error) {
	return f.freshness, f.freErr
}

func (f *fakeStatusBackend) Incidents(context.Context) (StatusIncidents, error) {
	return f.incidents, f.incErr
}

func TestStatus_NoBackend_DegradedSurface(t *testing.T) {
	srv := New(Options{
		RegionName:       "r1",
		RegionDeployment: "production",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rr.Code)
	}

	var env Envelope
	if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	body, _ := json.Marshal(env.Data)
	var st StatusResponse
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("re-decode: %v", err)
	}

	if st.Region.Name != "r1" {
		t.Errorf("Region.Name = %q, want r1", st.Region.Name)
	}
	// F-0055: the in-process surface (api=ok + indexer/aggregator
	// unknown) is partial visibility. The mixed-state branch of
	// the overall rollup returns "degraded" — silently reporting
	// "ok" while two of three services are unknown is the exact
	// bug this regression test pins.
	if st.Overall != "degraded" {
		t.Errorf("Overall = %q, want degraded (partial visibility: api ok, indexer/aggregator unknown)", st.Overall)
	}
	if !env.Flags.Stale {
		t.Errorf("flags.stale = false; want true when no backend wired")
	}

	// Indexer + aggregator should be present but unknown.
	want := map[string]string{"api": "ok", "indexer": "unknown", "aggregator": "unknown"}
	got := map[string]string{}
	for _, s := range st.Services {
		got[s.Name] = s.Status
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("services[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestStatus_WithBackend_HappyPath(t *testing.T) {
	now := time.Now().UTC()
	srv := New(Options{
		RegionName: "r1",
		StatusBackend: &fakeStatusBackend{
			heartbeats: map[string]time.Time{
				"indexer":    now.Add(-5 * time.Second),
				"aggregator": now.Add(-3 * time.Second),
			},
			latency: StatusLatency{P50Ms: 10, P95Ms: 80, P99Ms: 200, WindowSecs: 300},
			freshness: StatusFreshness{
				LastAggregatorTick: now,
				ActiveSources:      14,
				TotalSources:       18,
			},
			incidents: StatusIncidents{ActiveCount: 0},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", rr.Code)
	}
	var env Envelope
	json.NewDecoder(rr.Body).Decode(&env)
	body, _ := json.Marshal(env.Data)
	var st StatusResponse
	json.Unmarshal(body, &st)

	if st.Overall != "ok" {
		t.Errorf("Overall = %q, want ok", st.Overall)
	}
	got := map[string]string{}
	for _, s := range st.Services {
		got[s.Name] = s.Status
	}
	for _, n := range []string{"api", "indexer", "aggregator"} {
		if got[n] != "ok" {
			t.Errorf("services[%q] = %q, want ok", n, got[n])
		}
	}
	if st.Latency.P99Ms != 200 {
		t.Errorf("Latency.P99Ms = %v, want 200", st.Latency.P99Ms)
	}
	if st.Freshness.ActiveSources != 14 {
		t.Errorf("Freshness.ActiveSources = %d, want 14", st.Freshness.ActiveSources)
	}
}

func TestStatus_WithBackend_StaleHeartbeatDown(t *testing.T) {
	now := time.Now().UTC()
	srv := New(Options{
		RegionName: "r1",
		StatusBackend: &fakeStatusBackend{
			heartbeats: map[string]time.Time{
				// 5 minutes old — well past the 60 s threshold.
				"indexer":    now.Add(-5 * time.Minute),
				"aggregator": now.Add(-3 * time.Second),
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var env Envelope
	json.NewDecoder(rr.Body).Decode(&env)
	body, _ := json.Marshal(env.Data)
	var st StatusResponse
	json.Unmarshal(body, &st)

	// Per F-0055 rollup precedence (worst wins): any service in
	// "down" makes overall=down. A stale heartbeat is a definite
	// negative signal, not "degraded" partial visibility.
	if st.Overall != "down" {
		t.Errorf("Overall = %q, want down (stale indexer hb)", st.Overall)
	}
	if !env.Flags.Stale {
		t.Errorf("flags.stale = false; want true when overall != ok")
	}
	for _, s := range st.Services {
		if s.Name == "indexer" && s.Status != "down" {
			t.Errorf("indexer.Status = %q, want down", s.Status)
		}
	}
}

func TestStatus_WithBackend_PageAlertDegrades(t *testing.T) {
	now := time.Now().UTC()
	srv := New(Options{
		RegionName: "r1",
		StatusBackend: &fakeStatusBackend{
			heartbeats: map[string]time.Time{
				"indexer":    now.Add(-3 * time.Second),
				"aggregator": now.Add(-3 * time.Second),
			},
			incidents: StatusIncidents{
				ActiveCount: 2,
				PageCount:   1,
				TicketCount: 1,
				Active: []ActiveIncident{
					{Name: "ratesengine_api_down", Severity: "page"},
					{Name: "ratesengine_aggregator_silent", Severity: "ticket"},
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var env Envelope
	json.NewDecoder(rr.Body).Decode(&env)
	body, _ := json.Marshal(env.Data)
	var st StatusResponse
	json.Unmarshal(body, &st)

	if len(st.Incidents.Active) != 2 {
		t.Fatalf("Active len = %d, want 2", len(st.Incidents.Active))
	}
	if st.Incidents.Active[0].Name != "ratesengine_api_down" {
		t.Errorf("Active[0] = %q, want ratesengine_api_down", st.Incidents.Active[0].Name)
	}

	if st.Overall != "degraded" {
		t.Errorf("Overall = %q, want degraded (page alert firing)", st.Overall)
	}
	if st.Incidents.PageCount != 1 {
		t.Errorf("PageCount = %d, want 1", st.Incidents.PageCount)
	}
}

// TestStatus_BackendErrorDegradesOverall pins the regression
// from r1 2026-05-10: when Prometheus is dead, every backend
// query (Heartbeats, Latency, Freshness, Incidents) errors out;
// /v1/status was returning Overall="ok" because the rollup logic
// only flagged "degraded" inside the success branches. With the
// metrics pipeline blind, "ok" is a lie — degrade so the
// status-page poller (and operators reading the API directly)
// see the real state.
func TestStatus_BackendErrorDegradesOverall(t *testing.T) {
	srv := New(Options{
		RegionName: "r1",
		StatusBackend: &fakeStatusBackend{
			hbErr:  errors.New("prometheus: connection refused"),
			latErr: errors.New("prometheus: connection refused"),
			freErr: errors.New("prometheus: connection refused"),
			incErr: errors.New("prometheus: connection refused"),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	var env Envelope
	json.NewDecoder(rr.Body).Decode(&env)
	body, _ := json.Marshal(env.Data)
	var st StatusResponse
	json.Unmarshal(body, &st)

	if st.Overall != "degraded" {
		t.Errorf("Overall = %q, want degraded (metrics backend unreachable)", st.Overall)
	}
}

func TestPrometheusStatusBackend_QueryShape(t *testing.T) {
	// Hand-rolled HTTP server returning a canned Prometheus
	// instant-query response. Verifies the client parses it
	// correctly without hitting a real Prometheus.
	const body = `{
		"status":"success",
		"data":{
			"resultType":"vector",
			"result":[
				{"metric":{"job":"ratesengine-indexer"},"value":[1730000000,"1730000050"]},
				{"metric":{"job":"ratesengine-aggregator"},"value":[1730000000,"1730000048"]}
			]
		}
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/query") {
			t.Errorf("path = %q, want /api/v1/query*", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	p := &PrometheusStatusBackend{URL: ts.URL}
	hb, err := p.Heartbeats(context.Background())
	if err != nil {
		t.Fatalf("Heartbeats: %v", err)
	}
	if len(hb) != 2 {
		t.Fatalf("hb len = %d, want 2", len(hb))
	}
	if _, ok := hb["indexer"]; !ok {
		t.Errorf("hb[\"indexer\"] missing")
	}
	if _, ok := hb["aggregator"]; !ok {
		t.Errorf("hb[\"aggregator\"] missing")
	}
}

func TestPrometheusStatusBackend_IncidentsParsesAlertsAndCounts(t *testing.T) {
	// Three firing alerts: 1 page, 1 ticket, 1 informational, plus
	// the deadmansswitch which the query excludes server-side. The
	// client tally should match the labels.
	const body = `{
		"status":"success",
		"data":{
			"resultType":"vector",
			"result":[
				{"metric":{"alertname":"ratesengine_api_down","alertstate":"firing","severity":"page"},"value":[1730000000,"1"]},
				{"metric":{"alertname":"ratesengine_aggregator_silent","alertstate":"firing","severity":"ticket"},"value":[1730000000,"1"]},
				{"metric":{"alertname":"ratesengine_host_cpu_high","alertstate":"firing","severity":"informational"},"value":[1730000000,"1"]}
			]
		}
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	p := &PrometheusStatusBackend{URL: ts.URL}
	got, err := p.Incidents(context.Background())
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if got.PageCount != 1 || got.TicketCount != 1 || got.InformationalCount != 1 {
		t.Errorf("counts = %+v, want page=1 ticket=1 info=1", got)
	}
	if got.ActiveCount != 3 {
		t.Errorf("ActiveCount = %d, want 3", got.ActiveCount)
	}
	if len(got.Active) != 3 {
		t.Fatalf("Active len = %d, want 3", len(got.Active))
	}
	// page first, then ticket, then informational.
	wantOrder := []string{
		"ratesengine_api_down",
		"ratesengine_aggregator_silent",
		"ratesengine_host_cpu_high",
	}
	for i, want := range wantOrder {
		if got.Active[i].Name != want {
			t.Errorf("Active[%d] = %q, want %q", i, got.Active[i].Name, want)
		}
	}
}

func TestPrometheusStatusBackend_IncidentsDedupesByAlertname(t *testing.T) {
	// Two label-sets for the same alertname (per-instance fan-out)
	// — the public surface dedupes by name.
	const body = `{
		"status":"success",
		"data":{
			"resultType":"vector",
			"result":[
				{"metric":{"alertname":"ratesengine_host_down","alertstate":"firing","severity":"ticket","instance":"r1"},"value":[1730000000,"1"]},
				{"metric":{"alertname":"ratesengine_host_down","alertstate":"firing","severity":"ticket","instance":"r2"},"value":[1730000000,"1"]}
			]
		}
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	p := &PrometheusStatusBackend{URL: ts.URL}
	got, err := p.Incidents(context.Background())
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if got.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1 (deduped)", got.ActiveCount)
	}
	if len(got.Active) != 1 {
		t.Errorf("Active len = %d, want 1", len(got.Active))
	}
}

// TestStatus_OverallRollup_F0055 pins the F-0055 fix: the
// customer-facing `overall` field is computed from the worst-case
// per-service state plus the two cross-cutting canaries
// (backend-error, page-firing). Each table row exercises one
// branch of the precedence chain in rollupOverall; flags.stale is
// asserted as the inverse of overall=="ok" so the wire envelope
// stays consistent with the rollup verdict.
func TestStatus_OverallRollup_F0055(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-3 * time.Second) // within 60s threshold
	stale := now.Add(-10 * time.Minute) // past 60s threshold

	type expect struct {
		overall string
		stale   bool
	}
	cases := []struct {
		name    string
		backend *fakeStatusBackend
		want    expect
	}{
		{
			// All three services healthy, no canary trips → ok.
			name: "all_ok",
			backend: &fakeStatusBackend{
				heartbeats: map[string]time.Time{
					"indexer": recent, "aggregator": recent,
				},
			},
			want: expect{overall: "ok", stale: false},
		},
		{
			// Indexer heartbeat way past threshold → svc=down → overall=down.
			name: "any_down",
			backend: &fakeStatusBackend{
				heartbeats: map[string]time.Time{
					"indexer": stale, "aggregator": recent,
				},
			},
			want: expect{overall: "down", stale: true},
		},
		{
			// Backend errors on every query AND services degrade
			// to unknown (hbErr != nil branch). api=ok keeps
			// anyOK=true; indexer/aggregator unknown → mixed →
			// degraded (the backendErr canary also forces this).
			name: "backend_error_partial",
			backend: &fakeStatusBackend{
				hbErr:  errors.New("prometheus dead"),
				latErr: errors.New("prometheus dead"),
				freErr: errors.New("prometheus dead"),
				incErr: errors.New("prometheus dead"),
			},
			want: expect{overall: "degraded", stale: true},
		},
		{
			// No service is down/degraded, but a page-severity
			// alert is firing → cross-cutting canary trips
			// degraded.
			name: "page_alert_firing",
			backend: &fakeStatusBackend{
				heartbeats: map[string]time.Time{
					"indexer": recent, "aggregator": recent,
				},
				incidents: StatusIncidents{
					ActiveCount: 1, PageCount: 1,
					Active: []ActiveIncident{
						{Name: "ratesengine_api_down", Severity: "page"},
					},
				},
			},
			want: expect{overall: "degraded", stale: true},
		},
		{
			// Mixed known/unknown (api ok + indexer/aggregator
			// unknown because heartbeats map is empty) → partial
			// visibility surfaces as degraded, NOT ok.
			name: "mixed_ok_and_unknown",
			backend: &fakeStatusBackend{
				heartbeats: map[string]time.Time{
					// neither indexer nor aggregator present
				},
			},
			want: expect{overall: "degraded", stale: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Options{
				RegionName:    "r1",
				StatusBackend: tc.backend,
			})

			req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)

			var env Envelope
			if err := json.NewDecoder(rr.Body).Decode(&env); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			body, _ := json.Marshal(env.Data)
			var st StatusResponse
			if err := json.Unmarshal(body, &st); err != nil {
				t.Fatalf("decode StatusResponse: %v", err)
			}

			if st.Overall != tc.want.overall {
				t.Errorf("Overall = %q, want %q", st.Overall, tc.want.overall)
			}
			if env.Flags.Stale != tc.want.stale {
				t.Errorf("flags.stale = %v, want %v", env.Flags.Stale, tc.want.stale)
			}
		})
	}
}

// TestRollupOverall_AllUnknownBranch exercises the
// every-service-is-unknown branch of rollupOverall directly. The
// http-level tests can't easily synthesise this state because the
// handler always stamps an "ok" api entry; this unit test pokes
// rollupOverall with a synthetic services slice so the "unknown"
// branch (distinct from "down" and from "ok") is pinned.
func TestRollupOverall_AllUnknownBranch(t *testing.T) {
	// All three services unknown, no canary trips → overall=unknown.
	// This is the pure F-0055 evidence-from-prod state: every signal
	// is unknown + zero LastSeen. Previously rolled to "ok"; now
	// rolls to "unknown".
	services := []StatusService{
		{Name: "api", Status: "unknown"},
		{Name: "indexer", Status: "unknown"},
		{Name: "aggregator", Status: "unknown"},
	}
	if got := rollupOverall(services, false, false); got != "unknown" {
		t.Errorf("all-unknown rollup = %q, want unknown", got)
	}

	// Zero-time LastSeen on a non-api "ok" service is treated as
	// unknown — guards against a backend returning Status="ok"
	// with no heartbeat data behind it.
	services = []StatusService{
		{Name: "indexer", Status: "ok"}, // zero LastSeen
	}
	if got := rollupOverall(services, false, false); got != "unknown" {
		t.Errorf("zero-time ok rollup = %q, want unknown", got)
	}

	// A service explicitly marked degraded → overall=degraded.
	services = []StatusService{
		{Name: "api", Status: "ok", LastSeen: time.Now()},
		{Name: "indexer", Status: "degraded", LastSeen: time.Now()},
	}
	if got := rollupOverall(services, false, false); got != "degraded" {
		t.Errorf("any-degraded rollup = %q, want degraded", got)
	}
}
