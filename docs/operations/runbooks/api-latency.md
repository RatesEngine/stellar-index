---
title: Runbook — api-latency
last_verified: 2026-04-30
status: draft
severity: P2 (direct-threshold) / P1 (SLO burn-rate fast)
---

# Runbook — `ratesengine_api_latency_p95_high` / `_p99_high` (+ SLO burn-rate variants)

## At a glance

| Field | Value |
| ----- | ----- |
| Direct-threshold alerts | `ratesengine_api_latency_p95_high` (> 500 ms), `ratesengine_api_latency_p99_high` (> 2 s) — `deploy/monitoring/rules/api.yml` |
| SLO burn-rate alerts | `ratesengine_slo_latency_burn_{fast,medium,slow}` (per ADR-0009 multi-window pattern) — `deploy/monitoring/rules/slo.yml` |
| Severity | P2 (ticket) for direct-threshold; **P1** for fast/medium burn, P3 for slow burn |
| Typical MTTR | 15–60 min |
| Impact | Requests complete but slowly. Freighter's wallet feels sluggish; clients with tight timeouts may give up and retry. Not customer-visible as an outage, but breaches our SLA (p95 ≤ 200 ms, p99 ≤ 500 ms). |

## Burn-rate vs direct-threshold pages

The two alert families (this runbook handles both) signal
different things:

- **Direct-threshold** (`p95_high` / `p99_high`): "p95 just
  crossed the line." Useful as an immediate "look, something
  changed" signal but noisy — a single bad bucket can trip it.
- **SLO burn-rate** (`slo_latency_burn_{fast,medium,slow}`):
  "we're consuming SLO error budget too quickly." Per the Google
  SRE workbook pattern, both a short and a long window must agree
  before firing — suppresses single-spike noise. The fast tier
  (5m AND 1h windows, 14.4× budget) means the budget will be
  exhausted in hours if this rate continues; medium (30m AND 6h,
  6×) gives days; slow (6h AND 24h, 1×) gives ~weeks.

If a `_burn_fast` page reaches you, the diagnosis flow below is
the same — but the urgency is "we're burning budget at a
SEV-1-worthy rate", not "p95 happens to be elevated for a
moment." The mitigation should target a real fix, not just
tolerate the symptom until the alert clears.

## Symptoms

- `histogram_quantile(0.95, rate(http_request_duration_seconds_bucket[5m])) > 0.5` for ≥ 2 min.
- Per-endpoint panel on the *API → latency* dashboard shows which
  route carries the tail — usually `/v1/vwap` or `/v1/history`
  (both are CAGG-backed), not `/v1/price` (Redis hot-path).
- Client-facing: higher 499 (client-abort) rate as downstreams time out.

## Quick diagnosis (≤ 5 min)

```sh
# Which endpoint is slow?
curl -s http://prometheus:9090/api/v1/query --data-urlencode \
  'query=histogram_quantile(0.95, sum by (route, le) (rate(http_request_duration_seconds_bucket{job="api"}[5m])))' | \
  jq -r '.data.result[] | "\(.metric.route): \(.value[1])s"' | sort -k2 -rn | head

# Is Redis healthy? Cache-miss storms are the common trigger.
redis-cli -h redis --latency-history  # in another pane
curl -s http://api:9464/metrics | grep sep1_cache_ops_total

# Is Timescale the bottleneck?
psql -c "SELECT state, count(*), max(now()-query_start) AS oldest
         FROM pg_stat_activity WHERE application_name LIKE 'ratesengine%'
         GROUP BY state;"
```

## Typical root causes (roughly in frequency order)

1. **Redis cache-miss storm**. A popular asset's price key gets
   evicted / TTL'd; suddenly every `/v1/price?asset=X` request is
   a full Timescale query. This cascades because Timescale
   saturates → every request slows → pileup.
   - Signal: `ratesengine_sep1_cache_ops_total{result="miss"}`
     rate jumps; Redis eviction rate > 100/s.
   - Mitigation: warm the cache or scale Redis memory; see
     `redis-memory.md`.

2. **Timescale contention**. A long-running query (manual backfill,
   an operator's exploratory `SELECT` without a LIMIT) holds shared
   locks or fills the connection pool.
   - Signal: `pg_stat_activity` shows a query > 30 s old.
   - Mitigation: `SELECT pg_cancel_backend(pid)` on the offender
     after confirming it's not production traffic.

3. **CAGG not refreshed**. `/v1/vwap` / `/v1/twap` fall back to a
   raw-trades aggregation when the CAGG is stale; that path is
   O(trades) not O(buckets) and can take seconds.
   - Signal: `ratesengine_timescale_cagg_stale` usually fires too.
   - Mitigation: refresh the CAGG manually; see `cagg-stale.md`.

4. **Noisy neighbor on the host**. Another tenant on the same node
   pegs CPU or IO.
   - Signal: `ratesengine_host_cpu_high` fires on the same instance.
   - Mitigation: scale horizontally or move to a dedicated node.

5. **GC pressure** from a runaway allocation pattern (we've seen
   this once — a loop concatenating strings per trade in a hot
   path). Not infrastructure.
   - Signal: `go_gc_duration_seconds` quantile rises; latency
     correlates with GC pauses.
   - Mitigation: profile + fix; usually requires a code PR.

## Mitigation

- [ ] Step 1 — narrow down which endpoint is slow (diagnosis above).
- [ ] Step 2 — walk the likely causes in the order above; each has
      a faster check than the next.
- [ ] Step 3 — if Redis-driven: jump to `redis-memory.md`.
- [ ] Step 4 — if Timescale-driven: jump to `pg-conns-saturated.md`
      or `replica-lag.md` depending on the signal.
- [ ] Step 5 — scale the API up if the hot-path is CPU-bound and
      other causes are ruled out. Latency-driven scale-out is a
      bandaid — file a follow-up for the real fix.
- [ ] Verification: p95 back under 200 ms for 15 min (our SLA
      target, not the alarm threshold — don't leave it oscillating
      between 200 and 500 ms).

## Root cause analysis

- Per-route latency histograms for the incident window.
- `pg_stat_statements` top-N by `total_exec_time` around the event.
- Redis `INFO stats` delta (`keyspace_misses`, `evicted_keys`).
- Correlated dashboards: was Timescale disk/IO saturated? Was CPU
  maxed on the API hosts?

## Known false-positive patterns

- **Midnight UTC** — our 24h-trade-count window rolls over and
  the first request of each hour for a rarely-queried asset pays
  the cold-cache cost. The histogram bucket at midnight reflects
  that single slow request, not a sustained issue. The `for: 2m`
  window usually absorbs this.
- **Large `limit=500` `/v1/markets` scan** after a fresh deploy
  with cold Timescale buffers. Warms up within a minute.

## Related

- `api-5xx.md` — errors, not slowness.
- `redis-memory.md` / `cagg-stale.md` — common upstream causes.
- `pg-conns-saturated.md` — when the pool is the bottleneck.
- Freighter SLA spec in `docs/freighter-rfp.md` §Performance.

## Changelog

- 2026-04-23 — initial draft. Alert threshold is 2.5× the SLA
  target (p95) and 4× (p99) so we get lead time, not just
  breach notifications.
- 2026-04-30 — runbook now also covers the SLO multi-window
  burn-rate alerts shipped in #313 (per ADR-0009), which route
  here. Burn-rate-vs-threshold section explains the different
  semantics so on-call doesn't treat a `_burn_fast` page as a
  benign spike.
