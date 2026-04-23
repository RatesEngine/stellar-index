---
title: Runbook — cagg-stale
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_timescale_cagg_stale`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_timescale_cagg_stale` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 15–60 min |
| Impact | A continuous aggregate is > 5× its refresh interval overdue. `/v1/vwap`, `/v1/twap`, `/v1/ohlc` rely on these — API queries either read stale windows or fall back to raw aggregation (slow). Price data accuracy for aggregate endpoints degrades. |

## Symptoms

- `time() - ratesengine_cagg_last_refresh_unix > 5 * refresh_interval_seconds`
  for some `cagg` for ≥ 5 min.
- `SELECT * FROM timescaledb_information.job_stats` shows
  `last_run_status != 'Success'` or `last_finish` well behind
  expected.
- `/v1/vwap` responses have `observed_at` that doesn't track
  recent trades.

## Quick diagnosis (≤ 5 min)

```sh
# Who's stale and why?
psql -c "SELECT j.job_id, j.schedule_interval, s.last_run_status,
                s.last_run_duration, s.last_finish, s.last_run_err
         FROM timescaledb_information.jobs j
         JOIN timescaledb_information.job_stats s USING (job_id)
         WHERE j.proc_name = 'policy_refresh_continuous_aggregate'
         ORDER BY s.last_finish DESC NULLS FIRST;"

# Is the timescaledb scheduler even running?
psql -c "SELECT * FROM pg_stat_activity
         WHERE application_name LIKE '%timescale%';"

# Try a manual refresh — does it succeed?
psql -c "CALL refresh_continuous_aggregate('<cagg_name>', NULL, NULL);"
```

## Typical root causes

1. **Refresh job encountering an error** that gets swallowed into
   `last_run_status = 'Failed'`. Could be:
   - Source hypertable constraint violation (bad data snuck in)
   - Lock conflict with a concurrent vacuum/migration
   - Out-of-memory for a window function on a large window
   - Mitigation: read `last_run_err`; address the specific error.

2. **timescaledb-scheduler hung**. The background scheduler
   worker can wedge (rarely). Restart Postgres (or just the
   scheduler-related background worker).

3. **Refresh runs but takes longer than its schedule interval.**
   Each run starts before the previous finishes; the scheduler
   skips queued runs and the CAGG falls behind.
   - Mitigation: widen the schedule interval, or narrow the
     refresh window, or pre-aggregate upstream.

4. **Refresh window extends past current data.** If refresh is
   set to include `now()` with an `end_offset = 0`, it may skip
   windows that are still being written to.
   - Mitigation: add an `end_offset` ≥ largest expected
     late-arriving event gap.

## Mitigation

- [ ] Step 1 — read `last_run_err` for the specific error.
- [ ] Step 2 — manually refresh: `CALL refresh_continuous_aggregate(...)`
      to see if it's a one-off.
- [ ] Step 3 — fix the underlying error (schema / constraint /
      lock).
- [ ] Step 4 — once stable, re-enable the policy if it was
      disabled.
- [ ] Verification: job_stats shows `last_run_status = 'Success'`
      and `last_finish` within the schedule interval; alert clears.

## Root cause analysis

- `last_run_err` for every recent run.
- timescaledb version + known bug tracker.
- Were there schema changes on the source hypertable recently?
- Is the CAGG definition using a pattern known to be expensive
  (unbounded `time_bucket_gapfill` over very long windows)?

## Known false-positive patterns

- **Fresh CAGG creation** triggers this until the first refresh
  completes. Expected; the alert's `for: 5m` threshold helps.
- **Postgres restart** — scheduler doesn't start refreshing until
  a few seconds after startup; briefly skips a cycle.

## Related

- `api-latency.md` — downstream effect when VWAP queries fall
  back to raw aggregation.
- `price-stale.md` — aggregator staleness visible through the API.
- `pg-conns-saturated.md` — can cascade if the refresh is holding
  connections.

## Changelog

- 2026-04-23 — initial draft.
