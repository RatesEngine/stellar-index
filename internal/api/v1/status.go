package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StatusResponse is the wire shape for /v1/status — a customer-
// facing rollup of system health. Distinct from /v1/healthz
// (shallow liveness, k8s-style) and /v1/readyz (deep dependency
// ping): /v1/status is what the public explorer status page
// renders and what the "are the SLAs being met" pages
// are built from.
//
// Operators inspecting /v1/diagnostics/cursors get more detail;
// /v1/status is the human-friendly summary.
type StatusResponse struct {
	// Overall is "ok" when every signal is healthy, "degraded" when
	// at least one signal is unhealthy but the API is still serving,
	// "down" when no live signal is available.
	Overall string `json:"overall"`

	// Region identifies which region this response came from.
	// Currently single-region (r1); multi-region rollup is a future
	// surface.
	Region StatusRegion `json:"region"`

	// Services reports per-binary heartbeats. Always populated:
	// the API process itself is queryable, and the indexer +
	// aggregator heartbeats come from Prometheus when wired.
	Services []StatusService `json:"services"`

	// Latency reports the API histogram-derived percentiles over
	// the last 5 minutes. Zero values indicate Prometheus isn't
	// wired or there are no samples in the window.
	Latency StatusLatency `json:"latency"`

	// Freshness summarises the ingest layer.
	Freshness StatusFreshness `json:"freshness"`

	// Incidents counts active alerts in Alertmanager by severity.
	// Zero values indicate Alertmanager isn't wired or no alerts
	// are firing.
	Incidents StatusIncidents `json:"incidents"`
}

type StatusRegion struct {
	Name       string `json:"name"`
	Deployment string `json:"deployment"`
}

type StatusService struct {
	Name     string    `json:"name"`
	Status   string    `json:"status"` // "ok" | "down" | "unknown"
	LastSeen time.Time `json:"last_seen,omitempty"`
}

type StatusLatency struct {
	P50Ms      float64 `json:"p50_ms"`
	P95Ms      float64 `json:"p95_ms"`
	P99Ms      float64 `json:"p99_ms"`
	WindowSecs int     `json:"window_secs"`
}

type StatusFreshness struct {
	LastAggregatorTick time.Time `json:"last_aggregator_tick,omitempty"`
	ActiveSources      int       `json:"active_sources"`
	TotalSources       int       `json:"total_sources"`
}

type StatusIncidents struct {
	ActiveCount        int `json:"active_count"`
	PageCount          int `json:"page_count"`
	TicketCount        int `json:"ticket_count"`
	InformationalCount int `json:"informational_count"`

	// Active is the (deduplicated, severity-page-first) list of
	// currently-firing alerts. Capped server-side at 16 — operators
	// in a real incident reach for /metrics or alertmanager UI
	// directly; this surface is a customer-facing "what's broken".
	// Empty when no alerts are firing OR no Prometheus backend is
	// wired.
	Active []ActiveIncident `json:"active,omitempty"`
}

// ActiveIncident is one entry in [StatusIncidents.Active] — the
// fields a public status page wants to render. Internal labels
// (component, team, instance) are deliberately excluded so this
// surface stays anonymous-friendly. RunbookURL is included
// because the runbooks themselves are public GitHub markdown —
// operators clicking through from the status page during an
// incident benefit from the direct link, and surfacing it doesn't
// leak any operator-only signal that wasn't already public.
type ActiveIncident struct {
	Name       string `json:"name"`
	Severity   string `json:"severity"`
	RunbookURL string `json:"runbook_url,omitempty"`
}

