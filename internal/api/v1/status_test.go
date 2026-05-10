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
	if st.Overall != "ok" {
		t.Errorf("Overall = %q, want ok (in-process surface)", st.Overall)
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

func TestStatus_WithBackend_StaleHeartbeatDegraded(t *testing.T) {
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

	if st.Overall != "degraded" {
		t.Errorf("Overall = %q, want degraded (stale indexer hb)", st.Overall)
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
