# Metrics Reference

Every metric the Rates Engine binaries emit, with its labels, type,
and purpose. Lint `scripts/ci/lint-docs.sh` section 3 enforces
round-trip: any metric declared in `internal/obs/metrics.go` MUST
appear here, and vice versa.

Declaration source of truth: `internal/obs/metrics.go`.
Emission sites: `grep -rn <metric_name> internal/ cmd/`.

## HTTP layer (emitted by the API binary only)

The indexer also exposes an HTTP mux (for `/metrics` + `/healthz`)
but deliberately does NOT wrap it with `obs.HTTPMetrics`
middleware — every Prometheus scrape would otherwise inflate
`http_requests_total`. These counters reflect only the public API
request path.

### `http_requests_total`

Counter, labels `method`, `route`, `status`.

Counts every request served by `obs.HTTPMetrics` middleware. `method`
is canonicalised via `normalizeMethod` (uppercase-only for standard
verbs to bound cardinality). `route` is the Go 1.22 pattern path with
the method prefix stripped, or `"unmatched"` for 404s. `status` is
numeric; `"499"` is NGINX's "client closed request" — emitted when
the caller's ctx cancelled before the handler wrote.

### `http_request_duration_seconds`

Histogram, labels `method`, `route`.

Handler latency including time-in-middleware. Buckets 1ms – 10s with
extra resolution at the 200ms / 500ms SLO boundaries.

## Ingestion (indexer binary)

### `ratesengine_source_events_total`

Counter, label `source`.

Every event the live indexer sink attempts to persist for that
source. Emitted from `internal/pipeline/sink.go`, not the retired
legacy orchestrator path. Zero rate + `source_enabled=1` backs the
`source-stopped` alert.

### `ratesengine_source_enabled`

Gauge, label `source`.

`1` for sources the current indexer enabled from config at startup;
`0` during shutdown or when the source is not configured. Used to
qualify source-level alerts so intentionally disabled sources do not
page.

### `ratesengine_source_lag_ledgers`

Gauge, label `source`.

Legacy metric from the pre-dispatcher orchestrator topology. The
current `ledgerstream -> dispatcher` indexer does not emit this gauge,
so no live alert should depend on it until a replacement per-source
lag signal exists.

### `ratesengine_source_last_event_unix`

Gauge, label `source`. Unix-seconds timestamp of the most recent
event dispatched to the sink. Dashboards use it for a last-seen clock.

### `ratesengine_source_decode_errors_total`

Counter, label `source`.

Per-event parse failures — SCVal shape mismatch, malformed XDR,
canonical-invariant violations. Distinct from `orphan_events`
(events were well-formed but partnerless) and `insert_errors`
(decoded fine but persistence broke). Emitted from dispatcher stats
deltas after each processed ledger.

### `ratesengine_source_orphan_events_total`

Counter, label `source`.

Events that arrived but never correlated into a complete observation.
Soroswap: swap without matching sync (or vice versa). Phoenix:
incomplete N-of-8 field set aged past the buffer's 5-min ceiling.
Aquarius / Reflector don't emit orphans — they're 1-event-per-
observation. Emitted from decoder-maintained orphan counters via the
live dispatcher path.

### `ratesengine_discovery_dropped_hits_total`

Counter, no labels.

Discovery hits dropped because the async SEP-41 discovery sink buffer
was full. Emitted by the live indexer from periodic `DroppedCount`
sampling, not only at shutdown, so operators can alert on sustained
loss while the process is still running. Any non-zero increase means
discovery coverage is degrading under recorder pressure; this is
best-effort data loss, not a backpressure signal on the main ingest
path.

### `ratesengine_source_insert_errors_total`

Counter, labels `source`, `kind` (`trade` / `oracle` / `panic` / `unhandled`).

`unhandled` fires when a source emits an event type the sink's
type-switch doesn't recognise — usually a half-wired new source
registered in `buildSources()` without a matching case in
`handleOneEvent`. Silent drops would otherwise look like "metrics
say we're ingesting" with empty tables.