// StatusBackend pulls signals from a metrics + alerting stack.
// Production wiring is [PrometheusStatusBackend]; nil means we
// degrade to in-process data only (uptime, region label).
type StatusBackend interface {
	// Heartbeats returns the latest scrape time per service.
	// Map key is a friendly service name ("indexer", "aggregator",
	// "api"); value is when Prometheus last successfully scraped
	// the corresponding job. A zero time means the scrape has
	// never succeeded.
	Heartbeats(ctx context.Context) (map[string]time.Time, error)

	// Latency returns the 5-minute p50/p95/p99 of the API
	// histogram. Zero values mean no samples in the window.
	Latency(ctx context.Context) (StatusLatency, error)

	// Freshness returns aggregator + source-count signals.
	Freshness(ctx context.Context) (StatusFreshness, error)

	// Incidents returns the count of currently-firing alerts
	// grouped by severity label.
	Incidents(ctx context.Context) (StatusIncidents, error)

	// SourceEntries24h returns the per-source count of events
	// ingested in the trailing 24h, from the same per-source counter
	// (stellarindex_source_events_total) that backs active_sources.
	// Universal across on-chain and external sources and cheap (a
	// Prometheus range-increase, not a trades-hypertable scan).
	// Sources with no events in the window are absent from the map.
	SourceEntries24h(ctx context.Context) (map[string]int64, error)
}

// PrometheusStatusBackend hits a local Prometheus' HTTP query
// API. The URL points at the v1 root (e.g. http://localhost:9090);
// the client appends /api/v1/query for instant queries.
type PrometheusStatusBackend struct {
	URL    string
	Client *http.Client
}

func (p *PrometheusStatusBackend) Heartbeats(ctx context.Context) (map[string]time.Time, error) {
	// up{job=...} returns 1 if scrape succeeded, 0 otherwise.
	// The metric's timestamp is the scrape time — we want the
	// latest scrape time per job, which we derive from
	// timestamp(up{job=...}).
	const q = `timestamp(up{job=~"stellarindex-indexer|stellarindex-aggregator|stellarindex-api"})`
	res, err := p.queryVector(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(res))
	for _, sample := range res {
		job, _ := sample.Labels["job"].(string)
		short := strings.TrimPrefix(job, "stellarindex-")
		if t, ok := sample.Float(); ok {
			out[short] = time.Unix(int64(t), 0).UTC()
		}
	}
	return out, nil
}

func (p *PrometheusStatusBackend) Latency(ctx context.Context) (StatusLatency, error) {
	out := StatusLatency{WindowSecs: 300}
	for _, q := range []struct {
		expr   string
		target *float64
	}{
		{
			`histogram_quantile(0.5, sum by (le) (rate(http_request_duration_seconds_bucket{job="stellarindex-api"}[5m])))`,
			&out.P50Ms,
		},
		{
			`histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{job="stellarindex-api"}[5m])))`,
			&out.P95Ms,
		},
		{
			`histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket{job="stellarindex-api"}[5m])))`,
			&out.P99Ms,
		},
	} {
		res, err := p.queryVector(ctx, q.expr)
		if err != nil {
			return out, err
		}
		for _, sample := range res {
			if v, ok := sample.Float(); ok {
				*q.target = v * 1000 // s → ms
			}
		}
	}
	return out, nil
}

func (p *PrometheusStatusBackend) Freshness(ctx context.Context) (StatusFreshness, error) {
	var out StatusFreshness

	// Active sources: those whose source_enabled == 1 AND have
	// emitted an event in the last 10 minutes.
	if res, err := p.queryVector(ctx,
		`count(rate(stellarindex_source_events_total[10m]) > 0)`); err == nil {
		for _, s := range res {
			if v, ok := s.Float(); ok {
				out.ActiveSources = int(v)
			}
		}
	}

	// Total sources configured as enabled.
	if res, err := p.queryVector(ctx,
		`count(stellarindex_source_enabled == 1)`); err == nil {
		for _, s := range res {
			if v, ok := s.Float(); ok {
				out.TotalSources = int(v)
			}
		}
	}

	// Last aggregator tick — the timestamp of the most recent
	// vwap-write counter increment.
	if res, err := p.queryVector(ctx,
		`max(timestamp(stellarindex_aggregator_vwap_writes_total))`); err == nil {
		for _, s := range res {
			if v, ok := s.Float(); ok && v > 0 {
				out.LastAggregatorTick = time.Unix(int64(v), 0).UTC()
			}
		}
	}

	return out, nil
}

