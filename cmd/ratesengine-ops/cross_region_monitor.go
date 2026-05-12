package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ─── cross-region-monitor ──────────────────────────────────────────
//
// Runs cross-region-check on a fixed interval, exporting Prometheus
// metrics on a configurable port instead of writing to stdout. Same
// per-region fetch + per-bucket analysis code path as the one-shot
// check; only the outer loop and the output sink differ.
//
// The intended deployment is a sidecar systemd service on the
// observability host:
//
//   ratesengine-ops cross-region-monitor \
//     -regions r1=https://r1.api...,r2=https://r2.api...,r3=https://r3.api... \
//     -pairs native/fiat:USD \
//     -metric vwap \
//     -interval 60s \
//     -listen :9479
//
// Then a Grafana panel scrapes :9479/metrics. Alertmanager fires on:
//
//   rate(ratesengine_cross_region_divergences_total[10m]) > 0
//   rate(ratesengine_cross_region_fetch_errors_total[5m])  > 0.1
//
// (Concrete alerts land with docs/operations/runbooks/ when the
// observability box is provisioned.)

func crossRegionMonitor(args []string) error { //nolint:funlen,gocognit,gocyclo // single-purpose long-running loop
	fs := flag.NewFlagSet("cross-region-monitor", flag.ContinueOnError)
	regionsCSV := fs.String("regions", "",
		"Comma-separated `name=url` pairs (required). Same format as cross-region-check.")
	pairsCSV := fs.String("pairs", "native/fiat:USD",
		"Comma-separated canonical pair strings to check")
	metric := fs.String("metric", "vwap",
		"Which endpoint to compare across regions: vwap, twap, ohlc")
	window := fs.Duration("window", 30*time.Second,
		"Closed-bucket window size (matches CAGG bucket size)")
	samples := fs.Int("samples", 1,
		"How many recent closed buckets to compare per tick. 1 is enough for steady-state monitoring; bump for noisier alerts.")
	timeout := fs.Duration("timeout", 10*time.Second,
		"Per-region HTTP request timeout")
	interval := fs.Duration("interval", 60*time.Second,
		"How often to run a check. Shouldn't be smaller than window — there's only one new closed bucket per window.")
	listen := fs.String("listen", ":9479",
		"HTTP listen address for /metrics + /healthz")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *regionsCSV == "" {
		return errors.New("-regions is required")
	}
	if *samples < 1 {
		return errors.New("-samples must be >= 1")
	}
	if *interval < time.Second {
		return errors.New("-interval must be >= 1s (don't hammer regions)")
	}

	regions, err := parseRegionList(*regionsCSV)
	if err != nil {
		return err
	}
	if len(regions) < 2 {
		// F-1234 (audit-2026-05-12): pre-R2/R3-bringup posture has
		// only R1 deployed. Refusing to start trains operators to
		// disable the monitor; instead log + exit cleanly so a
		// systemd unit wrapper can stay disabled until R2/R3 land
		// and a healthy `systemctl status` reports the reason.
		_, _ = fmt.Fprintf(os.Stdout,
			"cross-region-monitor: only %d region configured; needs ≥ 2 to compare.\n"+
				"This is the pre-launch posture (R2/R3 not yet deployed). Exiting cleanly.\n"+
				"Track R2/R3 bringup in docs/architecture/r2-r3-bringup.md.\n",
			len(regions))
		return nil
	}

	pairs := splitCSV(*pairsCSV)
	if len(pairs) == 0 {
		return errors.New("-pairs parsed to empty list")
	}

	metricKind := crossRegionMetric(strings.ToLower(*metric))
	switch metricKind {
	case metricVWAP, metricTWAP, metricOHLC:
	default:
		return fmt.Errorf("-metric must be one of vwap|twap|ohlc; got %q", *metric)
	}

	// Build a private Prometheus registry so we don't share with the
	// indexer/api process metrics that internal/obs registers at init.
	reg := prometheus.NewRegistry()
	exp := newCrossRegionExporter(reg)

	httpClient := &http.Client{Timeout: *timeout}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run an immediate first check before serving so the first scrape
	// has fresh data instead of zero gauges.
	runOneTick(ctx, httpClient, regions, pairs, metricKind, *window, *samples, exp)

	// Background tick loop.
	go func() {
		ticker := time.NewTicker(*interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOneTick(ctx, httpClient, regions, pairs, metricKind, *window, *samples, exp)
			}
		}
	}()

	// HTTP server with /metrics + /healthz.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Healthy = the loop has run at least once. Sets aside the
		// ambiguity of "we just started, no data yet" vs "we've been
		// up for hours but haven't run a check".
		if exp.lastRunUnix.Load() == 0 {
			http.Error(w, "no checks completed yet", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	_, _ = fmt.Fprintf(os.Stderr,
		"cross-region-monitor: listening on %s; checking %d region(s) × %d pair(s) every %s\n",
		*listen, len(regions), len(pairs), *interval)
	return srv.ListenAndServe()
}

// runOneTick does a single sweep over (pairs × samples), updates the
// metrics, and returns. Errors are recorded in counters, not propagated.
func runOneTick(
	ctx context.Context,
	client *http.Client,
	regions []regionEndpoint,
	pairs []string,
	metric crossRegionMetric,
	window time.Duration,
	samples int,
	exp *crossRegionExporter,
) {
	exp.inFlight.Inc()
	defer exp.inFlight.Dec()

	anchor, err := resolveAnchor("", window)
	if err != nil {
		exp.checkErrors.WithLabelValues(metric.String(), err.Error()).Inc()
		return
	}

	for _, pair := range pairs {
		for i := 0; i < samples; i++ {
			bucketTo := anchor.Add(-time.Duration(i) * window)
			bucketFrom := bucketTo.Add(-window)

			results := fetchAllRegions(ctx, client,
				regions, pair, metric, bucketFrom, bucketTo)

			// Account for fetch failures per region.
			for _, r := range results {
				if r.Err != nil {
					exp.fetchErrors.WithLabelValues(r.Region, pair, metric.String()).Inc()
				}
			}

			// Run the analysis but discard its stdout output — we
			// only need the bool. The diff itself is already in the
			// metrics labels (region pair) for ops to triage.
			divergence := analyseRegionResults(metric, pair, bucketFrom, bucketTo, results, io.Discard)

			outcome := "ok"
			if divergence {
				outcome = "divergence"
				exp.divergences.WithLabelValues(pair, metric.String()).Inc()
			}
			// "error" outcome only if EVERY region failed; partial
			// failures are tracked separately via fetchErrors.
			if allFailed(results) {
				outcome = "error"
			}
			exp.checksTotal.WithLabelValues(pair, metric.String(), outcome).Inc()
		}
	}

	exp.lastRunUnix.Store(time.Now().Unix())
	exp.lastRunGauge.SetToCurrentTime()
}

// allFailed reports whether every region's fetch returned an error.
// Used to flag the "every region is unreachable" alert as distinct
// from "we got data and it diverged".
func allFailed(results []regionResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Err == nil {
			return false
		}
	}
	return true
}

