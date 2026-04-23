---
title: Runbook — pg-conns-saturated
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_timescale_connections_saturated`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_timescale_connections_saturated` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 15–60 min |
| Impact | > 80 % of `max_connections` in use. New requests start queuing in PgBouncer (or failing with `sorry, too many clients` if PgBouncer isn't in the path). API latency rises; indexer may fail `pgx.Acquire`. |

## Symptoms

- `(pg_stat_activity_count / pg_settings_max_connections) * 100 > 80`
  for ≥ 5 min.
- API latency climbs (connection acquisition becomes the bottleneck).
- Postgres log shows `FATAL: sorry, too many clients already` if
  we've hit 100 %.
- PgBouncer's `SHOW POOLS` / `SHOW STATS` shows `cl_waiting` > 0.

## Quick diagnosis (≤ 5 min)

```sh
# What's the state distribution?
psql -c "SELECT state, wait_event_type, count(*)
         FROM pg_stat_activity
         GROUP BY 1, 2
         ORDER BY count DESC;"

# Who's the top holder?
psql -c "SELECT application_name, count(*)
         FROM pg_stat_activity
         WHERE state != 'idle'
         GROUP BY application_name
         ORDER BY count DESC;"

# Long-running transactions (common cause of accumulation)
psql -c "SELECT pid, application_name, state, wait_event_type,
                now() - xact_start AS xact_age, left(query, 80)
         FROM pg_stat_activity
         WHERE xact_start IS NOT NULL
         ORDER BY xact_age DESC
         LIMIT 10;"

# PgBouncer view (if in the path)
psql -h pgbouncer -p 6432 -U pgbouncer pgbouncer -c 'SHOW POOLS;'
```

## Typical root causes

1. **Long-running transaction leaking connections.** A handler
   opens a transaction, does work, but never commits or rolls
   back because of an unhandled error path.
   - Signal: `xact_age` > 10 min on several connections from one
     `application_name`.
   - Mitigation: kill the offending pids:
     `SELECT pg_terminate_backend(pid)`; fix the code.

2. **PgBouncer pool too small** for incoming demand. If the pool
   is `pool_size=10` and 20 clients want to query, 10 queue up.
   - Mitigation: increase `pool_size` (within Postgres's
     `max_connections` budget).

3. **Connection leak in the API** — goroutines that never release
   their `*sql.Conn`. Usually shows as a steadily climbing count
   from the API binary.
   - Mitigation: restart the API binary; fix the leak with
     pprof/runtime/pgx introspection.

4. **Burst load** — a marketing campaign, a viral asset, a big
   external caller. Legitimate; the fix is scale, not cleanup.

5. **Idle-in-transaction timeout not configured.** Without a
   timeout, a broken client's leaked transactions accumulate
   forever.
   - Mitigation: `SET idle_in_transaction_session_timeout = '5min'`
     at the role or db level.

## Mitigation

- [ ] Step 1 — identify the top holder (above).
- [ ] Step 2 — if long-running xact: terminate, then fix the code.
- [ ] Step 3 — if pool-size: bump PgBouncer pool (don't just bump
      Postgres `max_connections` — that's expensive per-connection
      in memory).
- [ ] Step 4 — if genuine growth: scale the DB or the pool.
- [ ] Verification: utilization drops under 50 % sustained; no
      more `too many clients` errors in logs.

## Root cause analysis

- Which application name was the offender?
- Timeline of connection growth — steady climb (leak) or spike
  (burst)?
- Correlated API metrics — did request rate rise in lockstep?

## Known false-positive patterns

- **Backup process** briefly opens many connections (pgBackRest
  `--process-max`). Expected; narrow to backup window.
- **Brief spike during an outage of a downstream service**
  (Redis) — API handlers take longer, so connection hold-time
  rises, so utilization spikes. Resolves when the downstream
  recovers.

## Related

- `api-latency.md` — upstream symptom.
- `replica-lag.md` — if the saturation is on the replica, not
  primary.
- `db-disk-full.md` — writes blocked → xacts can't commit →
  connections hold.

## Changelog

- 2026-04-23 — initial draft.
