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

Every event the orchestrator dispatched to the indexer's event-sink.
Zero rate + `source_enabled=1` fires the `source-stopped` alert.

### `ratesengine_source_enabled`

Gauge, label `source`.

`1` while the source's goroutine is inside `runSource`; `0` on exit.
Used to qualify the zero-rate source-stopped alert so legitimately
disabled sources don't page.

### `ratesengine_source_lag_ledgers`

Gauge, label `source`.

Ledgers-behind-tip: `resp.LatestLedger - source.Health().LastLedger`.
Zero at tip or when the source hasn't observed any events yet.

### `ratesengine_source_last_event_unix`

Gauge, label `source`. Unix-seconds timestamp of the most recent
event dispatched to the sink. Dashboards use it for a last-seen clock.

### `ratesengine_source_decode_errors_total`

Counter, label `source`.

Per-event parse failures — SCVal shape mismatch, malformed XDR,
canonical-invariant violations. Distinct from `orphan_events`
(events were well-formed but partnerless) and `insert_errors`
(decoded fine but persistence broke).

### `ratesengine_source_orphan_events_total`

Counter, label `source`.

Events that arrived but never correlated into a complete observation.
Soroswap: swap without matching sync (or vice versa). Phoenix:
incomplete N-of-8 field set aged past the buffer's 5-min ceiling.
Aquarius / Reflector don't emit orphans — they're 1-event-per-
observation.

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

Mirror of the `ingestion_cursors` row's `last_ledger` value, updated
every `CursorPersistEvery` tick (30 s default). `cursor-stuck` alert
fires when `increase(...[5m]) == 0` with `source_enabled=1`.

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