// crossRegionExporter holds the metric vectors. One instance per
// process; passed by pointer to runOneTick.
type crossRegionExporter struct {
	checksTotal  *prometheus.CounterVec
	divergences  *prometheus.CounterVec
	fetchErrors  *prometheus.CounterVec
	checkErrors  *prometheus.CounterVec
	lastRunGauge prometheus.Gauge
	inFlight     prometheus.Gauge

	// lastRunUnix is the wall clock of the last completed sweep.
	// Used by /healthz; the gauge above is for Prometheus.
	lastRunUnix atomic.Int64
}

func newCrossRegionExporter(reg prometheus.Registerer) *crossRegionExporter {
	exp := &crossRegionExporter{
		checksTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ratesengine_cross_region_checks_total",
				Help: "Count of cross-region check sweeps, labelled by pair, metric, and outcome (ok | divergence | error).",
			},
			[]string{"pair", "metric", "outcome"},
		),
		divergences: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ratesengine_cross_region_divergences_total",
				Help: "Count of buckets where regions disagreed. Alert when rate > 0 for >5m.",
			},
			[]string{"pair", "metric"},
		),
		fetchErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ratesengine_cross_region_fetch_errors_total",
				Help: "Per-region HTTP fetch failures (timeout, 5xx, parse error).",
			},
			[]string{"region", "pair", "metric"},
		),
		checkErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ratesengine_cross_region_check_errors_total",
				Help: "Internal errors in the monitor loop (e.g. anchor resolution failed).",
			},
			[]string{"metric", "reason"},
		),
		lastRunGauge: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ratesengine_cross_region_last_run_timestamp_seconds",
				Help: "Unix time of the last completed check sweep.",
			},
		),
		inFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ratesengine_cross_region_check_in_flight",
				Help: "1 while a check sweep is running, 0 otherwise. Persistent 1 indicates the loop is stuck.",
			},
		),
	}
	reg.MustRegister(
		exp.checksTotal,
		exp.divergences,
		exp.fetchErrors,
		exp.checkErrors,
		exp.lastRunGauge,
		exp.inFlight,
	)
	return exp
}

// String makes crossRegionMetric usable as a Prometheus label value
// without a type assertion every call site.
func (m crossRegionMetric) String() string { return string(m) }
