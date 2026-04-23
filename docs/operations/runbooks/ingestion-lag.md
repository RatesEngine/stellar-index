---
title: Runbook — ingestion-lag
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_ingestion_lag_high`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_ingestion_lag_high` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/ingestion.yml` |
| Typical MTTR | 15–60 min (backfill), longer if the bottleneck is write-side |
| Impact | A specific source is > 1000 ledgers behind the tip. At ~5 s per ledger that's ~1.4 h of freshness debt for the assets that source quotes. `/v1/price` for those assets will serve older `observed_at`s; aggregates including that source will under-weight its recent activity until it catches up. |

## Symptoms

- `ratesengine_source_lag_ledgers{source=<X>} > 1000` for ≥ 10 min.
- `source_last_event_unix` still advancing (so the source isn't
  stopped — it's just slow).
- `price-stale.md` may also fire for assets that source quotes.

## Quick diagnosis (≤ 5 min)

```sh
# Who's behind and by how much?
curl -s http://indexer:9464/metrics |
  awk '/ratesengine_source_lag_ledgers/ && $2 > 100 {print}'

# Is the source's processing rate > production rate?
#   Production: ~1 ledger/5 s = 0.2 ledger/s
#   If the source processes < 0.2 ledger/s it'll never catch up.
curl -s http://prometheus:9090/api/v1/query --data-urlencode \
  'query=rate(ratesengine_source_events_total{source="<X>"}[5m])'

# Is the RPC also lagging? If so, we can't catch up faster than it.
ratesengine-ops rpc-probe http://stellar-rpc:8000

# Is persistence the bottleneck (insert errors rising)?
curl -s http://indexer:9464/metrics | grep insert_errors_total
```

## Typical root causes

1. **We're behind because we just restarted.** The orchestrator
   resumes from `last_ledger` in `ingestion_cursors`; if that's
   hours behind (deployment took time, replay is slow), expected
   until catchup finishes.
   - Signal: lag shrinking over time — catchup in progress.
   - Mitigation: wait. Watch the rate of shrinkage to estimate ETA.

2. **The source is processing slower than the chain produces.**
   Heavy per-event work (decode + correlate + persist + metric
   emit) can fall behind when pair counts grow.
   - Signal: ledger-per-second rate on the source is flat at or
     below the production rate.
   - Mitigation: profile the decoder; parallelize persistence
     (batched `INSERT` vs per-row). Requires a code PR.

3. **Write-side bottleneck.** Timescale primary is slow (locks,
   WAL pressure, disk IO).
   - Signal: insert-rate drops but decode-rate stays normal;
     backend time in `pg_stat_activity` shows `IO:DataFileRead`
     or `Lock:tuple` waits.
   - Mitigation: `pg_conns-saturated.md` / `replica-lag.md` /
     `db-disk-full.md` depending on the underlying storage issue.

4. **Upstream RPC is lagging.** We can process at any speed we
   like; if the RPC is 10 min behind wall-clock, we look 10 min
   lagged.
   - Signal: `rpc_latest_ledger_age_seconds` also elevated.
   - Mitigation: `rpc-lag.md`.

## Mitigation

- [ ] Step 1 — separate "behind and catching up" from "behind and
      staying behind" (plot lag over 15 min).
- [ ] Step 2 — if catching up: wait, estimate ETA from shrinkage.
- [ ] Step 3 — if stuck: find the bottleneck (RPC / persistence /
      decoder) and follow the linked runbook.
- [ ] Step 4 — if the gap is large and we want to skip ahead:
      `ratesengine-ops detect-gaps` + manual backfill range. Not
      currently automated (`TODO(#0)`).
- [ ] Verification: lag drops below 100 ledgers sustained 15 min.

## Root cause analysis

- When did lag start growing — correlate with deploys, RPC
  outages, schema migrations.
- Was only one source affected, or multiple? Multiple = shared
  upstream/storage problem. One = source-specific code/schema.
- For source-specific issues, inspect recent golden-file test
  fixtures — is the real event shape drifting from our decoder?

## Known false-positive patterns

- **Post-deploy catchup** triggers the alert if the deploy-gap >
  1000 ledgers. Silence during known-long deploys.
- **Paused sources** (operator-disabled via `enabled_sources`)
  don't update `source_enabled`, so the `and on(source) source_enabled == 1`
  qualifier keeps them out of this alert. If you see an alert for
  a source you thought was disabled, your gauge wiring is wrong
  — fix that first.

## Related

- `source-stopped.md` — the "stopped" variant.
- `rpc-lag.md` — if the upstream is the bottleneck.
- `insert-errors.md` — if persistence is failing (not just slow).
- `cursor-stuck.md` — when the cursor doesn't advance at all.

## Changelog

- 2026-04-23 — initial draft.
