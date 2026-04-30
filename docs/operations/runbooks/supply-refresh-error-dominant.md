---
title: Runbook — supply-refresh-error-dominant
last_verified: 2026-04-30
status: ratified
severity: P3
---

# Runbook — `ratesengine_aggregator_supply_refresh_error_dominant`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_aggregator_supply_refresh_error_dominant` |
| Severity | P3 (ticket) |
| Detected by | `deploy/monitoring/rules/supply-refresh.yml` |
| Typical MTTR | 15–60 min |
| Impact | `/v1/assets/{id}` F2 fields are stale OR wrong for affected assets — the previous snapshot stays in `asset_supply_history`, so consumers see correct-but-old data. |

## Symptoms

- `> 50%` of `ratesengine_aggregator_supply_refresh_total` ticks
  have `outcome != "ok"` for ≥ 30 min.
- Aggregator logs show repeated `supply refresh: <outcome>` lines
  with the same outcome label.

## Quick diagnosis (≤ 5 min)

```sh
# 1. Which outcome dominates?
curl -s http://aggregator:9464/metrics | \
  grep ratesengine_aggregator_supply_refresh_total | \
  sort -t' ' -k2 -rn | head

# 2. Per-asset breakdown? (Logs carry asset key; metric doesn't.)
sudo journalctl -u ratesengine-aggregator --since "30 min ago" -n 200 | \
  grep "supply refresh: " | sort | uniq -c | sort -rn | head

# 3. Sanity-check the aggregator config.
grep -A 10 "^\[supply" /etc/ratesengine.toml
```

## Typical root causes (split by dominant outcome)

### `outcome="no_ledger"`

The aggregator can't resolve the latest ledger from
`ingestion_cursors`. Either the indexer hasn't produced its
first cursor, or the table is unreachable.

- Signal: `_no_ledger` increments far exceed any other outcome.
- Mitigation: confirm the indexer is running + writing cursors;
  if storage is broken, route to `pg-conns-saturated.md`.

### `outcome="no_observation"`

The chain reader (live LCM) returned no observation for at
least one watched asset, AND the operator-static fallback was
also empty. Most common after a fresh deploy: the
AccountEntry observer hasn't backfilled to a deep enough range
yet.

- Signal: `_no_observation` increments dominate; per-asset logs
  identify which watched accounts are uncovered.
- Mitigation: either (a) wait for the observer's backfill to
  catch up (typical: hours-to-days for the configured
  watched-set), (b) populate the operator-static config blocks
  (`[supply.reserve_balances_stroops]`, `[metadata.issuer_home_domains]`)
  as a bridge until the observer covers the live set.

### `outcome="compute_error"`

The supply algorithm itself failed. For Algorithm 1 (XLM) this
means the reserve reader or the XLMComputer threw. For
Algorithm 2 (classic) it means one of the four component sums
failed. For Algorithm 3 (SEP-41) it means the kind-totals
query failed.

- Signal: `_compute_error` increments in the absence of
  no_observation / no_ledger.
- Mitigation: check the per-asset logs for the wrapped error;
  this is typically a code bug or a config inconsistency
  (asset not parseable, etc.). Roll back the binary if it's a
  recent deploy.

### `outcome="write_error"`

`Store.InsertSupply` failed. Postgres unreachable, NUMERIC
overflow on a malformed amount, etc.

- Signal: `_write_error` increments.
- Mitigation: confirm Postgres is reachable; check the
  asset_supply_history table for CHECK-constraint violations
  in the recent rows.

## Mitigation

- [ ] Step 1 — Identify dominant outcome via Quick diagnosis #1.
- [ ] Step 2 — Apply the matching root-cause fix from above.
- [ ] Step 3 — If `_no_observation` and the observer hasn't
      backfilled: this is expected during bootstrap. Wait
      OR populate the operator-static fallback config blocks.
- [ ] Verification: `outcome="ok"` rate exceeds error-outcome
      rate; alert clears within 30 min as the rolling window
      catches up.

## Known false-positive patterns

- **Bootstrap window after fresh deploy.** The
  `_no_observation` outcome dominates briefly while the
  AccountEntry observer (#298) backfills the watched accounts.
  The 30 min `for` clause typically absorbs this; longer
  bootstraps still trip it. Operators that anticipate this can
  silence the alert during deploy windows.

- **Per-asset bootstrap timing.** A new entry added to
  `[supply] watched_classic_assets` will produce
  `_no_observation` ticks until the observer has rows for the
  asset's locked-set members. The other watched assets continue
  to produce `_ok` ticks; the alert fires only when the error
  fraction exceeds 50%.

## Related

- `supply-refresh-stalled.md` — when no ticks are happening at all.
- ADR-0011, ADR-0021, ADR-0022, ADR-0023 — the algorithms +
  observer designs the refresher consumes.

## Changelog

- 2026-04-30 — initial draft alongside #313 (supply-refresh
  alerts).
