---
title: Runbook — price-stale
last_verified: 2026-05-03
status: draft
severity: P2
---

# Runbook — `ratesengine_api_price_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_api_price_stale` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/api.yml` |
| Typical MTTR | 15–60 min |
| Impact | `/v1/price?asset=<X>` returns a price but `observed_at` is stale. Clients get a real answer; it's just old. Envelope `stale=true` flag is set when we fell back to last-trade, but the gauge captures the underlying staleness even on the happy path. |

## Symptoms

- `ratesengine_price_staleness_seconds{asset=...} > 120` sustained 5 min.
- The affected asset's chart on the *Price → freshness* dashboard
  shows a flat line where observations should be ticking.
- `/v1/price?asset=<X>` returns 200 with a price but the
  `observed_at` timestamp is well behind wall-clock.

## Quick diagnosis (≤ 5 min)

```sh
# Which asset is stale?
curl -s http://api:9464/metrics |
  awk '/^ratesengine_price_staleness_seconds/ && $2 > 120 {print}'

# Is it one asset or many?
#   One asset → that asset's source is stopped / paused.
#   Many assets on one source → the source is stopped.
#   Many sources → the aggregator isn't writing (or isn't running).

# Which sources quote this asset?
psql -c "SELECT source, max(observed_at) AS most_recent
         FROM trades WHERE base = 'native:XLM' OR quote = 'native:XLM'
         GROUP BY source ORDER BY most_recent DESC;"

# Is the aggregator binary running and writing CAGGs?
ssh root@aggregator-01 "systemctl status ratesengine-aggregator --no-pager | head -10"
ssh root@aggregator-01 "curl -s http://localhost:9464/metrics | grep aggregator_writes_total"
```

## Typical root causes

1. **Source quoting this asset is stopped.** Events stop → no new
   trade → aggregator has nothing fresh to roll up → API serves
   the last trade with an aging `observed_at`.
   - Signal: `ratesengine_source_last_event_unix{source=<X>}` is
     frozen; `ratesengine_ingestion_source_stopped` alert may also
     have fired on that source.
   - Mitigation: `source-stopped.md`.

2. **Aggregator is running but not writing CAGGs / hot-cache**.
   Happens when CAGG refresh jobs fail (schedule misfire, SQL
   error in the window function).
   - Signal: `ratesengine_timescale_cagg_stale` alert.
   - Mitigation: `cagg-stale.md`.

3. **Redis TTL'd the hot-cache entry for a low-QPS asset.** The
   API-side gauge is only updated on request; a 2-min-stale reading
   for an asset nobody queried in that window is a measurement
   artifact, not a real data problem.
   - Signal: the asset is rare. The metric uses the Prometheus
     `change()` operator in the alert rule so truly untouched
     assets don't page — but a "queried once every 3 min" asset
     can. Tune `for:` duration or alert `change()` reading if this
     is chronic.

4. **Pair that doesn't really trade on-chain anymore.** Classic
   long-tail assets: the last legitimate trade was days ago. This
   is a *data reality* not a bug — but it exposes clients to very
   stale prices. Consider de-listing the asset from the API
   response or flagging `stale=true` in the envelope (we already
   do if fallback fired).

## Mitigation

- [ ] Step 1 — confirm which sources quote the asset, and which of
      them has stopped (see diagnosis above).
- [ ] Step 2 — if one source is dead: `source-stopped.md`.
- [ ] Step 3 — if the aggregator pipeline is the problem:
      `cagg-stale.md`.
- [ ] Step 4 — if there's genuinely no on-chain activity: decide
      with product whether to de-list or keep the stale number
      with `stale=true`.
- [ ] Verification: `ratesengine_price_staleness_seconds{asset=<X>}`
      drops back under 120 s and the alert clears (`for: 5m` gives
      you time to verify it's not a flap).

## Root cause analysis

- Which asset(s) were stale — was it a pattern (all tokens from
  one issuer, all pairs quoted by one source)?
- Aggregator logs covering the window — any errors, any refresh
  lag?
- Source `health()` reports from the orchestrator — was it
  throwing soft errors (decode-errors up) while still running?

## Known false-positive patterns

- **Freshly-added assets**: the first time a new asset is queried,
  the gauge fires with a large staleness reading because the first
  observation just landed. The `for: 5m` absorbs this.
- **Chain halt**: if Stellar mainnet itself stops producing ledgers
  (rare), every asset goes stale simultaneously — correlates
  strongly with `core-lag.md` / `rpc-lag.md`. The price-stale alert
  in this case is redundant noise; the core/rpc alert is the real
  one.

## Related

- `source-stopped.md` — when a specific source has stopped dispatching.
- `cagg-stale.md` — when aggregation jobs fail to refresh.
- `oracle-stale.md` — oracle-specific staleness.
- HA plan §9 degradation envelope: `docs/architecture/ha-plan.md`.

## Changelog

- 2026-04-23 — initial draft. Follows the pattern where per-asset
  gauges only update on request; document this explicitly so
  future responders don't chase phantom staleness on untouched
  assets.