func (p *PrometheusStatusBackend) Incidents(ctx context.Context) (StatusIncidents, error) {
	var out StatusIncidents

	// Single query over ALERTS gives us names AND lets us count by
	// severity client-side — saves three round-trips. Excludes the
	// deadmansswitch (it fires constantly by design) and the
	// recording-rule artefacts that show up in ALERTS as
	// alertname="" when severity isn't set.
	res, err := p.queryVector(ctx,
		`ALERTS{alertstate="firing",alertname!="stellarindex_deadmansswitch",alertname!=""}`)
	if err != nil {
		return out, err
	}

	const maxActive = 16
	seen := make(map[string]bool, len(res))
	for _, sample := range res {
		name, _ := sample.Labels["alertname"].(string)
		severity, _ := sample.Labels["severity"].(string)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		switch severity {
		case "page":
			out.PageCount++
		case "ticket":
			out.TicketCount++
		case "informational":
			out.InformationalCount++
		}

		if len(out.Active) < maxActive {
			runbookURL, _ := sample.Labels["runbook_url"].(string)
			out.Active = append(out.Active, ActiveIncident{
				Name:       name,
				Severity:   severity,
				RunbookURL: runbookURL,
			})
		}
	}

	// Sort by severity (page > ticket > informational) so the most
	// urgent surfaces first when truncation matters.
	sort.SliceStable(out.Active, func(i, j int) bool {
		return severityRank(out.Active[i].Severity) < severityRank(out.Active[j].Severity)
	})

	out.ActiveCount = out.PageCount + out.TicketCount + out.InformationalCount
	return out, nil
}

func severityRank(s string) int {
	switch s {
	case "page":
		return 0
	case "ticket":
		return 1
	case "informational":
		return 2
	default:
		return 3
	}
}

// promSample is one (metric, value) pair from the Prometheus
// instant query response.
type promSample struct {
	Labels map[string]any
	Value  []any // [unix_ts, "value"]
}

