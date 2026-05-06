// Binary ratesengine-sla-probe is the executable SLA-evidence
// suite. It drives load against a deployed Rates Engine API and
// reports p50 / p95 / p99 latency per endpoint, freshness against
// the currently-observed ledger, and a pass/fail verdict against
// the RFP-stated SLA targets:
//
//	p95 ≤ 200 ms
//	p99 ≤ 500 ms
//	freshness ≤ 30 s   (Freighter RFP — price freshness)
//	availability ≥ 99.9 %  (sampled per-tick error rate)
//
// Closes Codex medium-7 / Task #52 / RFP coverage matrix rows
// S5.2, S9.1, S9.2, F3.1-F3.4. Provides the executable evidence
// the RFPs / proposal asked for; the rest of those rows (HA
// posture, SEV detection time) are operational SLAs that need a
// production deployment to measure, not a pre-launch CLI.
//
// Usage:
//
//	ratesengine-sla-probe \
//	    -base-url https://api.ratesengine.net/v1 \
//	    -duration 60s \
//	    -concurrency 4 \
//	    -pair native,fiat:USD \
//	    -pair USDC:GA5...,fiat:USD \
//	    -report-format json
//
// Output: a JSON report with per-endpoint statistics and overall
// pass/fail verdict. Exit code 0 = pass, 1 = at least one SLA
// violated. Designed for CI / scheduled-job integration so the
// SLA results trend over time rather than living in a one-off
// notebook.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/version"
)

// SLA targets — match the RFP-stated thresholds. Configurable via
// flags at runtime.
const (
	defaultP95Target     = 200 * time.Millisecond
	defaultP99Target     = 500 * time.Millisecond
	defaultFreshTarget   = 30 * time.Second
	defaultAvailabilityT = 99.9 // percent
)

// endpoint captures one API surface to probe. Path is the URL
// suffix appended to -base-url; the runner GETs it with the
// fixed query params (if any) and counts the HTTP status code
// against the SLA's success classes (2xx).
type endpoint struct {
	Name     string
	Path     string
	Query    map[string]string
	Critical bool // when true, a single failure here fails the whole run
}

// staticEndpoints are probed regardless of -pair flags — they
// have no per-pair variant. Health + version probes verify the
// process is responsive; the catalogue probes (/coins, /issuers,
// /markets, /diagnostics/cursors) verify that the read-heavy
// surfaces the explorer site fans out across hold latency under
// load. Without these, a regression on /v1/coins would only
// surface as "the explorer is slow" — well after the SLA probe
// gate would have caught it.
func staticEndpoints() []endpoint {
	return []endpoint{
		{Name: "healthz", Path: "/healthz", Critical: true},
		{Name: "readyz", Path: "/readyz", Critical: true},
		{Name: "version", Path: "/version"},
		{Name: "coins", Path: "/coins", Query: map[string]string{"limit": "100"}},
		{Name: "issuers", Path: "/issuers", Query: map[string]string{"limit": "100"}},
		{Name: "markets", Path: "/markets", Query: map[string]string{"limit": "100"}},
		{Name: "diagnostics-cursors", Path: "/diagnostics/cursors"},
	}
}

