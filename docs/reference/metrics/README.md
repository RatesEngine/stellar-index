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

### `ratesengine_ingest_gap_ledgers`

Gauge, labels `source`.

Total missing ledgers in contiguous data-coverage gaps >= the
detector's `min-gap-size` threshold (1000 by default) per source.
**Data-derived** complement to the cursor-derived density
projection in `/v1/diagnostics/ingestion`: cursor coverage
measures process state ("did we walk this ledger") and can read
100% while data is missing; this gauge measures reality by
querying `soroban_events` distinct-ledger coverage directly.
Refreshed every 5 min by the gap detector goroutine in the
aggregator binary (`internal/storage/timescale.RunGapDetector`).
Source label values: `soroban-events` today; SDEX detection is a
follow-up.

### `ratesengine_ingest_gap_count`

Gauge, labels `source`.

Number of contiguous gaps per source at the detector's most
recent cycle. A single 100K-ledger gap and 100 ten-ledger gaps
both report ≈100K in `ratesengine_ingest_gap_ledgers` but very
different shapes; chart this alongside the size gauge to
distinguish "one big halt" (cascade signature) from "many small
drops" (flaky-write pattern).

### `ratesengine_ingest_gap_max_size_ledgers`

Gauge, labels `source`.

Size of the largest contiguous gap per source at the detector's
most recent cycle. Drives the `ratesengine_ingest_gap_detected`
P1 alert (fires when > 1000 sustained 15 min).

### `ratesengine_ingest_source_distinct_ledgers`

Gauge, labels `source`, `table`.

Count-distinct of ledgers in the per-source hypertable at the
most recent gap-detector cycle. The **numerator** of the ADR-0031
data-derived density signal:

```
density(source) = ratesengine_ingest_source_distinct_ledgers / (tip - genesis + 1)
```

