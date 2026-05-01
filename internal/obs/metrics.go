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
		DiscoveryDroppedHitsTotal,
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
		AggregatorDroppedWindowsTotal,

		SupplyCrossCheckDivergenceStroops,
		SupplyCrossCheckTotal,

		AnomalyFreezeEngagedTotal,
		AggregatorTriangulationsTotal,
		AggregatorFXSnapFallbackTotal,
		AggregatorBaselineRefreshTotal,
		AggregatorSupplyRefreshTotal,
		AggregatorConfidenceComputeTotal,

		VerifyArchiveLedgersVerified,
		VerifyArchiveCurrentLedger,
		VerifyArchiveCheckpointsTotal,
		VerifyArchiveMismatchesTotal,
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

// DiscoveryDroppedHitsTotal — count of SEP-41 discovery hits that
// were dropped because the async sink buffer was full. Discovery is
// intentionally best-effort, but operators still need a live signal
// when the buffer starts shedding records under write pressure.
var DiscoveryDroppedHitsTotal = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "ratesengine_discovery_dropped_hits_total",
		Help: "Discovery hits dropped because the async discovery sink buffer was full.",
	},
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
// docs/architecture/aggregation-plan.md.
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

// AggregatorDroppedWindowsTotal — count of (pair, window) refreshes
// where the post-class + post-outlier trade set was non-empty but
// the window was suppressed by a window-level filter. Labelled by
// reason: "min_usd_volume" = window's total USD-equivalent volume
// fell below `aggregate.min_usd_volume`. Distinct from
// AggregatorEmptyWindowsTotal (which means literally zero trades);
// drives the launch-readiness L2.1 caveat audit.
var AggregatorDroppedWindowsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_dropped_windows_total",
		Help: "Windows the orchestrator suppressed at the window-level filter step, labelled by reason (min_usd_volume).",
	},
	[]string{"reason"},
)

// ─── Supply-derivation metrics ────────────────────────────────────

// SupplyCrossCheckDivergenceStroops — gauge of the absolute stroop
// difference between a classic asset's Algorithm 2 supply and its
// SAC-wrapped Algorithm 3 supply. Per ADR-0011 the two MUST agree
// within 1 stroop; the alert in
// deploy/monitoring/rules/supply.yml fires when this exceeds the
// tolerance.
//
// Labelled by classic_key (CODE:ISSUER) so a per-asset dashboard +
// runbook can identify the offending asset without log dive. Cardinality
// bound by the curated asset set with deployed SAC contracts (low
// dozens at launch, hundreds at maturity).
var SupplyCrossCheckDivergenceStroops = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_supply_cross_check_divergence_stroops",
		Help: "Absolute stroop difference between classic and SAC-wrapped supply for the same asset; alert when > 1 (ADR-0011).",
	},
	[]string{"classic_key"},
)

// SupplyCrossCheckTotal — counter of cross-check evaluations per
// outcome (within | over). Drives the alert's rate-of-failure view
// and gives operators a "is the cross-checker even running" check
// orthogonal to the gauge.
var SupplyCrossCheckTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_supply_cross_check_total",
		Help: "Cross-check evaluations, labelled by outcome (within|over).",
	},
	[]string{"outcome"},
)

// ─── verify-archive metrics ───────────────────────────────────────
//
// Emitted by `ratesengine-ops verify-archive` when the operator
// passes -metrics-listen ADDR. One-shot diagnostic command, but the
// run can take hours on full pubnet sweeps — live metrics let
// operators dashboard the bottleneck during the run rather than
// guessing from log tails.
//
// All vectors labelled by chunk_idx (decimal string) so a parallel
// run with -workers 8 produces per-chunk series. Cardinality bound
// by the -workers cap (currently [1,16]).

// AnomalyFreezeEngagedTotal — counter of ActionFreeze decisions
// the aggregator's anomaly checker emitted, labelled by the asset
// class that drove the threshold lookup. Each increment means the
// orchestrator declined to publish a fresh VWAP (kept the prior
// bucket's last-known-good value); the API's /v1/price for the
// affected pair will surface flags.frozen=true on the next read.
//
// Pair-specific freeze details live in the freeze marker JSON
// (deviation_pct, reason) — labelled by class only here so
// cardinality stays bound to the small AssetClass enum.
var AnomalyFreezeEngagedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_anomaly_freeze_engaged_total",
		Help: "ActionFreeze decisions emitted by the aggregator anomaly checker, labelled by asset class.",
	},
	[]string{"class"},
)

// AggregatorTriangulationsTotal — counter of triangulation
// computations per outcome. The aggregator runs one row per
// (chain, window) per tick; steady state is mostly `ok` with
// periodic `missing_leg` entries when a leg's window was empty
// this tick. Sustained `parse_error` or `redis_error` rates >
// baseline indicate upstream regression worth investigating.
var AggregatorTriangulationsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_triangulations_total",
		Help: "Aggregator triangulation outcomes per tick × chain × window. Outcome ∈ {ok, missing_leg, parse_error, redis_error}.",
	},
	[]string{"outcome"},
)