// pairEndpoints expands one (asset, quote) pair into the per-pair
// endpoints we measure: /v1/price and /v1/oracle/latest are the
// load-bearing customer surfaces; /v1/markets is included as a
// representative listing surface.
func pairEndpoints(asset, quote string) []endpoint {
	q := func(extra map[string]string) map[string]string {
		out := map[string]string{"asset": asset, "quote": quote}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	return []endpoint{
		{Name: "price", Path: "/price", Query: q(nil), Critical: true},
		{Name: "oracle-latest", Path: "/oracle/latest", Query: map[string]string{"asset": asset}},
	}
}

// stats holds per-endpoint sampling output.
type stats struct {
	Endpoint        string       `json:"endpoint"`
	Path            string       `json:"path"`
	Samples         int          `json:"samples"`
	Successes       int          `json:"successes"`
	Errors          int          `json:"errors"`
	AvailabilityPct float64      `json:"availability_pct"`
	LatencyMS       latencyStats `json:"latency_ms"`
	// ObservedAtFreshSec — for endpoints that return an observed_at
	// timestamp (price), the median freshness in seconds. Zero
	// when no observed_at field on this endpoint.
	ObservedAtFreshSec *float64 `json:"observed_at_fresh_sec,omitempty"`
}

type latencyStats struct {
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

// report is the top-level JSON output.
type report struct {
	BaseURL       string     `json:"base_url"`
	StartedAt     time.Time  `json:"started_at"`
	DurationSec   float64    `json:"duration_sec"`
	Concurrency   int        `json:"concurrency"`
	SLA           slaTargets `json:"sla"`
	PerEndpoint   []stats    `json:"per_endpoint"`
	Verdict       string     `json:"verdict"` // "pass" | "fail"
	FailedReasons []string   `json:"failed_reasons,omitempty"`
}

type slaTargets struct {
	P95MS           float64 `json:"p95_ms"`
	P99MS           float64 `json:"p99_ms"`
	FreshnessSec    float64 `json:"freshness_sec"`
	AvailabilityPct float64 `json:"availability_pct"`
}

func main() {
	// API key default falls through to RATESENGINE_PROBE_API_KEY so
	// the systemd unit can pass it via Environment= without leaking
	// it onto the command line (visible in ps).
	defaultAPIKey := os.Getenv("RATESENGINE_PROBE_API_KEY")

	var (
		baseURL      = flag.String("base-url", "http://localhost:3000/v1", "API base URL (required)")
		duration     = flag.Duration("duration", 30*time.Second, "Test duration")
		concurrency  = flag.Int("concurrency", 4, "Concurrent request workers")
		pairFlag     = stringSliceFlag{}
		reportFormat = flag.String("report-format", "text", "Output format: text | json")
		p95Target    = flag.Duration("p95-target", defaultP95Target, "p95 latency SLA target")
		p99Target    = flag.Duration("p99-target", defaultP99Target, "p99 latency SLA target")
		freshTarget  = flag.Duration("freshness-target", defaultFreshTarget, "Price-freshness SLA target")
		availTarget  = flag.Float64("availability-target", defaultAvailabilityT, "Per-endpoint availability SLA target (percent)")
		textfileOut  = flag.String("textfile-output", "", "Path to write Prometheus textfile (node_exporter textfile_collector format). Empty = no metrics emit.")
		apiKey       = flag.String("api-key", defaultAPIKey, "API key for Authorization: Bearer header. Defaults to $RATESENGINE_PROBE_API_KEY. Without one the probe hits the anonymous-tier rate limit (60 req/min) and reads as a fail.")
		showVersion  = flag.Bool("version", false, "Print version and exit")
	)
	flag.Var(&pairFlag, "pair", "Asset pair as 'asset,quote' (e.g. 'native,fiat:USD'). Repeatable.")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "ratesengine-sla-probe: -base-url is required")
		flag.Usage()
		os.Exit(2)
	}

	// Default pair if none supplied — XLM/USD is the headline
	// Stellar pair and a sensible smoke-test target.
	if len(pairFlag) == 0 {
		pairFlag = stringSliceFlag{"native,fiat:USD"}
	}

	endpoints := staticEndpoints()
	for _, p := range pairFlag {
		parts := strings.SplitN(p, ",", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "ratesengine-sla-probe: invalid -pair %q (want asset,quote)\n", p)
			os.Exit(2)
		}
		endpoints = append(endpoints, pairEndpoints(parts[0], parts[1])...)
	}

	rep := runProbe(*baseURL, *apiKey, endpoints, *duration, *concurrency, slaTargets{
		P95MS:           durationMS(*p95Target),
		P99MS:           durationMS(*p99Target),
		FreshnessSec:    freshTarget.Seconds(),
		AvailabilityPct: *availTarget,
	})

	switch *reportFormat {
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(rep)
	default:
		printText(os.Stdout, &rep)
	}

	if *textfileOut != "" {
		if err := writeTextfileAtomic(*textfileOut, &rep); err != nil {
			fmt.Fprintf(os.Stderr, "ratesengine-sla-probe: write textfile: %v\n", err)
			os.Exit(2)
		}
	}

	if rep.Verdict != "pass" {
		os.Exit(1)
	}
}