Where `tip` comes from
`ratesengine_ingest_gap_detector_tip_ledger` and `genesis` is the
per-source first-deploy ledger (hard-coded in the diagnostic
handler's source-genesis map). Dense sources (SDEX, Soroswap)
approach the [genesis, tip] span; sparse-by-design sources (Blend
auctions, CCTP) are naturally lower because the contract doesn't
emit per ledger.

### `ratesengine_ingest_gap_detector_tip_ledger`

Gauge (no labels).

The live ledgerstream cursor's `last_ledger` at the most recent
gap-detector cycle's start — the upper bound used by every scan.
ADR-0031 consumers subtract per-source genesis from this to
compute the density denominator. One gauge for the whole detector
because every target uses the same tip in the same cycle.

### `ratesengine_ingest_gap_detector_runs_total`

Counter, labels `source`, `outcome`.

Periodic gap-detector cycle outcomes — `ok` on a clean scan,
`error` on a transient Postgres / timeout failure. Operators
chart `rate({outcome="ok"}[5m])` to confirm the worker is alive;
the `ratesengine_ingest_gap_detector_silent` ticket-tier alert
fires when this rate goes to zero.

### `ratesengine_ingest_gap_detector_duration_seconds`

Histogram, labels `source`, `outcome`.

Wall-clock latency of one detector cycle. The LAG()-over-DISTINCT
scan against ~12 M Soroban-era ledgers takes ~2-3 s on r1; the
60s scan timeout is the upper bound.

### `ratesengine_projector_lag_ledgers`

Gauge, labels `source`.

Distance (in ledgers) between the projector's per-source cursor
and the live ledgerstream tip at the end of the last cycle. 0 =
caught up. Drives the `ratesengine_projector_lag_high` alert (P3
ticket: > 256 ledgers sustained 10 min). See ADR-0032.

### `ratesengine_projector_runs_total`

Counter, labels `source`, `outcome`.

Per-cycle outcome counter. Outcomes: `ok` (cursor advanced),
`idle` (caught up, no rows in scan range), `error` (scan / cursor
read / cursor write failed; cursor not advanced — retried next
cycle). Drives the `ratesengine_projector_error_rate_high` alert.

### `ratesengine_projector_events_decoded_total`

Counter, labels `source`, `outcome`.

Number of consumer.Events the projector emitted to its sink.
Outcomes: `ok` (decode succeeded) and `decode_error` (Reconstruct
or Decoder.Decode returned non-nil; row skipped, cursor still
advances).

### `ratesengine_projector_cycle_duration_seconds`

Histogram, labels `source`.

Wall-clock latency of one projector cycle (scan + decode + sink).
Each cycle is bounded by `PerSourceTimeout=60s`. Sustained p99 >
30s for one source is the first sign that the sink is the
bottleneck.

### `http_request_success_duration_seconds`

Histogram, labels `method`, `route`.

The non-5xx twin of `http_request_duration_seconds`. The HTTP
middleware records into this histogram only when the response status
is < 500 (and not 499 / client-aborted). Pair with
`http_request_duration_seconds_count` for the latency-SLO ratio so a
fast 5xx burns budget: numerator counts fast successes; denominator
counts all requests including errors. Added in 2026-05-28 to close
F-0105 (audit 2026-05-26) — pre-this-PR the SLO ratio reported a
5ms 500 as a "good fast" response.

### `ratesengine_api_cache_ops_total`

Counter, labels `cache`, `op`, `result`.

Every read through the API's in-memory cache wrappers
(`v1.CachedMarketsReader`, future `v1.CachedCoinsReader`, …)
increments this counter. `cache` is the wrapper name (e.g.
`markets`); `op` is the cached method (`distinct_pairs` /
`source_markets` / `asset_markets` / `all_pools`); `result` is
`hit` (returned cached value, including single-flight-wait
callers that piggy-backed on an in-progress upstream call) or
`miss` (called upstream).

Use to detect prewarm-key drift: when a prewarm goroutine warms
key A but the handler looks up key B, `result="miss"` rate
stays high even though the prewarm cycle is running. Suggested
alert: `rate(ratesengine_api_cache_ops_total{result="miss"}[5m])
/ rate(ratesengine_api_cache_ops_total[5m]) > 0.5` sustained
for 10 min on any (cache, op) pair — for hot ops the prewarm
should keep miss rate under 10%.

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

### `ratesengine_source_last_insert_unix`

Gauge, label `source`. Wall-clock Unix-seconds timestamp of the
most recent SUCCESSFULLY-inserted trade row per source (i.e.
`Store.InsertTrade` returned with `rowsInserted == 1`, not
`ON CONFLICT DO NOTHING`).

Pairs with `ratesengine_source_last_event_unix` to expose the
stuck-cursor / duplicate-flood pattern: when the dispatcher matches
events (last_event climbs) but every insert hits the ON CONFLICT
short-circuit (last_insert flat-lines), the gap between the two
grows. Direct alert template:

    time() - ratesengine_source_last_insert_unix{source="sdex"} > 3600

catches the live r1 2026-05-28 pattern (157 SDEX insert-attempts/
min, every one a duplicate, max(ts) 11 h old) within an hour of
recurrence. Complements `ratesengine_trade_insert_outcome_total`'s
rate-shape signal with a timestamp-shape signal that doesn't
require sustained traffic to fire.

### `ratesengine_source_matched_events_total`

Counter, label `source`.

Per-source count of inputs (events, contract calls, entry changes,
classic ops) the decoder's `Matches()` claimed. The DENOMINATOR of
decoder error-rate — chart
`rate(ratesengine_source_decode_errors_total[5m]) /
rate(ratesengine_source_matched_events_total[5m])` per source.
Bumped pre-Decode so a decoder that matches then errors still
counts; error-rate stays interpretable (errors / attempted) rather
than tautological (errors / successful).

Distinct from `source_events_total` — that's downstream of decoding
(decoder OUTPUT, what reaches the sink). A decoder that buffers
(soroswap swap+sync correlation) or matches an intermediate event
producing zero outputs would register here but not on
`source_events_total`.

### `ratesengine_source_decode_errors_total`

Counter, label `source`.

Per-event parse failures — SCVal shape mismatch, malformed XDR,
canonical-invariant violations. Distinct from `orphan_events`
(events were well-formed but partnerless) and `insert_errors`
(decoded fine but persistence broke). Emitted from dispatcher stats
deltas after each processed ledger. Denominator is
`ratesengine_source_matched_events_total`.

### `ratesengine_source_unknown_symbols_total`

Counter, label `source`.

Asset slots skipped from an otherwise-decoded oracle event because
the symbol or feed-id isn't in our canonical asset allow-list
(ADR-0010). Distinct from `decode_errors`: the rest of the event
still decoded cleanly — the parent decoder `continue`d past the
unknown slot rather than failing the whole event. Reflector, Band,
and Redstone are the live emitters; CEX streamers don't fan out
into mixed-asset batches the same way. A sustained non-zero rate
means an upstream oracle expanded its feed set and our allow-list
needs an amendment. F-1234 (codex audit-2026-05-12).

### `ratesengine_source_orphan_events_total`

Counter, label `source`.

Events that arrived but never correlated into a complete observation.
Soroswap: swap without matching sync (or vice versa). Phoenix:
incomplete N-of-8 field set aged past the buffer's 5-min ceiling.
Aquarius / Reflector don't emit orphans — they're 1-event-per-
observation. Emitted from decoder-maintained orphan counters via the
live dispatcher path.

### `ratesengine_external_poller_polls_total`

Counter, labels `source`, `outcome` ∈ {success, error, skipped}.

Per-source, per-outcome count of `PollOnce` invocations from the
external-poller runner. Emitted on every poll tick of every
configured external source (CoinGecko, CoinMarketCap, CryptoCompare,
ECB, ExchangeRatesAPI, PolygonForex, Binance, Coinbase, Kraken,
Bitstamp). The `skipped` outcome covers the per-poller cooldown path
(e.g. CoinGecko's post-throttle backoff) — distinct from `success`
so absence-of-success alerting isn't masked by the poller silently
respecting a backoff window.

### `ratesengine_cex_stream_disconnect_total`

Counter, labels `source`, `reason` ∈ {reset, broken_pipe, timeout, dial, server_requested, other}.

Per-source, per-reason count of CEX WebSocket stream disconnects from
the Binance and Bitstamp streaming sources. `reset` is the most common
on r1 (Binance proactively recycles connections every 6–12 min); a
sustained rate of `dial` or `timeout` means the venue is unreachable
or our keepalive isn't recovering the socket. Combined with
`ratesengine_external_poller_last_success_unix` (when the streamer
emits trades the runner forwards to the poller's success channel),
operators can distinguish "stream churning but data flowing" from
"stream stuck and we're losing the venue". F-0029 (audit-2026-05-27)
fix landed alongside this metric — bounded 5–60 s exponential backoff
with a healthy-connection reset path, plus TCP keepalive on the
dialer.

### `ratesengine_external_poller_last_success_unix`

Gauge, label `source`.

UNIX-seconds timestamp of the most recent successful `PollOnce` per
external source. Zero / unset when the poller has never succeeded
since process start. Companion to
`ratesengine_external_poller_polls_total`: a gauge makes "data is
stale by N minutes" expressible as `time() - <gauge>` rather than
multi-window rate math, which simplifies alerting (see
`ratesengine_external_poller_stale`).

### `ratesengine_discovery_dropped_hits_total`

Counter, no labels.

Discovery hits dropped because the async SEP-41 discovery sink buffer
was full. Emitted by the live indexer from periodic `DroppedCount`
sampling, not only at shutdown, so operators can alert on sustained
loss while the process is still running. Any non-zero increase means
discovery coverage is degrading under recorder pressure; this is
best-effort data loss, not a backpressure signal on the main ingest
path. With in-process dedup (per `ratesengine_discovery_skipped_hits_total`)
healthy steady-state should never drop — a non-zero rate typically
means a Postgres outage or cold-start burst.

### `ratesengine_discovery_skipped_hits_total`

Counter, no labels.

Discovery hits skipped because their `(contract_id, event_type)` had
already been enqueued in this process and the recorder upserts on the
same key — re-enqueue is wasted work. Emitted by the live indexer from
periodic `SkippedCount` sampling. A high ratio of Skipped to
(Skipped + Recorded) is expected and healthy: most contract events are
duplicates from already-discovered contracts. Tracked for
capacity-planning visibility, not for alerting. A process restart
resets the dedup set; the first push for any key after restart still
records (no-op upsert if already in DB).

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
NOTHING` dedupe is invisible to this counter — pair with
[`ratesengine_trade_insert_outcome_total`](#ratesengine_trade_insert_outcome_total)
below to see new-vs-duplicate.

### `ratesengine_trade_insert_outcome_total`

Counter, labels `source`, `outcome` (`new` | `duplicate`).

Per-source counter of trade-insert outcomes. `new` = the
`INSERT ... ON CONFLICT DO NOTHING` actually persisted a row;
`duplicate` = the conflict short-circuit fired and no row was
written.

On a healthy live indexer `outcome=new` tracks 1:1 with attempts;
a cursor-replay loop or stuck-tip pattern produces a fast-growing
`outcome=duplicate` rate with zero `outcome=new`. Alert on
`rate({outcome="new"}[5m]) == 0 AND rate({outcome="duplicate"}[5m]) > 0`
to catch the live r1-2026-05-28 signature (157 SDEX insert
attempts/min while the hypertable's `max(ts)` was 11 h old).

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

### `ratesengine_aggregator_vwap_cache_write_errors_total`

Counter, no labels.

Cumulative count of failed Redis `SET` attempts during the VWAP
cache write step in `internal/aggregate/orchestrator/orchestrator.go`.
The aggregator returns an error and the next tick retries — but
from the customer surface, sustained failures here mean
`/v1/price` returns 404 on every cached pair (rewritten,
triangulated, stablecoin-proxy paths) while the Timescale-direct
paths continue serving. Surfaces the May-10 SEV-2 incident class
(`internal/incidents/data/2026-05-10-redis-writes-blocked-disk-full.md`)
where Redis BGSAVE failed for ~9 h and the only customer signal
was 404s on rewritten pairs because `flags.stale` was not flipped
(the aggregator process was alive and ticking, just unable to
publish). Operators alert on `rate(...[5m]) > 0` for ≥ 2 min as
the upstream-of-stale signal.

### `ratesengine_aggregator_empty_windows_total`

Counter, no labels.

Count of (pair, window) refreshes that produced zero VWAP-eligible
trades after class filtering, stablecoin expansion, and outlier
filtering. The `vwap_writes / empty_windows` ratio surfaces pair
coverage gaps without per-pair cardinality cost — a sustained
all-empty signal usually means the configured pair set has
out-grown the live data.

### `ratesengine_aggregator_stream_publish_total`

Counter, label `outcome` (`ok` / `error`).

Closed-bucket events handed to the orchestrator's
[`StreamPublisher`](../../../internal/aggregate/orchestrator/orchestrator.go)
(L3.9 SSE fan-out). Production wiring is the Redis-pub/sub
publisher in `internal/api/streaming/redispub`; the API binary's
matching subscriber republishes each event on the in-process
`streaming.Hub` so `/v1/price/stream` clients receive the
fan-out. `outcome="error"` is best-effort failure (publish
errored; the next tick retries; the VWAP cache write itself
is unaffected).

### `ratesengine_api_stream_subscribe_total`

Counter, label `outcome` (`ok` / `decode_error` / `malformed`).

Closed-bucket Redis pub/sub messages the API binary's
[`Subscriber`](../../../internal/api/streaming/redispub/subscriber.go)
processed (L3.9 SSE fan-out, consumer side). `ok` = decoded and
republished on the local `streaming.Hub` so `/v1/price/stream`
SSE subscribers receive the event. `decode_error` = JSON
unmarshal failed (wire-format drift between aggregator's
Publisher and this Subscriber — investigate if non-zero).
`malformed` = JSON decoded but Asset or Quote was empty (no
valid topic to route to; message dropped). All paths log; only
the `ok` path forwards.

### `ratesengine_api_cors_decisions_total`

Counter, label `outcome` (`no_origin` / `allowed_origin` /
`allowed_wildcard` / `denied`).

Per-request CORS decisions emitted by the API binary's CORS
middleware. `no_origin` = request had no Origin header (server-
to-server, curl); `allowed_origin` = exact-match allow-list hit;
`allowed_wildcard` = wildcard policy (`*`) matched; `denied` =
Origin header present but not in the allow-list (browser will
block the response).

The pre-existing `warnOpenCORS` startup-only check fires once at
boot then drifts out of memory. This counter is the per-request
companion — operators dashboard cross-origin traffic patterns and
alert when a wildcard policy starts handling real cross-origin
traffic in production (the silent failure mode of
`RATESENGINE_ALLOWED_ORIGINS=*` slipping into prod with
credentialed auth_mode). F-1244.

### `ratesengine_customer_webhook_delivery_attempts_total`

Counter, label `outcome` (`delivered` / `server_error` /
`client_error` / `exhausted` / `network_error` / `webhook_missing` /
`disabled` / `build_error` / `list_error` / `mark_error`).

Per-attempt outcome of the customer-webhook delivery worker
(`internal/customerwebhook`). `delivered` = 2xx response;
`server_error` = 5xx (scheduled for retry); `client_error` =
4xx (terminally failed — the customer's URL is broken);
`exhausted` = retry budget hit; `network_error` = TCP/TLS/timeout
(retry); `webhook_missing` = registry row deleted mid-flight
(terminal); `disabled` = `webhook.Enabled=false` (terminal);
`build_error` = malformed URL (terminal); `list_error` /
`mark_error` = db transport failure on the queue surface
(transient).

Operator alerts:

```
rate(...{outcome="server_error"}[5m]) > 0.1
  # one customer's URL is sustained-failing — open a ticket

rate(...{outcome="exhausted"}[1h]) > 0
  # a delivery permanently failed after 15 retries — drag the
  # deliveries log
```

F-1270 (audit-2026-05-12).

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

### `ratesengine_anomaly_freeze_recovered_total`

Counter, no labels.

Freeze rows the recovery worker closed (`MarkRecovered` stamped
`recovered_at` on the durable `freeze_events` row after the Redis
marker TTL elapsed). Steady-state rate trails
`ratesengine_anomaly_freeze_engaged_total` by the freeze TTL plus
the recovery-worker poll interval (default 60s). A persistent gap
between the two indicates the recovery worker is broken — see the
[freeze-recovery-stalled runbook](../../operations/runbooks/freeze-recovery-stalled.md).

### `ratesengine_anomaly_freeze_recovery_sweeps_total`

Counter, label `outcome` (`ok` / `partial` / `error`).

Recovery-worker poll cycles. `error` outcomes mean the lister or
Redis transport failed for the entire sweep; `partial` means
`MarkRecovered` failed for one or more rows (postgres write path
issue) but the rest of the sweep completed. Sustained non-`ok`
indicates an upstream infrastructure problem; the recovery worker
itself retries on the next tick.

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

### `ratesengine_anomaly_freeze_recovery_sweep_duration_seconds`

Histogram, label `outcome` (matches the `_sweeps_total` counter:
`ok` / `partial` / `error`).

Latency of the freeze recovery worker's per-sweep tick. Pairs
with `_sweeps_total` — that one tells you whether sweeps succeed,
this one tells you how long they take. Sweep does ListOpen
(Postgres read) plus, per open row, a Redis GET and possibly
MarkRecovered (Postgres write). Fast path is sub-100 ms when
zero rows are open; latency scales with open-row count.

Latency degradation typically means Postgres pressure or Redis
lag rather than a freeze-policy issue. Sweep cadence is 60 s,
so even a multi-second sweep doesn't lose correctness — the
next tick catches up — but sustained slowness is worth
investigating before the freeze_events table accumulates open
rows the operator UI shows as permanently firing.

Buckets span 10 ms → 30 s. No alert wired today.

### `ratesengine_aggregator_supply_refresh_duration_seconds`

Histogram, label `outcome` (matches the per-asset_key counter's
outcome enum: `ok` / `no_ledger` / `no_observation` /
`compute_error` / `write_error` / `stale_component` /
`missing_freshness`).

Latency of the supply.Refresher.Tick call per supply-refresh
cycle. Pairs with `_aggregator_supply_refresh_total{asset_key,
outcome}` — that one tells you which assets refreshed + how often
they succeeded; this one tells you how long each tick took.

**Why no `asset_key` label here?** Histograms multiply cardinality
by buckets; pairing `asset_key × outcome × 12 buckets` blows up
fast on deployments watching many assets. Operators correlate
per-asset latency from the per-tick log line emitted by
`supply.Refresher.Tick` (timestamps + asset_key) when needed.

Steady-state ~50-200 ms per tick. A p99 climb past 1 s typically
means the snapshot inserter is contending with another writer or
a per-component freshness reader fell off its index. Buckets span
10 ms → 30 s. No alert wired today; the existing
`ratesengine_supply_snapshot_*` alert family covers freshness
+ never-initialised paths.

### `ratesengine_divergence_refresh_duration_seconds`

Histogram, label `outcome` (`ok` / `no_vwap` / `parse_error` /
`refresh_error`).

Per-pair divergence-refresh latency. Pairs with the existing
`_total` counter — that one tells you how often refreshes happen
+ whether they succeed; this one tells you how long they take.

`RefreshPair` fans out to every configured external reference
(CoinGecko, Chainlink, …) for the pair, so the natural failure
mode is "one vendor's API goes slow and the whole refresh tick
stretches" — invisible without this metric. Operators chart
`ok` p95/p99 separately to detect vendor slowdown without a
`refresh_error` outcome (the slow vendor still returns,
eventually).

Buckets span 10 ms → 30 s — covers a healthy local cache-only
refresh (≤ 50 ms when every reference is cached), a single slow
vendor (~1-5 s on CG / Chainlink), and the worst-case
per-reference timeout (`per_reference_timeout_seconds`,
default 5 s) compounded across multiple references. No alert
wired today; the existing failing-rate signal lives in the
`_total` counter.

### `ratesengine_customer_webhook_delivery_duration_seconds`

Histogram, label `outcome` (`delivered` / `server_error` /
`client_error` / `network_error` / `build_error`).

Latency of the outbound HTTP POST inside the customer-webhook
delivery worker (`internal/customerwebhook/worker.go`). Pairs with
the `_attempts_total` counter — that one tells you how often
attempts happen + whether they succeed; this one tells you how
long they take.

The standard `http_request_duration_seconds` histogram covers the
INBOUND HTTP handler surface but not goroutine workers, so this
metric closes the corresponding gap for the OUTBOUND delivery
path. Includes body-drain time (the worker io.Copy(io.Discard,
resp.Body) so the connection can be reused).

Operators chart p95/p99 latency separately per outcome to isolate:

- `delivered` p99 climbing → a customer's endpoint is slow.
  Customer-side problem; we keep delivering, just slower.
- `server_error` p99 high → the customer's endpoint takes long
  AND returns 5xx; usually the same endpoint failing harder
  rather than two distinct problems.
- `network_error` p99 → connect or TLS handshake stalling. Often
  upstream DNS / network blip rather than the customer.
- `build_error` recorded as 0 (no HTTP roundtrip happened) so the
  bucket still populates in dashboards.

Buckets span 10 ms → 60 s (the worker's per-request context
timeout). No alert wired today; the existing
`ratesengine_customer_webhook_delivery_failing` covers the
failing-rate signal, latency degradation surfaces in the
dashboard.

### `ratesengine_postgres_ping_total`

Counter, label `outcome` (`ok` / `error`).

Emitted by the indexer's `watchPostgresPing` resilience goroutine
every 60 s. Probes the *sql.DB pool with `PingContext` (5 s
timeout). `ok` = healthy round-trip; `error` = any failure mode
(timeout, connection refused, dead pool, DSN misconfig).

**Why this exists (F-0151 / 2026-05-26 cascade):** when
postgres@15-main crashed and recovered during the disk-full SEV,
the indexer's pool held stale conns and silently failed writes
for ~14 h until a manual restart. The pool now retires conns every
30 min via `SetConnMaxLifetime` — automatic safety-net — and this
counter is the live observability signal so the next cascade
surfaces in minutes via `ratesengine_postgres_ping_failing`
instead of hours of silent drift.

Alert: `rate(ratesengine_postgres_ping_total{outcome="error"}[5m]) > 0.5`
for 2 m → page. Brief failures during a postgres restart are
expected; sustained means the pool is wedged.

### `ratesengine_postgres_ping_failure_streak`

Gauge, no labels.

Consecutive failed-ping count from the same `watchPostgresPing`
goroutine. Resets to 0 on the next success. Pair with
`ratesengine_postgres_ping_total` on dashboards to chart the live
streak alongside the cumulative outcome counts. The indexer logs a
structured error at `streak == 3` (`pool may be wedged`); search
the journal for that string when triaging the
`ratesengine_postgres_ping_failing` page. F-0151.

### `ratesengine_tls_cert_not_after_unix`

Gauge, label `host`.

Unix-seconds NotAfter timestamp of the leaf TLS cert observed at
the configured host. Emitted by the API binary's self-probe
(`internal/api/v1/tls_probe.go::RunTLSCertProbe`) on a 6 h cadence.
Probe failures keep the last-known value in place — the probe
counter below is the freshness signal. F-0051.

Alert `ratesengine_tls_cert_expiring_soon` fires when
`(not_after_unix - time()) < 14 * 24 * 3600` sustained 1 h.

### `ratesengine_tls_cert_probe_total`

Counter, labels `host`, `outcome` (`ok` / `dial_error` /
`timeout` / `no_cert`).

TLS cert self-probe outcomes per host. A growing `ok` rate while
`ratesengine_tls_cert_not_after_unix` stays flat is the success
signal; a sustained non-`ok` rate alongside a stale gauge means
the probe itself is failing — investigate before the gauge ages
out via the alert rule's 14-day threshold. F-0051.

### `ratesengine_stripe_platform_sync_errors_total`

Counter, label `operation` (`get_account` / `upsert_subscription` /
`account_update` / `list_keys` / `key_update`).

Failures inside the Stripe webhook's platform-store side-effects path
(`internal/api/v1/stripe_webhook.go::applyPlatformSideEffects` and
the subscription / invoice handlers). The webhook deliberately does
NOT 5xx on platform-store failures — Stripe retries would just keep
applying the same Redis rate-limit without making the platform-store
path any healthier — so this counter is the operator-visible signal
that the bridge is degraded.

**Any non-zero reading is alertable**: the customer's dashboard /
Postgres-backed key state is drifting from their Stripe billing
state. Per-`operation` breakdown isolates the failing layer:
`get_account` → no row for that Stripe customer (signup never
completed); `upsert_subscription` → Postgres write failure;
`account_update` → tier sync failure; `list_keys` / `key_update` →
per-key rate-limit lift failure.

### `ratesengine_markets_skipped_rows_total`

Counter, no labels.

Count of trades rows the `/v1/markets` scanner skipped because
their `base_asset` / `quote_asset` failed to parse as canonical
asset strings. The ingest pipeline only emits canonical asset
codes, so any non-zero reading means something bypassed the
normal write path (manual SQL insert, integration test residue,
etc.) and the row should be cleaned up.

**Any non-zero reading is alertable** — a single unparseable row
used to 500 the entire `/v1/markets` surface and trip page-tier
`api_error_rate_critical` + `slo_availability_burn_fast` alerts
(2026-06-01 incident, one row with `base_asset='test'`). The
handler now skips + bumps this counter instead of failing the
whole response, but operators should still investigate + delete
any row that increments this counter.

## Changelog

- 2026-06-01 — added `ratesengine_markets_skipped_rows_total`
  to surface non-canonical rows in the trades table that the
  /v1/markets scanner is skipping. Closes the 2026-06-01
  incident root cause (one stray test-row 500ed every markets
  request).
- 2026-05-27 — added postgres-pool resilience metrics
  (`ratesengine_postgres_ping_total` +
  `ratesengine_postgres_ping_failure_streak`) emitted by the
  indexer's `watchPostgresPing` goroutine. Closes the F-0151
  observability gap surfaced by the 2026-05-26 cascade (dead
  pool, ~14 h silent drift before manual restart). Pairs with
  the new `ratesengine_postgres_ping_failing` page alert in
  `configs/prometheus/rules.r1/storage.yml` +
  `deploy/monitoring/rules/storage.yml`.
- 2026-05-13 — added freeze-recovery-sweep latency histogram
  (`ratesengine_anomaly_freeze_recovery_sweep_duration_seconds`).
  Pairs with the existing `_sweeps_total` counter; surfaces
  Postgres / Redis pressure as a chartable signal before the
  freeze_events table accumulates open rows.
- 2026-05-13 — added supply-refresh latency histogram
  (`ratesengine_aggregator_supply_refresh_duration_seconds`).
  Pairs with the existing per-asset_key `_total` counter;
  histogram labels by outcome only to keep cardinality bounded
  on deployments watching many assets.
- 2026-05-13 — added divergence-refresh latency histogram
  (`ratesengine_divergence_refresh_duration_seconds`). Pairs
  with the existing `_total` counter to give operators per-pair
  per-outcome p95/p99 — surfaces "one vendor's API is slow" as
  a chartable signal even when the refresh still eventually
  succeeds.
- 2026-05-13 — added customer-webhook delivery latency
  histogram (`ratesengine_customer_webhook_delivery_duration_seconds`).
  Pairs with the existing `_attempts_total` counter to give
  operators per-outcome p95/p99 latency on the OUTBOUND
  webhook surface (the standard `http_request_duration_seconds`
  covers inbound only).
- 2026-05-13 — added Stripe platform-bridge error counter
  (`ratesengine_stripe_platform_sync_errors_total`) covering the
  five platform-store side-effect failure sites in the Stripe
  webhook path. Closes the long-standing TODO from F-1219 wave 32.
- 2026-04-29 — added verify-archive metrics (`ratesengine_verify_archive_*`)
  covering per-chunk ledger progress, checkpoint outcomes, and
  mismatches.
- 2026-04-28 — added supply cross-check metrics (L2.12 PR 5)
- 2026-04-25 — added aggregator orchestrator metrics
  (`ratesengine_aggregator_*`) covering tick outcomes, VWAP writes,
  empty windows, and per-stage trade drops.
- 2026-04-23 — initial reference document to close the lint drift.
