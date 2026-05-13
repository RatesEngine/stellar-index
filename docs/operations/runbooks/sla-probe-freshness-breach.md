---
title: Runbook — sla-probe-freshness-breach
last_verified: 2026-05-03
status: ratified
severity: P2
---

# Runbook — `ratesengine_sla_probe_freshness_breach`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_sla_probe_freshness_breach` |
| Severity | P2 (page) |
| Detected by | `deploy/monitoring/rules/sla-probe.yml` |
| Typical MTTR | 30–90 min |
| Impact | Customer-visible — the Freighter detail page shows stale prices. RFP target is `observed_at` ≤ 30 s; sustained breach means the wallet's price column is out of date. |

## Symptoms

- `ratesengine_sla_probe_freshness_sec{endpoint="price"} > 30`
  for ≥ 30 min.
- The probe's JSON report shows the `observed_at` returned by
  `/v1/price` (or `/v1/oracle/latest` if that endpoint also has
  freshness) is more than 30 s behind wall-clock.
- `flags.stale: true` will be set on responses for the affected
  pair — clients gating on this flag are likely backing off, but
  many clients don't gate and surface the stale value as-is.

## Quick diagnosis (≤ 5 min)

The freshness chain has three stages — bisect by which one's lagging:

```sh
# 1. Pull the probe's JSON report; note observed_at + the wall-clock delta.
# F-1309 (codex audit-2026-05-13): unit name is `ratesengine-sla-probe`,
# not `sla-probe`; the legacy name pre-dates the systemd unit rename.
sudo journalctl -u ratesengine-sla-probe.service -n 1 --output=cat | jq '.per_endpoint[] | select(.endpoint=="price")'

# 2. Direct API check on the same pair.
curl -s 'https://api.ratesengine.net/v1/price?asset=native&quote=fiat:USD' | jq

# 3. What does Redis say for the cache key?
# F-1309: aggregator writes `vwap:<base>:<quote>:<window-seconds>`,
# not a single `price:<base>:<quote>` key. Check each window
# explicitly — TTLs differ (300s = 5m bucket, 3600s = 1h, 86400s = 1d).
redis-cli GET 'vwap:native:fiat:USD:300'
redis-cli GET 'vwap:native:fiat:USD:3600'
redis-cli GET 'vwap:native:fiat:USD:86400'

# 4. What does Postgres say is the latest closed bucket?
# F-1309: timestamp column is `bucket`, NOT `ts` — that's the
# trades-table column.
psql -c "SELECT bucket, vwap FROM prices_1m
         WHERE base_asset='native' AND quote_asset='fiat:USD'
         ORDER BY bucket DESC LIMIT 5;"
```

If Postgres has fresh buckets but Redis is stale → aggregator
isn't writing the cache. If Postgres is also stale → ingestion
side. If both are fresh but the API returns stale → API is
reading the wrong key.

## Typical root causes (roughly in frequency order)

1. **Aggregator orchestrator down or wedged.** No one is writing
   the `vwap:<base>:<quote>:<window>` cache keys, so `/v1/price`
   falls back to the Postgres read which has the right value but
   slower path; freshness lags as the aggregator's tick gap grows.
   - Signal: `rate(ratesengine_aggregator_ticks_total[5m]) == 0`
     (the [aggregator-silent](aggregator-silent.md) alert fires
     on this directly via `ratesengine_aggregator_vwap_writes_total`).
   - Mitigation: restart the aggregator binary; investigate why
     it stopped.

2. **Indexer lag**. The dispatcher is behind on LCM consumption,
   so even fresh ledger data isn't producing closed buckets.
   - Signal: `time() - ratesengine_source_last_event_unix > 30`
     for the dominant sources (the
     [source-stopped](source-stopped.md) alert covers the
     per-source case via `rate(ratesengine_source_events_total[5m]) == 0`).
   - Mitigation: see `core-lag.md`.

3. **CAGG refresh policy is paused or lagging.** The `prices_1m`
   CAGG isn't materializing recent buckets even though raw trades
   are present.
   - Signal: `ratesengine_timescale_cagg_stale` fires too.
   - Mitigation: see `cagg-stale.md`.

4. **No trades for the asset in the last 30 s.** Legitimate market
   quiet — the asset just hasn't traded. The "stale" flag is
   correct, not a bug.
   - Signal: trade-count panel for the pair shows zero recent rows.
   - Mitigation: this is expected; mark the alert as ack'd if the
     asset is known-thin.

## Mitigation

- [ ] Step 1 — Bisect via Quick diagnosis to identify the lagging
      stage.
- [ ] Step 2 — Route to the stage-specific runbook
      (aggregator / indexer / CAGG).
- [ ] Step 3 — If "no trades in window" — this is honest staleness.
      Confirm the pair is genuinely quiet and ack the alert.
- [ ] Verification: probe `freshness_sec` drops back under 30 for
      ≥ 30 min.

## Known false-positive patterns

- **Newly-listed asset** with low trading volume. Freshness can
  easily exceed 30 s if no one's trading the pair. Consider adding
  the asset to a "thin-pair allowlist" if this pattern is
  expected to persist.
- **Maintenance windows** — if the indexer or aggregator is
  intentionally paused for migration, the probe will fire.
  Pre-silence the alert before the maintenance window.

## Related

- `cagg-stale.md` — Postgres-side staleness.
- `core-lag.md` — indexer-side lag.
- `aggregator-silent.md` — orchestrator not writing.
- Freighter RFP V1 § Pricing — the 30s spec.

## Changelog

- 2026-04-30 — initial draft alongside #294 (alert rules).