// probeSample is one observation: latency + success + (optional)
// observed_at parsed from the response body.
type probeSample struct {
	latency    time.Duration
	ok         bool
	observedAt time.Time
}

// runProbe drives `concurrency` workers against `endpoints` for
// `duration`, then aggregates per-endpoint stats and produces a
// pass/fail report. apiKey, when non-empty, is sent as
// `Authorization: Bearer <key>` on every request — without one the
// probe hits the anonymous-tier rate limit and the verdict reads as
// fail for reasons unrelated to actual SLA compliance.
func runProbe(baseURL, apiKey string, endpoints []endpoint, duration time.Duration, concurrency int, sla slaTargets) report {
	started := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	samples := collectSamples(ctx, baseURL, apiKey, endpoints, concurrency)

	rep := report{
		BaseURL:     baseURL,
		StartedAt:   started,
		DurationSec: time.Since(started).Seconds(),
		Concurrency: concurrency,
		SLA:         sla,
	}
	for _, ep := range endpoints {
		rep.PerEndpoint = append(rep.PerEndpoint, aggregateEndpointStats(ep, samples[ep.Name]))
	}
	computeVerdict(&rep, sla)
	return rep
}

// collectSamples spawns `concurrency` workers that round-robin
// across `endpoints` until ctx expires. Returns a per-endpoint-name
// sample slice.
func collectSamples(ctx context.Context, baseURL, apiKey string, endpoints []endpoint, concurrency int) map[string][]probeSample {
	var mu sync.Mutex
	samples := make(map[string][]probeSample)
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for w := 0; w < concurrency; w++ {
		go func(workerID int) {
			defer wg.Done()
			i := workerID % len(endpoints)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				ep := endpoints[i]
				i = (i + 1) % len(endpoints)
				lat, ok, observedAt := hit(ctx, httpClient, baseURL, apiKey, ep)
				mu.Lock()
				samples[ep.Name] = append(samples[ep.Name], probeSample{lat, ok, observedAt})
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	return samples
}

// aggregateEndpointStats reduces a slice of samples into one stats
// row.
func aggregateEndpointStats(ep endpoint, ss []probeSample) stats {
	if len(ss) == 0 {
		return stats{Endpoint: ep.Name, Path: ep.Path}
	}
	latencies := make([]float64, len(ss))
	var freshSamples []float64
	successes := 0
	for i, s := range ss {
		latencies[i] = float64(s.latency.Milliseconds())
		if s.ok {
			successes++
		}
		if !s.observedAt.IsZero() {
			freshSamples = append(freshSamples, time.Since(s.observedAt).Seconds())
		}
	}
	st := stats{
		Endpoint:        ep.Name,
		Path:            ep.Path,
		Samples:         len(ss),
		Successes:       successes,
		Errors:          len(ss) - successes,
		AvailabilityPct: 100.0 * float64(successes) / float64(len(ss)),
		LatencyMS: latencyStats{
			P50:  percentile(latencies, 0.50),
			P95:  percentile(latencies, 0.95),
			P99:  percentile(latencies, 0.99),
			Max:  maxFloat(latencies),
			Mean: meanFloat(latencies),
		},
	}
	if len(freshSamples) > 0 {
		med := percentile(freshSamples, 0.50)
		st.ObservedAtFreshSec = &med
	}
	return st
}

// computeVerdict scans rep.PerEndpoint against sla and fills
// rep.Verdict + rep.FailedReasons.
func computeVerdict(rep *report, sla slaTargets) {
	rep.Verdict = "pass"
	for _, st := range rep.PerEndpoint {
		rep.FailedReasons = append(rep.FailedReasons, endpointFailures(st, sla)...)
	}
	if len(rep.FailedReasons) > 0 {
		rep.Verdict = "fail"
	}
}

// endpointFailures returns the human-readable SLA-violation strings
// for one endpoint. Empty slice = endpoint passes.
func endpointFailures(st stats, sla slaTargets) []string {
	if st.Samples == 0 {
		return []string{fmt.Sprintf("%s: no samples", st.Endpoint)}
	}
	var out []string
	if st.LatencyMS.P95 > sla.P95MS {
		out = append(out, fmt.Sprintf("%s: p95=%.1fms > target %.1fms", st.Endpoint, st.LatencyMS.P95, sla.P95MS))
	}
	if st.LatencyMS.P99 > sla.P99MS {
		out = append(out, fmt.Sprintf("%s: p99=%.1fms > target %.1fms", st.Endpoint, st.LatencyMS.P99, sla.P99MS))
	}
	if st.AvailabilityPct < sla.AvailabilityPct {
		out = append(out, fmt.Sprintf("%s: availability=%.2f%% < target %.2f%%", st.Endpoint, st.AvailabilityPct, sla.AvailabilityPct))
	}
	if st.ObservedAtFreshSec != nil && *st.ObservedAtFreshSec > sla.FreshnessSec {
		out = append(out, fmt.Sprintf("%s: freshness=%.1fs > target %.1fs", st.Endpoint, *st.ObservedAtFreshSec, sla.FreshnessSec))
	}
	return out
}

// hit issues one GET to `<baseURL><path>?<query>` and returns the
// wall-clock latency, success boolean (2xx), and the parsed
// observed_at timestamp from the response body when present. apiKey,
// when non-empty, is sent as `Authorization: Bearer <key>`.
func hit(ctx context.Context, c *http.Client, baseURL, apiKey string, ep endpoint) (time.Duration, bool, time.Time) {
	u := baseURL + ep.Path
	if len(ep.Query) > 0 {
		var parts []string
		for k, v := range ep.Query {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
		sort.Strings(parts)
		u = u + "?" + strings.Join(parts, "&")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return 0, false, time.Time{}
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	start := time.Now()
	resp, err := c.Do(req)
	lat := time.Since(start)
	if err != nil {
		return lat, false, time.Time{}
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok {
		return lat, false, time.Time{}
	}
	// Try to parse observed_at — only the price endpoint has it.
	var env struct {
		Data struct {
			ObservedAt time.Time `json:"observed_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		return lat, true, env.Data.ObservedAt
	}
	return lat, true, time.Time{}
}

// percentile returns the p-th percentile (0..1) of xs using
// linear interpolation between rank-positions. Mutates xs (sorts
// in place); pass a copy if the caller needs to preserve order.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	if len(xs) == 1 {
		return xs[0]
	}
	rank := p * float64(len(xs)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return xs[lo]
	}
	weight := rank - float64(lo)
	return xs[lo]*(1-weight) + xs[hi]*weight
}

func maxFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := xs[0]
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func meanFloat(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func durationMS(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

// printText renders the report as a human-readable summary —
// useful for ad-hoc CLI runs without a JSON consumer.
func printText(w io.Writer, rep *report) {
	fmt.Fprintf(w, "ratesengine-sla-probe — %s\n", rep.BaseURL)
	fmt.Fprintf(w, "  duration: %.1fs   concurrency: %d   started: %s\n",
		rep.DurationSec, rep.Concurrency, rep.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "  SLA: p95<=%vms p99<=%vms fresh<=%vs avail>=%v%%\n\n",
		rep.SLA.P95MS, rep.SLA.P99MS, rep.SLA.FreshnessSec, rep.SLA.AvailabilityPct)
	fmt.Fprintf(w, "%-15s %-25s %7s %7s %7s %7s %9s\n",
		"endpoint", "path", "p50ms", "p95ms", "p99ms", "avail%", "fresh-s")
	for _, st := range rep.PerEndpoint {
		fresh := "—"
		if st.ObservedAtFreshSec != nil {
			fresh = fmt.Sprintf("%.1f", *st.ObservedAtFreshSec)
		}
		fmt.Fprintf(w, "%-15s %-25s %7.1f %7.1f %7.1f %6.2f%% %9s\n",
			st.Endpoint, st.Path,
			st.LatencyMS.P50, st.LatencyMS.P95, st.LatencyMS.P99,
			st.AvailabilityPct, fresh)
	}
	fmt.Fprintf(w, "\nverdict: %s\n", rep.Verdict)
	if len(rep.FailedReasons) > 0 {
		fmt.Fprintln(w, "failed:")
		for _, r := range rep.FailedReasons {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
}

// stringSliceFlag is the standard Go pattern for repeatable
// flags: each `-pair foo,bar` appends one entry.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}