Events that failed to persist to the store. `panic` kind flags a
recovered panic in the event-sink handler. A sustained rate signals
storage-layer distress; the `insert-errors` alert escalates.

### `ratesengine_cursor_last_ledger`

Gauge, label `source`.

Mirror of the committed `ingestion_cursors.last_ledger` value for the
live ledgerstream pipeline, updated after each successful cursor
upsert. `cursor-stuck` alert fires when `increase(...[5m]) == 0` with
`source_enabled=1`.

### `ratesengine_trade_inserts_total`

Counter, labels `source`, `usd_volume_populated` (`yes` | `no`).

Per-source attempt counter for `Store.InsertTrade`, broken out by
whether `usd_volume` was populated at insert time (per L2.2 phase 1
— see `internal/storage/timescale.Store.WouldPopulateUSDVolume`).
Operators flipping on `[trades].usd_pegged_classic_assets` use this
to verify their allow-list actually covers what the indexer is
seeing. Counts attempts; the trades hypertable's `ON CONFLICT DO
NOTHING` dedupe is invisible to this counter.

### `ratesengine_stream_publish_total`

Counter, label `stream` (currently only `price_stream`).

Per-stream counter of envelopes the API binary's
`internal/api/streampublish.Publisher` fanned out to a
`streaming.Hub`. Increments only on a NEW closed bucket — the
publisher short-circuits when `ObservedAt` hasn't advanced, so a
flat counter against an active subscription means the upstream
`PriceReader` isn't seeing new buckets (cursor stuck, aggregator
stalled, etc.). Operators read this alongside per-pair subscriber
counts to verify the closed-bucket fanout path: steady publishes
with zero subscribers means clients aren't connecting; zero
publishes with active subscribers means the producer is starved.

## Oracle layer (indexer binary, reflector + future sources)

### `ratesengine_oracle_last_update_unix`

Gauge, labels `source`, `asset`.

Unix-seconds timestamp of the most recent oracle observation for the
(source, asset) pair. `oracle-stale` alert compares to
`oracle_resolution_seconds`.

### `ratesengine_oracle_resolution_seconds`

Gauge, label `source`.

Declared publication cadence of the oracle (Reflector: 300 s). Set
once at source construction. Used by `oracle-stale` to make "> 10×
resolution" tractable without hard-coding per-source intervals in
the rule.

## API layer (api binary)

### `ratesengine_price_staleness_seconds`

Gauge, label `asset`.

Age of the most recent price served for `asset` via `/v1/price`, in
seconds. Updated per request so a popular asset keeps a fresh
reading; unqueried assets stop updating and the `price-stale` alert
uses `change()` to distinguish "no-update" from "updated-but-stale".

### `ratesengine_sep1_cache_ops_total`

Counter, label `result` (`hit` / `miss` / `upstream_error`).

SEP-1 resolver cache outcomes. Operators watch `hit / total` for
cache effectiveness and `upstream_error` rate for issuer-side
outages. `upstream_error` deliberately doesn't cache — a 404 from
an issuer is a real signal, typically transient.

### `ratesengine_ratelimit_fail_open_total`

Counter, no labels.

Requests that bypassed rate-limiting because the Redis backing store
errored. The middleware fails open deliberately (Redis outage
shouldn't take down the API); this metric gives ops a quantitative
signal that correlates with `redis` readyz turning red.

### `ratesengine_aggregator_ticks_total`

Counter, label `outcome` (`ok` / `error`).

One increment per aggregator orchestrator tick. `error` fires when
at least one (pair, window) refresh inside the tick failed — a tick
with all-pair-success records as `ok`. Per-pair errors still surface
as soft warnings; this counter is the tick-level rollup operators
watch for sustained instability.

### `ratesengine_aggregator_vwap_writes_total`

Counter, no labels.

Cumulative VWAP cache writes performed by the aggregator. Pair-level
detail intentionally excluded — Prometheus cardinality stays bounded
and the per-pair lens lives in the Redis key namespace
(`vwap:<base>:<quote>:<window>`). Operators alert on a sustained
zero-rate as the "aggregator is silent" signal.

### `ratesengine_aggregator_empty_windows_total`

Counter, no labels.

Count of (pair, window) refreshes that produced zero VWAP-eligible
trades after class filtering, stablecoin expansion, and outlier
filtering. The `vwap_writes / empty_windows` ratio surfaces pair
coverage gaps without per-pair cardinality cost — a sustained
all-empty signal usually means the configured pair set has
out-grown the live data.

### `ratesengine_aggregator_dropped_trades_total`

Counter, label `reason` (`class` / `outlier`).

Trades removed from the VWAP input set, broken down by which filter
discarded them. `class` = removed by the ClassExchange-only filter
(non-exchange source: aggregator / oracle / authority_sanity / not
registered). `outlier` = removed by the σ-threshold filter
(`OutlierSigmaThreshold > 0`). A spike in `class` is usually a venue
mis-registered in `external.Registry`; a spike in `outlier` is
usually a market-distress event flooding the window with anomalies.

### `ratesengine_aggregator_dropped_windows_total`

Counter, label `reason` (`min_usd_volume`).

Windows the orchestrator suppressed at the window-level filter step
— distinct from `dropped_trades_total` (per-trade) and
`empty_windows_total` (zero trades to begin with). `min_usd_volume`
fires when a fiat:USD-quoted pair's post-class + post-outlier window
has less total USD volume than `aggregate.min_usd_volume` (closes
launch-readiness L2.1). Operators alert on a sustained
fraction-of-ticks dropping for `min_usd_volume` as a sign that a
configured pair has thinned out beyond the threshold or the
threshold is mis-tuned.

## Supply derivation (aggregator binary)

### `ratesengine_supply_cross_check_divergence_stroops`

Gauge, label `classic_key` (`CODE:ISSUER`).

Absolute stroop difference between a classic asset's Algorithm 2
total_supply (ledger-entry sum) and its SAC-wrapped Algorithm 3
total_supply (SEP-41 event sum). Per ADR-0011 the two MUST agree
within 1 stroop because both algorithms observe the same underlying
state. Drives the
[`ratesengine_supply_cross_check_divergence`](../../operations/runbooks/supply-cross-check-divergence.md)
alert when > 1.

### `ratesengine_supply_cross_check_total`

Counter, label `outcome` (`within` / `over`).

Cross-check evaluations classified by whether the divergence stayed
within tolerance. Drives the alert's rate-of-failure view and
provides a "is the cross-checker even running" check orthogonal to
the gauge — a flat gauge with zero counter increments means the
orchestrator stopped invoking the cross-check, not that everything's
healthy.

### `ratesengine_aggregator_triangulations_total`

Counter, label `outcome` (`ok` / `missing_leg` / `parse_error` /
`redis_error`).

Triangulation outcomes per tick × chain × window. The aggregator
runs one row per (chain, window) per tick after the per-pair
refresh; steady state is mostly `ok` with periodic `missing_leg`
entries when a leg's window was empty this tick. Sustained
`parse_error` or `redis_error` rates above baseline indicate
upstream regression worth investigating (Redis blip, malformed
cached value).

### `ratesengine_aggregator_fx_snap_fallback_total`

Counter, label `leg` (canonical pair string of the FX leg, e.g.
`fiat:USD/fiat:EUR`).

Triangulations that fell back to cached VWAP for an FX leg because
`FXQuoteAtOrBefore` returned no row at-or-before the bucket-end
timestamp. The X2.5 forex-snap rule (ADR-0018 §"Forex factor handling")
mandates the FX factor be the most recent FX-source quote at-or-before
bucket close; on miss, the orchestrator falls back to the leg's
cached VWAP so the chain still publishes (degraded but functional).

Steady state should be near-zero once FX ingestion is warm. Cardinality
is bounded by the operator-configured triangulation chain set —
typically a single-digit number of FX legs across all chains. Sustained
> 50% of triangulations indicates an FX-source health issue; the alert
in `deploy/monitoring/rules/aggregator.yml` fires at 30m sustained
fallback dominance.

### `ratesengine_divergence_refresh_total`

Counter, label `outcome` (`ok` / `no_vwap` / `parse_error` /
`refresh_error`).

Per-Tick outcomes for the orchestrator's divergence-cache refresh
loop (ADR-0019 / launch-readiness L2.10 + L2.11). The aggregator
calls `divergence.Service.RefreshPair` once per configured pair
per Tick, using the pair's shortest-window VWAP as "our price"
input; the Service queries CoinGecko + Chainlink (when configured),
computes the divergence percent vs the median external reference,
and writes the result to `div:<asset>` in Redis. The API's
`flags.divergence_warning` reads from that cache.

`no_vwap` is benign on cold start and after Phase-1/Phase-2 freezes
(no fresh VWAP to compare against). Sustained `refresh_error` means
external references are unreachable — `flags.divergence_warning`
goes stale across the API surface; alert on a sustained rate via
`ratesengine_divergence_refresh_error_dominant` (deploy/monitoring/rules/aggregator.yml).

### `ratesengine_aggregator_baseline_refresh_total`

Counter, label `outcome` (`ok` / `not_enough_samples` / `read_error` /
`write_error`).

Baseline refresh outcomes per pair × refresh cycle (ADR-0019 Phase 2).
The aggregator's baseline-refresh worker recomputes Median + MAD over
each pair's 30-day VWAP window on an hourly cadence and UPSERTs the
result into `volatility_baseline_1m`. One increment per pair per cycle.

Steady state is mostly `ok`. Sustained `not_enough_samples` indicates
pairs in bootstrap (ADR-0019 §"Bootstrap policy") — the API's
confidence score for those pairs will fall back to the bootstrap
factor instead of using a per-asset baseline. Sustained `read_error`
or `write_error` rates indicate the storage layer needs investigation
(prices_1m read failing or volatility_baseline_1m write conflict).

### `ratesengine_aggregator_supply_refresh_total`

Counter, labels `asset_key` + `outcome`. `outcome` ∈ (`ok` /
`no_ledger` / `no_observation` / `compute_error` / `write_error`).
`asset_key` is the `supply.AssetKey` form: `XLM`, `CODE:ISSUER`
for classic credits, the bare contract C-strkey for SEP-41.

Supply-snapshot refresh outcomes per (asset_key, outcome) per
refresh cycle (ADR-0011, ADR-0021, ADR-0022, ADR-0023). The
aggregator's supply-refresh goroutine recomputes each watched
asset's supply on the operator-configured cadence (`[supply]
aggregator_refresh_cadence`, default 5 min) and inserts the
snapshot into `asset_supply_history` (idempotent on
`(asset_key, ledger_sequence)`). One increment per (asset, tick).

Only fires when `[supply] aggregator_refresh_enabled = true` —
operators that drive the writer via the systemd timer in
`deploy/systemd/supply-snapshot.timer` instead see this counter
stay at zero.

Steady state is mostly `ok` per asset. Sustained `no_observation`
on an asset indicates the AccountEntry observer hasn't backfilled
the relevant accounts yet AND the static fallback config is also
empty or missing entries — expected briefly post-deploy, alarming
sustained. `no_ledger` fires before the indexer produces its
first ingestion cursor; clears as soon as ingest catches up.
`write_error` indicates the storage layer needs investigation.

The `asset_key` label lets operators chart per-asset bootstrap
progress + isolate failure modes per asset rather than chasing
a single aggregate signal across the watched-set.

### `ratesengine_aggregator_confidence_compute_total`

Counter, label `outcome` (`ok` / `skipped` / `baseline_missing` /
`marshal_error` / `write_error`).

Confidence-score compute outcomes per (pair, window) × tick (ADR-0019
§"Multi-factor confidence score"). The aggregator computes a
`confidence.Score` after each successful VWAP publish and writes it to
Redis at `confidence:<base>:<quote>:<window>`.

`skipped` covers the first-tick / no-prev-VWAP case (expected on
startup until the comparator slot warms). `baseline_missing` covers
pairs whose 30d baseline isn't yet computed — sustained values here
indicate the L2.5 baseline-refresh worker isn't keeping up with the
configured Pair set, and the API's confidence on those pairs falls
back to bootstrap. `ok` should be the dominant value in steady state.

`marshal_error` / `write_error` indicate the JSON encoder or Redis
itself misbehaved — both should be flat-zero in healthy operation.

### `ratesengine_anomaly_freeze_engaged_total`

Counter, label `class` (`stablecoin` / `treasury` / `crypto` /
`governance` / `default`).

ActionFreeze decisions emitted by the aggregator anomaly checker
(ADR-0019). Each increment means the orchestrator declined to
publish a fresh VWAP for some pair (kept the prior bucket's
last-known-good value); the API's `/v1/price` for the affected
pair will surface `flags.frozen=true` on the next read.

Pair-specific freeze details live in the `freeze:<asset>:<quote>`
Redis marker JSON (deviation_pct, reason, frozen_at) — labelled by
class only here so cardinality stays bound to the small AssetClass
enum.

## verify-archive (ratesengine-ops one-shot)

Emitted by `ratesengine-ops verify-archive` when the operator
passes `-metrics-listen ADDR`. One-shot diagnostic command, but the
run can take hours on full pubnet sweeps — live metrics let
operators dashboard the bottleneck during the run rather than
guessing from log tails.

All vectors labelled by `chunk_idx` (decimal string) so a parallel
run with `-workers 8` produces per-chunk series. Cardinality bound
by the `-workers` cap (currently `[1, 16]`).

### `ratesengine_verify_archive_ledgers_verified_total`

Counter, label `chunk_idx`.

Ledgers walked + verified per chunk. Rate over time gives ledgers/sec
per chunk — primary signal for spotting a stalled chunk versus a
slow one.

### `ratesengine_verify_archive_current_ledger`

Gauge, label `chunk_idx`.

Most-recent ledger sequence verified by each chunk. Together with
the chunk's `[from, to]` range (operator-known) gives a
percent-complete view; together across chunks gives a
ledger-distance-fan picture of leading vs trailing chunks.

### `ratesengine_verify_archive_checkpoints_total`

Counter, labels `chunk_idx` + `outcome` (`matched` / `missed`).

Tier B checkpoint outcomes per chunk. `missed` = archive file
absent (warning, or hard fail under `-fail-on-missed`); `matched` =
hash-equal proof.

### `ratesengine_verify_archive_mismatches_total`

Counter, labels `chunk_idx` + `reason` (`chain` / `sequence` /
`checkpoint`).

Chain breaks, sequence gaps, and checkpoint hash mismatches.
**Any non-zero reading is a hard failure** — the counter exists so
dashboards can distinguish "mismatch fired and the run aborted at
second X" from "chunk aborted for an unrelated reason (canceled
context)".

## Changelog

- 2026-04-29 — added verify-archive metrics (`ratesengine_verify_archive_*`)
  covering per-chunk ledger progress, checkpoint outcomes, and
  mismatches.
- 2026-04-28 — added supply cross-check metrics (L2.12 PR 5)
- 2026-04-25 — added aggregator orchestrator metrics
  (`ratesengine_aggregator_*`) covering tick outcomes, VWAP writes,
  empty windows, and per-stage trade drops.
- 2026-04-23 — initial reference document to close the lint drift.
