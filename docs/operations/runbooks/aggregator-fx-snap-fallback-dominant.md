---
title: Runbook — aggregator-fx-snap-fallback-dominant
last_verified: 2026-05-01
status: draft
severity: P3
---

# Runbook — `ratesengine_aggregator_fx_snap_fallback_dominant`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_fx_snap_fallback_dominant` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/aggregator.yml` |
| Typical MTTR | 15 min – 2 h |
| Impact | Chained-fiat triangulation (e.g. XLM/EUR via XLM/USD × USD/EUR) is using the cached-VWAP-of-FX path instead of the bucket-end snap mandated by ADR-0018. Output is functional but loses across-region byte-identical determinism — multi-region readers may see slightly different values for the same closed bucket. |

## Background

The X2.5 forex-factor snap rule (Task #71) requires the FX leg of any
chained-fiat triangulation to use the most recent FX-source quote
at-or-before the bucket-end timestamp, not the rolling-window VWAP.
ADR-0018 §"Forex factor handling" mandates this for across-region
consistency: every region serving the same closed bucket queries the
same trades hypertable and gets the same row, so the chained rate is
identical across regions.

When the snap path returns `ErrNoFXQuote` (no FX row at-or-before the
bucket end), the orchestrator falls back to the cached VWAP path. This
keeps the chain publishing — degraded but functional — and increments
`ratesengine_aggregator_fx_snap_fallback_total{leg=...}`.

The alert fires when **>50% of triangulations** fall back for **30+
minutes**. Brief spikes (single tick post-restart, FX-source lag in
the seconds) clear themselves; sustained dominance means FX ingestion
is unhealthy.

## Symptoms

- `sum(rate(ratesengine_aggregator_fx_snap_fallback_total[15m])) /
  sum(rate(ratesengine_aggregator_triangulations_total{outcome="ok"}[15m])) > 0.5`
  for 30+ minutes.
- Multi-region operators may report tiny chained-fiat rate differences
  for the same `bucket_end_ts` that they used to see byte-equal.
- `/v1/price` chained-fiat responses still serve normally — degraded,
  not down.

## Quick diagnosis (≤ 5 min)

```sh
# 1) Which legs are falling back? Cardinality is bounded, so the per-leg
#    counter directly names the affected pair(s).
curl -fs http://localhost:9464/metrics \
  | grep '^ratesengine_aggregator_fx_snap_fallback_total'

# 2) Is the trades hypertable getting fresh FX rows at all?
psql -d ratesengine -c \
  "SELECT source, base_asset, quote_asset, max(ts) AS latest
   FROM trades
   WHERE source IN ('polygon-forex', 'exchangeratesapi')
   GROUP BY source, base_asset, quote_asset
   ORDER BY latest DESC LIMIT 20;"

# 3) Is the orchestrator's bucket-end timestamp ahead of the latest FX
#    row by a wide margin? (snap returns ErrNoFXQuote if cutoff < first row)
psql -d ratesengine -c \
  "SELECT now() - max(ts) AS lag
   FROM trades
   WHERE source IN ('polygon-forex', 'exchangeratesapi');"
```

Decision tree:

| Latest FX row | Lag | Probable cause | Mitigation |
| ------------- | --- | -------------- | ---------- |
| Recent (<5 min) | OK | Snap-rule logic bug — alert is real but FX is healthy | File an issue; check recent commits to `internal/aggregate/orchestrator/triangulate.go` |
| Stale (5–30 min) | Connector lag | One FX source paused, others keeping up | Check connector logs for the affected venue; usually self-heals |
| Very stale (>30 min) | Connector down | Both FX sources failing simultaneously | Check `ratesengine_source_events_total{source=~"polygon-forex|exchangeratesapi"}` — if zero, the connector(s) are dead |
| No rows at all | Fresh deploy | FX ingestion never started | Verify the indexer's external-source config has FX entries enabled |

## Mitigation (≤ 15 min)

- [ ] **Single FX source down**: confirm the other one is publishing.
      If yes, the snap query still hits — alert may be a false positive.
      If both are down, escalate to the connector owner.
- [ ] **Both FX sources down**: this is the hard outage case. Chain
      output continues via cached-VWAP fallback so consumer impact is
      "values are slightly stale, multi-region drift" rather than
      "values are missing." Restore at least one FX feed before the
      cached values themselves expire (default 1h TTL).
- [ ] **Stale rows in hypertable but connector says it's running**:
      check the indexer-to-Timescale write path. The trades hypertable
      should receive a row per FX poll cadence (typical: 1/min); if
      `ratesengine_source_events_total` increments but the table
      doesn't grow, the writer is stuck.
- [ ] **Verification**: fallback rate returns to near-zero within 30m
      of restoring FX ingestion.

## Root cause analysis

Capture for the postmortem:

- A 1-hour metric range showing the fallback spike + recovery.
- The FX-source connector logs across the incident window.
- Trade-table activity for the affected FX pair across the same
  window — when did the row count plateau, when did it resume?
- Whether across-region readers actually observed value drift during
  the window (the alert exists to prevent silent drift, so confirming
  drift OR lack of it is a useful postmortem datum).

## Known false-positive patterns

- **First 30 min after a fresh deploy**: FX ingestion is bootstrapping
  and the snap path won't have rows for early buckets. Suppress the
  alert during the first 30 min after a clean Timescale start.
- **Aggregator restart followed by FX-source restart**: brief flurry
  of fallbacks while the connector reconnects; usually clears in
  ≤ 5 min and never reaches the 30-min `for:` clause.
- **Bucket-end timestamp at exactly the latest FX row's `ts`**: the
  query is `<=`, so this hits — but if a region's clock is ahead of
  the FX source's publish time by even one second, the cutoff goes
  past the latest row and the next bucket's snap misses. Cross-region
  clock skew of >1 s is its own alert (chrony/timesyncd); investigate
  there if this pattern is observed.

## Related

- ADR-0018 §"Forex factor handling" — why the snap rule exists.
- `aggregator-silent.md` — fires when even the cached-VWAP fallback
  path produces zero writes (full ingestion outage, not just FX).
- `internal/aggregate/orchestrator/triangulate.go` — snap-path
  implementation. Any change to FX-leg detection or fallback logic
  must update this runbook.
- `internal/storage/timescale/trades.go::FXQuoteAtOrBefore` — the
  storage primitive backing the snap query.

## Changelog

- 2026-05-01 — initial draft alongside the X2.5 snap-rule
  implementation (Task #71).
