package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds every metric the binary emits. Binaries expose
// it at /metrics via [Handler].
//
// We use a non-default registry (not prometheus.DefaultRegisterer)
// so tests can spin up isolated registries without state leakage +
// the default process/go collectors are opt-in here.
var Registry = prometheus.NewRegistry()

func init() {
	// Register language-native metrics — heap, goroutines,
	// gc pauses, open file descriptors, process uptime.
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// Register every application metric.
	Registry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,

		SourceEventsTotal,
		SourceLagLedgers,
		SourceLastEventUnix,
		SourceEnabled,
		SourceDecodeErrorsTotal,
		SourceOrphanEventsTotal,
		SourceInsertErrorsTotal,
		CursorLastLedger,

		PriceStalenessSeconds,
		OracleLastUpdateUnix,
		OracleResolutionSeconds,
	)
}

// Handler returns an http.Handler that serves Prometheus-formatted
// metrics from [Registry]. Binaries mount this at /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{
		// Compression removes redundant labels from the scrape body
		// at the cost of a tiny bit of CPU per scrape.
		EnableOpenMetrics: true,
	})
}

// ─── HTTP-layer metrics ──────────────────────────────────────────

// HTTPRequestsTotal — count of HTTP requests served, by method,
// route pattern (not raw URL — avoids cardinality blow-up on IDs),
// and status class.
//
// Alert rules reference this via `http_requests_total{status=~"5..", job="api"}`.
var HTTPRequestsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Count of HTTP requests served by the API, labelled by method, route pattern, and status.",
	},
	[]string{"method", "route", "status"},
)

// HTTPRequestDuration — histogram of request latency in seconds,
// from first byte in to last byte out.
//
// Buckets cover 1 ms → 10 s so the p95 ≤ 200 ms SLA target lands
// inside a bucket boundary (0.2) for accurate p95/p99 readouts.
var HTTPRequestDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "http_request_duration_seconds",
		Help: "Request latency histogram, labelled by method + route pattern.",
		// Matches Freighter SLA: p95 ≤ 200ms, p99 ≤ 500ms. Buckets
		// are picked so the .2 + .5 boundaries land exactly.
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5, 1, 2.5, 5, 10},
	},
	[]string{"method", "route"},
)

// ─── Ingestion-layer metrics ─────────────────────────────────────

// SourceEventsTotal — per-source event count; increments on every
// event the consumer emits to its out-channel.
var SourceEventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_source_events_total",
		Help: "Total events emitted by each ingestion source.",
	},
	[]string{"source"},
)

// SourceLagLedgers — per-source gauge, how many ledgers behind the
// network tip a source is. Zero at tip.
var SourceLagLedgers = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_source_lag_ledgers",
		Help: "How many ledgers behind the network tip each source currently is.",
	},
	[]string{"source"},
)

// SourceLastEventUnix — per-source gauge, Unix-epoch timestamp of
// the source's most recent observed event.
var SourceLastEventUnix = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_source_last_event_unix",
		Help: "Timestamp of the most recent event per source (Unix seconds).",
	},
	[]string{"source"},
)

// SourceEnabled — per-source 0/1 gauge indicating config-time
// enablement. Used by the "source_stopped" alert to qualify rate-
// zero with "but it was supposed to be running".
var SourceEnabled = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_source_enabled",
		Help: "1 if source is configured enabled; 0 otherwise.",
	},
	[]string{"source"},
)

// SourceDecodeErrorsTotal — per-source counter of decode failures
// (SCVal parse errors, malformed event schemas, etc.).
var SourceDecodeErrorsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_source_decode_errors_total",
		Help: "Events that failed to decode, per source.",
	},
	[]string{"source"},
)

// SourceOrphanEventsTotal — per-source counter of events that
// arrived but never correlated into a complete observation.
// Soroswap emits one per aged-out half of a swap/sync pair;
// Phoenix emits one per aged-out incomplete 8-field set.
//
// Distinct from SourceDecodeErrorsTotal because an orphan event
// was well-formed on its own — the surrounding context is what's
// missing. A sustained rate usually means the RPC is dropping
// events or the contract shape shifted.
var SourceOrphanEventsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_source_orphan_events_total",
		Help: "Events that arrived without their required correlation partner, per source.",
	},
	[]string{"source"},
)

// SourceInsertErrorsTotal — per-source counter of persistence
// failures (DB connection lost, constraint violation, etc.).
// Separate from decode errors because operators respond differently:
// decode errors mean the source schema drifted; insert errors mean
// the storage layer is struggling. kind="trade"|"oracle" lets
// dashboards split trade vs oracle-update writes.
var SourceInsertErrorsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_source_insert_errors_total",
		Help: "Events that failed to persist to the store, per source + kind (trade/oracle).",
	},
	[]string{"source", "kind"},
)

// CursorLastLedger — per-source gauge, the last-committed cursor
// value in the ingestion_cursors table. Used to detect stuck
// cursors (increase == 0 over time).
var CursorLastLedger = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_cursor_last_ledger",
		Help: "Last ledger committed to the per-source cursor.",
	},
	[]string{"source"},
)

// ─── Pricing / oracle metrics ────────────────────────────────────

// PriceStalenessSeconds — per-asset gauge showing how old our
// latest aggregated-price observation is. Alert fires when >120s.
var PriceStalenessSeconds = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_price_staleness_seconds",
		Help: "Age of the most recent aggregated price per asset (seconds).",
	},
	[]string{"asset"},
)

// OracleLastUpdateUnix — per-(source, asset) gauge with the Unix
// timestamp of the most recent oracle observation for that pair.
var OracleLastUpdateUnix = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_oracle_last_update_unix",
		Help: "Timestamp of the most recent oracle observation, per source and asset.",
	},
	[]string{"source", "asset"},
)

// OracleResolutionSeconds — per-source gauge of the oracle's
// declared resolution interval. Used by the oracle-stale alert
// to qualify "no update in > 10× resolution".
var OracleResolutionSeconds = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_oracle_resolution_seconds",
		Help: "Declared resolution interval of each oracle source (seconds).",
	},
	[]string{"source"},
)
