---
title: Post-launch on-call query bundle (L6.7)
last_verified: 2026-05-04
status: operator runbook
---

# Post-launch on-call query bundle

The PromQL queries the on-call types into Grafana / `promtool query
instant` during the **L6.7 first-24h post-launch watch**. Each
query has an expected shape so the on-call can spot anomalies
without having to remember the metric semantics.

These complement the alerts in `deploy/monitoring/rules/*.yml`:
alerts fire on **bad**, these queries paint **what's normal**.

## Bookmark these in Grafana

Save each as a starred query in the "Stellar Index — Launch Watch"
folder (or drop them into a launch-week dashboard). Grafana
variables: `$range` (default `5m`), `$instance` (drop-down across
the API binaries).

## 1. Request rate per surface

```promql
sum by (route) (rate(http_requests_total[$range]))
```

**What healthy looks like**: every public surface emits non-zero
once the showcase site starts driving traffic — `/v1/price`,
`/v1/price/tip`, `/v1/observations`,
`/v1/history/since-inception`, `/v1/assets`, `/v1/oracle/*`,
`/v1/sources`, plus the showcase-fan-out surfaces `/v1/issuers`,
`/v1/issuers/{g_strkey}`, `/v1/markets`,
`/v1/changes/{entity_type}/{id}`, `/v1/diagnostics/cursors`.
`route="unmatched"` is a 404 — acceptable at low rate (clients
exploring), suspicious if sustained.

## 2. Error rate per surface

```promql
sum by (route) (rate(http_requests_total{status=~"5.."}[$range]))
```

**Bar**: < 0.1% of total request rate per surface (the SLA target
is ≥ 99.9% availability). Sustained 5xx on any one surface is a
SEV-2 minimum. The runbook `api-5xx.md` covers triage.

## 3. p95 / p99 latency per surface

```promql
histogram_quantile(0.95,
  sum by (le, route) (rate(http_request_duration_seconds_bucket[$range])))

histogram_quantile(0.99,
  sum by (le, route) (rate(http_request_duration_seconds_bucket[$range])))
```

**Bar**: p95 ≤ 200ms, p99 ≤ 500ms (the service SLA). The SLA
probe (`cmd/stellarindex-sla-probe`) is the formal evidence trail;
this query is the on-call's continuous view.

> **Carve-out**: `/v1/markets` does a `GROUP BY base_asset,
> quote_asset` across the 14-day chunk window of the trades
> hypertable; expect p95 ≤ 300 ms / p99 ≤ 1 s on this route
> (matches the k6 `07-catalogue-browse` thresholds). Other
> catalogue surfaces (`/v1/assets`, `/v1/issuers`,
> `/v1/diagnostics/cursors`) hold the standard 200 ms / 500 ms
> bar.

## 4. Oracle freshness — every source, every asset

```promql
time() - stellarindex_oracle_last_update_unix
  > on (source) stellarindex_oracle_resolution_seconds * 5
```

Returns rows where the oracle hasn't published in 5× its declared
resolution. Empty result = healthy. The
`stellarindex_oracle_stale` alert in `divergence.yml` fires at
10× — this query catches the early-warning band. Reflector ticks
every 5 min (so 25-min staleness shows here); Redstone ticks per
batch push.

## 5. Source events rate by source

```promql
sum by (source) (rate(stellarindex_source_events_total[$range]))
```

**What healthy looks like**: every source registered in
`internal/sources/external/registry.go` emits non-zero — even the
oracle/aggregator-class ones (which contribute to coverage but
not VWAP). Source-stopped fires at zero; this query catches
abnormal **drop** rates before zero.

## 6. Aggregator tick health

```promql
sum by (outcome) (rate(stellarindex_aggregator_ticks_total[$range]))
```

**What healthy looks like**: `outcome="ok"` rate matches the
configured tick interval (default 30s → ~120/h). `outcome="error"`
is non-zero only during transient redis/store hiccups. Sustained
error rate is a SEV-2.

## 7. VWAP cache writes — pair coverage proxy

```promql
rate(stellarindex_aggregator_vwap_writes_total[$range])
```

Single-counter (no labels), so this is the global VWAP-write
throughput. Compare to expected = `len(pairs) × len(windows) ×
ticks_per_minute`. Significant under-reporting points at
empty-window storms.

## 8. Decode errors per source

```promql
sum by (source) (rate(stellarindex_source_decode_errors_total[$range]))
```

**Bar**: < 1 error per minute per source in steady state. A spike
on a Soroban source after a `update_contract` upgrade is a SEV-2
— the contract-schema-evolution doc explains the pattern, the
`decode-errors` runbook explains the response.

## 9. Confidence-score distribution (qualitative spot-check)

The `/v1/price` envelope carries `confidence` in [0, 1]; spot-check
during the first hour by querying a few popular pairs:

```sh
for pair in "native,fiat:USD" "USDC-G...,fiat:USD" ; do
  base=${pair%,*}; quote=${pair##*,}
  curl -s "https://api.stellarindex.io/v1/price?base=${base}&quote=${quote}" \
    | jq '{ pair: "'$pair'", confidence: .data.confidence,
            factors: .data.confidence_factors }'
done
```

**What healthy looks like**: ≥ 0.7 for the major XLM pairs. < 0.5
sustained is a soft signal a single-source-degradation or
divergence-spike is approaching alert thresholds.

## 10. Rate-limit fail-open events

```promql
rate(stellarindex_ratelimit_fail_open_total[$range])
```

Non-zero = Redis is misbehaving and the rate-limit middleware
fell open (per design, so we serve traffic at the cost of
unguarded rates). Should trend zero in steady state. Pair with
the Redis dashboard for root cause.

## 11. Closed-bucket stream subscriber health (L3.9)

```promql
sum by (outcome) (rate(stellarindex_api_stream_subscribe_total[$range]))
sum by (outcome) (rate(stellarindex_aggregator_stream_publish_total[$range]))
```

**What healthy looks like**: publisher `ok` and subscriber `ok`
rates track each other (within reconnection-noise); `error` /
`decode_error` / `malformed` near zero. A non-trivial gap means
fan-out is dropping events — investigate the Redis pub/sub
channel.

## 12. Trade inserts vs `usd_volume` populate ratio

```promql
sum by (source, usd_volume_populated)
  (rate(stellarindex_trade_inserts_total[$range]))
```

**What healthy looks like**: for each off-chain CEX/FX source, the
`yes` rate dominates. For on-chain sources, `yes` shows up only
for trades whose quote matches the operator's
`[trades].usd_pegged_classic_assets` allow-list — by design.

## Hooked into the launch-day checklist

The L6.7 first-24h watch in
[`launch-day-checklist.md`](launch-day-checklist.md) §T-0 step 7
points at this doc. The on-call keeps queries 1-7 + 11 in a tabbed
dashboard; queries 8-10 + 12 are pulled when something specific
needs investigation.

## Cross-references

- [`deploy/monitoring/README.md`](../../deploy/monitoring/README.md) — alert rules.
- [`docs/operations/launch-day-checklist.md`](launch-day-checklist.md) — cutover orchestration.
- [`docs/operations/sla-probe.md`](sla-probe.md) — formal SLA-evidence probe (15-min cron).
- [`docs/reference/metrics/README.md`](../reference/metrics/README.md) — every metric this doc references.
