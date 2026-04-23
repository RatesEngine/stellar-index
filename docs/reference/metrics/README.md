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

## Changelog

- 2026-04-23 — initial reference document to close the lint drift.
