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
		RateLimitFailOpenTotal,
		Sep1CacheOpsTotal,
		CursorLastLedger,

		PriceStalenessSeconds,
		OracleLastUpdateUnix,
		OracleResolutionSeconds,

		AggregatorTicksTotal,
		AggregatorVWAPWritesTotal,
		AggregatorEmptyWindowsTotal,
		AggregatorDroppedTradesTotal,
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

// Sep1CacheOpsTotal — per-outcome counter for SEP-1 cache
// operations. Label `result` is one of:
//
//	hit         — served from cache.
//	miss        — fetched upstream + cached.
//	upstream_error — upstream fetch failed; not cached (see ADR).
//
// A rising `upstream_error` rate usually means an issuer's
// stellar.toml is down; a very low hit rate means the TTL is too
// short or the caller distribution is too dispersed for caching
// to help.
var Sep1CacheOpsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_sep1_cache_ops_total",
		Help: "SEP-1 resolver cache operations by outcome.",
	},
	[]string{"result"},
)

// RateLimitFailOpenTotal — counter of requests that skipped the
// rate-limit check because of a backing-store (Redis) error. The
// middleware fails-open on error so a Redis outage doesn't take
// the whole API down, but operators need a quantitative signal of
// how often it's happening. A spike here usually correlates with
// the redis readyz probe turning red.
var RateLimitFailOpenTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "ratesengine_ratelimit_fail_open_total",
		Help: "Requests that bypassed rate-limiting because Redis errored.",
	},
)

// SourceInsertErrorsTotal — per-source counter of persistence
// failures (DB connection lost, constraint violation, etc.).
// Separate from decode errors because operators respond differently:
// decode errors mean the source schema drifted; insert errors mean
// the storage layer is struggling. kind="trade"|"oracle"|"panic"|
// "unhandled" lets dashboards split trade vs oracle-update writes,
// flag recovered sink panics distinctly from storage-layer rejects,
// and surface half-wired sources whose event type the sink's
// type-switch doesn't recognise.
var SourceInsertErrorsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_source_insert_errors_total",
		Help: "Events that failed to persist to the store, per source + kind (trade/oracle/panic).",
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
//
// CARDINALITY WARNING: Stellar has tens of thousands of classic
// assets. Writers MUST restrict emission to an allow-list (top-N
// by volume, per-asset-quality tier, or similar) — never emit for
// every asset seen. Prometheus recommends <10^4 series per metric;
// unrestricted per-asset emission blows past that on a busy chain.
// The aggregator owns this allow-list; see
// docs/architecture/aggregation-plan.md when it lands.
var PriceStalenessSeconds = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_price_staleness_seconds",
		Help: "Age of the most recent aggregated price per asset (seconds). Writers MUST restrict to a top-N allow-list.",
	},
	[]string{"asset"},
)

// OracleLastUpdateUnix — per-(source, asset) gauge with the Unix
// timestamp of the most recent oracle observation for that pair.
//
// Cardinality: Reflector/Band/Redstone each track O(30) assets, so
// the shipped sources together stay well inside Prometheus's
// comfort zone. If we ever wire a "passthrough every asset"
// oracle, revisit — this would need the same allow-list discipline
// as PriceStalenessSeconds.
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

// ─── Aggregator orchestrator metrics ─────────────────────────────

// AggregatorTicksTotal — count of orchestrator ticks completed,
// labelled by outcome ("ok" when the tick ran without surfacing an
// error, "error" when at least one (pair, window) refresh failed).
// Per-pair errors are still recorded as soft warnings; this counter
// is the tick-level rollup.
var AggregatorTicksTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_ticks_total",
		Help: "Aggregator orchestrator tick count, labelled by outcome (ok|error).",
	},
	[]string{"outcome"},
)

// AggregatorVWAPWritesTotal — count of (pair, window) Redis writes
// performed by the orchestrator. Unlabelled to keep cardinality
// bounded — the per-pair lens lives in the Redis key namespace.
var AggregatorVWAPWritesTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_vwap_writes_total",
		Help: "Cumulative VWAP cache writes performed by the aggregator.",
	},
)

// AggregatorEmptyWindowsTotal — count of (pair, window) refreshes
// that produced zero VWAP-eligible trades after class filtering /
// stablecoin expansion / outlier filtering. Unlabelled for the same
// reason as VWAPWritesTotal.
var AggregatorEmptyWindowsTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_empty_windows_total",
		Help: "Aggregator (pair, window) refreshes that produced zero eligible trades.",
	},
)

// AggregatorDroppedTradesTotal — count of trades the orchestrator
// removed from the VWAP input set, labelled by reason. "class" =
// removed by the ClassExchange-only filter; "outlier" = removed by
// the σ-threshold filter. Operators alert on a sudden spike in
// "class" (a new venue mis-registered) or "outlier" (a market in
// distress flooding the window with anomalies).
var AggregatorDroppedTradesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_dropped_trades_total",
		Help: "Trades removed from the VWAP input set, labelled by reason (class|outlier).",
	},
	[]string{"reason"},
)