// AggregatorFXSnapFallbackTotal — counter of triangulation legs that
// fell back from the X2.5 forex-snap rule to the cached-VWAP path
// because FXQuoteAtOrBefore returned no row at-or-before the bucket
// end. Steady state should be near-zero once FX ingestion is warm.
// Sustained > 50% of triangulations indicates an FX-source health
// issue (polygon-forex / exchangeratesapi) — see the matching alert
// in deploy/monitoring/rules/aggregator.yml.
//
// Label `leg` is the canonical pair string of the FX leg that fell
// back (e.g. "fiat:USD/fiat:EUR"); cardinality is bounded by the
// operator-configured triangulation chain set.
var AggregatorFXSnapFallbackTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_fx_snap_fallback_total",
		Help: "Triangulations that fell back to cached VWAP for an FX leg because FXQuoteAtOrBefore returned no row at-or-before the bucket end.",
	},
	[]string{"leg"},
)

// AggregatorBaselineRefreshTotal — counter of baseline refresh
// outcomes per pair, per refresh cycle (ADR-0019 Phase 2). One
// increment per pair per cycle; outcome ∈ {ok, not_enough_samples,
// read_error, write_error}. Steady state is mostly `ok`; sustained
// `not_enough_samples` indicates pairs in bootstrap (ADR-0019
// §"Bootstrap policy"); sustained `read_error` / `write_error`
// indicate the storage layer needs investigation.
var AggregatorBaselineRefreshTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_baseline_refresh_total",
		Help: "Baseline refresh outcomes per pair × refresh cycle. Outcome ∈ {ok, not_enough_samples, read_error, write_error}.",
	},
	[]string{"outcome"},
)

// AggregatorSupplyRefreshTotal — counter of supply-snapshot refresh
// outcomes per cycle (ADR-0011 / ADR-0021 / ADR-0022 / ADR-0023).
// One increment per (asset, tick); labels:
//
//   - asset_key: supply.AssetKey form ("XLM", "CODE:ISSUER" for
//     classic credits, the bare contract C-strkey for SEP-41).
//   - outcome ∈ {ok, no_ledger, no_observation, compute_error,
//     write_error}.
//
// Steady-state is mostly `ok` per asset. Sustained `no_observation`
// means the AccountEntry observer hasn't backfilled the watched
// accounts yet (the chain-reader fell through to static config and
// that also missed) — expected briefly post-deploy, alarming
// sustained. Per-asset rates let operators chart bootstrap
// progress per watched asset rather than as one aggregate.
var AggregatorSupplyRefreshTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_supply_refresh_total",
		Help: "Supply-snapshot refresh outcomes per (asset_key, outcome). Outcome ∈ {ok, no_ledger, no_observation, compute_error, write_error}.",
	},
	[]string{"asset_key", "outcome"},
)

// AggregatorConfidenceComputeTotal — counter of confidence-score
// compute outcomes per (pair, window) per tick (ADR-0019 §"Multi-
// factor confidence score"). Outcome labels:
//
//   - ok                       — score computed + cached cleanly
//   - skipped                  — first-tick / no prev-VWAP comparator
//   - baseline_missing         — MultiBaseline absent or in full bootstrap
//   - marshal_error            — score JSON encode failed (unreachable in practice)
//   - write_error              — Redis write of confidence: key failed
//   - divergence_read_error    — Redis Get on div:<asset> errored (best-effort; sentinel passed)
//   - divergence_decode_error  — div:<asset> JSON decode failed
//
// `skipped` and `baseline_missing` are normal during pair bring-up;
// `ok` should dominate in steady state. divergence_* errors are
// non-fatal (the confidence step continues with the "no data"
// sentinel) but sustained rates indicate the divergence worker /
// Redis is misbehaving.
var AggregatorConfidenceComputeTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_aggregator_confidence_compute_total",
		Help: "Confidence-score compute outcomes per (pair, window) × tick. See package docs for the full label vocabulary.",
	},
	[]string{"outcome"},
)

// VerifyArchiveLedgersVerified — counter of ledgers successfully
// walked + verified. Rate over time gives ledgers/sec per chunk —
// the primary signal for spotting a stalled chunk vs a slow one.
var VerifyArchiveLedgersVerified = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_verify_archive_ledgers_verified_total",
		Help: "Ledgers walked + verified by verify-archive, per chunk_idx.",
	},
	[]string{"chunk_idx"},
)

// VerifyArchiveCurrentLedger — gauge of the most-recent ledger
// position per chunk. Together with the chunk's [from,to] range
// (operator-known) gives a percent-complete view; together across
// chunks gives a ledger-distance-fan picture of which chunks are
// leading vs trailing.
var VerifyArchiveCurrentLedger = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "ratesengine_verify_archive_current_ledger",
		Help: "Most-recent ledger sequence verified by each chunk_idx.",
	},
	[]string{"chunk_idx"},
)

// VerifyArchiveCheckpointsTotal — counter of Tier B checkpoint
// outcomes (matched | missed). missed=archive file absent (warning
// or hard fail under -fail-on-missed); matched=hash-equal proof.
var VerifyArchiveCheckpointsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_verify_archive_checkpoints_total",
		Help: "Tier B checkpoint outcomes per verify-archive chunk_idx, labelled by outcome (matched|missed).",
	},
	[]string{"chunk_idx", "outcome"},
)

// VerifyArchiveMismatchesTotal — counter of chain-break /
// checkpoint-mismatch / sequence-gap incidents. Any non-zero
// reading is a hard failure; the counter exists so dashboards can
// distinguish "mismatch fired and the run aborted at second X"
// from "chunk aborted for an unrelated reason (canceled context)".
var VerifyArchiveMismatchesTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "ratesengine_verify_archive_mismatches_total",
		Help: "Chain breaks, sequence gaps, and checkpoint mismatches per verify-archive chunk_idx + reason (chain|sequence|checkpoint).",
	},
	[]string{"chunk_idx", "reason"},
)