func (s promSample) Float() (float64, bool) {
	if len(s.Value) != 2 {
		return 0, false
	}
	str, ok := s.Value[1].(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// SourceEntries24h implements [StatusBackend]. See the interface doc.
func (p *PrometheusStatusBackend) SourceEntries24h(ctx context.Context) (map[string]int64, error) {
	const q = `sum by (source) (increase(stellarindex_source_events_total[24h]))`
	res, err := p.queryVector(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(res))
	for _, sample := range res {
		src, _ := sample.Labels["source"].(string)
		if src == "" {
			continue
		}
		if v, ok := sample.Float(); ok && v > 0 {
			out[src] = int64(v + 0.5) // round to nearest whole event
		}
	}
	return out, nil
}

func (p *PrometheusStatusBackend) queryVector(ctx context.Context, expr string) ([]promSample, error) {
	if p.URL == "" {
		return nil, fmt.Errorf("prometheus URL not configured")
	}
	u := p.URL + "/api/v1/query?query=" + url.QueryEscape(expr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus HTTP %d", resp.StatusCode)
	}
	var env struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]any `json:"metric"`
				Value  []any          `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if env.Status != "success" {
		return nil, fmt.Errorf("prometheus query non-success")
	}
	out := make([]promSample, 0, len(env.Data.Result))
	for _, r := range env.Data.Result {
		out = append(out, promSample{Labels: r.Metric, Value: r.Value})
	}
	return out, nil
}

// handleStatus serves GET /v1/status. Anonymous-friendly. Always
// returns 200; the body's `overall` field reports degraded state
// rather than an HTTP error so monitoring dashboards can poll a
// single endpoint without alerting on 503s.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := StatusResponse{
		Overall: "ok",
		Region: StatusRegion{
			Name:       s.regionName,
			Deployment: s.regionDeployment,
		},
		Services: []StatusService{
			{Name: "api", Status: "ok", LastSeen: time.Now().UTC()},
		},
	}

	if s.statusBackend == nil {
		// No metrics backend wired — return the in-process surface.
		// Indexer + aggregator heartbeats are unknown.
		out.Services = append(out.Services,
			StatusService{Name: "indexer", Status: "unknown"},
			StatusService{Name: "aggregator", Status: "unknown"},
		)
		out.Overall = rollupOverall(out.Services, false, false)
		writeJSON(w, out, Flags{Stale: out.Overall != "ok"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// Run the four backend queries concurrently — total wall time
	// is dominated by the slowest query, which keeps the endpoint
	// responsive even when Prometheus is sluggish.
	var (
		wg             sync.WaitGroup
		hb             map[string]time.Time
		latency        StatusLatency
		freshness      StatusFreshness
		incidents      StatusIncidents
		hbErr, latErr  error
		freErr, incErr error
	)
	wg.Add(4)
	go func() { defer wg.Done(); hb, hbErr = s.statusBackend.Heartbeats(ctx) }()
	go func() { defer wg.Done(); latency, latErr = s.statusBackend.Latency(ctx) }()
	go func() { defer wg.Done(); freshness, freErr = s.statusBackend.Freshness(ctx) }()
	go func() { defer wg.Done(); incidents, incErr = s.statusBackend.Incidents(ctx) }()
	wg.Wait()

	// Indexer + aggregator heartbeats. A heartbeat older than
	// 60 s flags the service down. If the metrics backend is
	// unreachable, the heartbeat is "unknown".
	now := time.Now().UTC()
	staleAfter := 60 * time.Second
	for _, name := range []string{"indexer", "aggregator"} {
		svc := StatusService{Name: name, Status: "unknown"}
		if hbErr == nil {
			if t, ok := hb[name]; ok && !t.IsZero() {
				svc.LastSeen = t
				if now.Sub(t) <= staleAfter {
					svc.Status = "ok"
				} else {
					svc.Status = "down"
				}
			}
		}
		out.Services = append(out.Services, svc)
	}

	if latErr == nil {
		out.Latency = latency
	}
	if freErr == nil {
		out.Freshness = freshness
	}
	if incErr == nil {
		out.Incidents = incidents
	}

	// Compute overall from the worst-case per-service state and the
	// backend-canary signals. F-0055: previously any service in
	// "unknown" silently rolled up to overall="ok" because we only
	// degraded inside the success branches of each loop. Now the
	// rollup explicitly handles the unknown floor — if every
	// service is unknown, overall is "unknown" (not "ok").
	backendErr := hbErr != nil || latErr != nil || freErr != nil || incErr != nil
	pageFiring := incErr == nil && incidents.PageCount > 0
	out.Overall = rollupOverall(out.Services, backendErr, pageFiring)

	// flags.stale reflects whether this envelope is reporting
	// non-fresh state — any non-"ok" overall trips it so polling
	// clients (status page, dashboards) can distinguish a healthy
	// snapshot from a degraded/unknown/down one without parsing
	// every per-service entry.
	writeJSON(w, out, Flags{Stale: out.Overall != "ok"})
}

// rollupOverall computes the customer-facing `overall` field from
// the per-service rollup plus the two cross-cutting signals
// (metrics-backend unreachable; page-severity alert firing).
//
// Precedence (worst wins):
//
//   - "down": any service is down.
//   - "degraded": any service is degraded, OR backendErr, OR
//     a page-severity alert is firing, OR services are in a mixed
//     known state (e.g. one ok + one unknown — partial visibility
//     is honest degradation, not "ok").
//   - "unknown": every service is unknown (or has a zero LastSeen).
//     Distinct from "down" — we have no signal at all, rather than
//     a definite negative one. F-0055: this branch was missing
//     pre-fix, which caused overall=ok during full metrics-backend
//     outages.
//   - "ok": every service is ok and no canary signal trips.
func rollupOverall(services []StatusService, backendErr, pageFiring bool) string {
	var anyDown, anyDegraded, anyOK, anyUnknown bool
	for _, svc := range services {
		switch svc.Status {
		case "down":
			anyDown = true
		case "degraded":
			anyDegraded = true
		case "ok":
			// A service self-reporting "ok" with a zero LastSeen
			// hasn't actually been observed — treat it as unknown for
			// rollup purposes. (The in-process "api" entry always
			// stamps LastSeen at request time so it's a real "ok".)
			if svc.LastSeen.IsZero() && svc.Name != "api" {
				anyUnknown = true
			} else {
				anyOK = true
			}
		default:
			anyUnknown = true
		}
	}

	switch {
	case anyDown:
		return "down"
	case anyDegraded, backendErr, pageFiring:
		return "degraded"
	case anyUnknown && !anyOK:
		return "unknown"
	case anyUnknown && anyOK:
		// Mixed known/unknown is partial visibility — surface as
		// degraded so callers can't mistake it for a full-green roll.
		return "degraded"
	default:
		return "ok"
	}
}
