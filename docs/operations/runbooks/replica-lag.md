---
title: Runbook — replica-lag
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_timescale_replica_lag`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_timescale_replica_lag` |
| Severity | P2 (ticket) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 15–60 min |
| Impact | Depends on the replica's role. If it's the **sync** replica, writes on the primary are being held back until it catches up → indexer + aggregator insert-rate drops, cursor alerts may follow. If it's an async replica, the impact is weaker — only read-scaled queries see stale data. |

## Symptoms

- `pg_replication_lag_seconds > 5` sustained 2 min.
- `pg_stat_replication` on the primary shows a `write_lag` /
  `replay_lag` value > 5 s.
- If sync replica: primary has transactions in `wait_event_type =
  SyncRep`.

## Quick diagnosis (≤ 5 min)

```sh
# Primary's view of all replicas
psql -c "SELECT application_name, state, sync_state,
                pg_wal_lsn_diff(sent_lsn, replay_lsn) AS replay_bytes,
                replay_lag
         FROM pg_stat_replication;"

# Replica's view
ssh <replica-host> 'psql -c "SELECT now() - pg_last_xact_replay_timestamp() AS lag"'

# Replica system load — IO-bound, CPU-bound, network?
ssh <replica-host> 'iostat -x 1 5'
ssh <replica-host> 'top -b -n1 | head -10'

# Is the network link OK?
ssh <replica-host> 'ping -c 10 <primary>'
```

## Typical root causes

1. **Replica IO saturated** — can't replay WAL as fast as primary
   generates it. Usually means the replica's disk is slower than
   the primary's, or it's busy serving read traffic.
   - Mitigation: reduce read load on the replica; improve disk.

2. **Long-running query on the replica** blocking WAL apply
   (`hot_standby_feedback` or conflict). Read queries on the
   replica can indefinitely postpone WAL application.
   - Signal: `pg_stat_activity` on the replica shows `Startup
     process` waiting on a query.
   - Mitigation: cancel the offending query with
     `SELECT pg_cancel_backend(...)`.

3. **Network bandwidth saturated.** Happens during resilvering of
   the primary host's storage, or during a big backfill that
   produces WAL faster than the link can stream.

4. **Replica process wedged** — rare but possible, especially on
   older Postgres versions. Usually requires replica restart.

5. **max_wal_senders / wal_keep_size misconfig**. Primary has
   already recycled WAL the replica needed; replica can't catch
   up incrementally and needs a base-backup.
   - Signal: replica log says `requested WAL segment has already
     been removed`.
   - Mitigation: rebuild the replica from a fresh base-backup.

## Mitigation

- [ ] Step 1 — identify whether it's resource, query, network, or
      WAL-recycle (above).
- [ ] Step 2 — for resource: shed read load / scale disk.
- [ ] Step 3 — for query block: cancel the offender.
- [ ] Step 4 — for network: monitor for recovery; investigate
      whatever's hogging bandwidth.
- [ ] Step 5 — for WAL-recycle: rebuild the replica from
      base-backup — this is a longer procedure (hours).
- [ ] Verification: `pg_replication_lag_seconds` back under 1 s
      sustained 15 min.

## Root cause analysis

- Primary + replica system metrics across the window.
- `pg_stat_replication` timeline — when did the lag start
  growing?
- Was there a deploy, schema migration, or backfill around the
  event? Those produce WAL bursts.

## Known false-positive patterns

- **Burst write during a backfill** — lag rises during and
  subsides after. If expected, silence for the duration.
- **Replica restart** — briefly out of sync on startup until it
  catches up.
- **Asynchronous replica** during peak traffic — some async lag
  is expected; the 5 s alert threshold should cover normal cases
  but tune if you get chronic flap.

## Related

- `timescale-primary-down.md` — replica lag is often the precursor
  to a failover event.
- `db-disk-full.md` — no disk → no WAL → infinite lag.
- `pg-conns-saturated.md` — if the replica pool is saturated.

## Changelog

- 2026-04-23 — initial draft.
