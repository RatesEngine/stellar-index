---
title: Runbook — db-disk-full
last_verified: 2026-04-23
status: draft
severity: P1
---

# Runbook — `ratesengine_timescale_disk_full` / `_disk_warning`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `_disk_full` (< 10 % free, SEV-1), `_disk_warning` (< 20 % free, ticket) |
| Severity | P1 (disk_full) / P2 (disk_warning) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 15–90 min |
| Impact | **WAL refuses to write once the disk is full.** Every write across the system halts — indexer insert-errors spike, API's `/v1/price` falls back to slow paths, every service readyz may go red. Act well before full. |

## Symptoms

- `/var/lib/postgresql` mount < 10 % (or < 20 % for warning)
  sustained 1 min (or 10 min for warning).
- Postgres logs: `could not extend file ... No space left on device`.
- Cascading: `insert-errors.md`, `replica-lag.md` (replica can't
  keep up if primary can't write), etc.

## Quick diagnosis (≤ 5 min)

```sh
# Capacity breakdown
ssh <pg-host> 'df -h /var/lib/postgresql'
ssh <pg-host> 'du -sh /var/lib/postgresql/*/pg_wal /var/lib/postgresql/*/base /var/lib/postgresql/*/pg_tblspc 2>/dev/null'

# Biggest hypertable / chunk
psql -c "SELECT hypertable_name, pg_size_pretty(hypertable_size(format('%I.%I', hypertable_schema, hypertable_name)::regclass))
         FROM timescaledb_information.hypertables
         ORDER BY hypertable_size(format('%I.%I', hypertable_schema, hypertable_name)::regclass) DESC;"

# Is there a retention / compression policy misfire?
psql -c "SELECT * FROM timescaledb_information.jobs WHERE job_id IN
         (SELECT job_id FROM timescaledb_information.job_stats WHERE last_run_status != 'Success');"

# pgBackRest backup stash eating space?
ssh <pg-host> 'du -sh /var/lib/pgbackrest'
```

## Typical root causes

1. **Compression policy isn't running.** Chunks older than 7 days
   should be compressed; if the job fails silently, raw chunks
   accumulate.
   - Signal: `compression-lag.md` is also firing.
   - Mitigation: `cagg-stale.md` and `compression-lag.md` explain
     the fix paths.

2. **Retention policy isn't running / not aggressive enough.**
   Old data we don't need is still there.
   - Mitigation: adjust `add_retention_policy(...)` interval.

3. **WAL fill-up.** A long-running transaction or a broken replica
   prevents WAL recycling. `pg_replication_slots` tells you if a
   slot is holding WAL.
   - Mitigation: advance or drop the stuck slot. Be careful —
     dropping a slot breaks the replica using it.

4. **Unbounded log growth** in the Postgres stderr log file
   (misconfig writing debug everywhere). `df` shows `/var/log/postgresql`
   as the culprit.
   - Mitigation: truncate the log + fix the verbosity.

5. **Someone loaded a large dataset** (backfill, import) without
   a retention policy covering it.

## Mitigation (fastest paths first)

- [ ] Step 1 — **create headroom NOW**. Don't investigate first.
      Quick wins:
      - Truncate a large stderr log: `sudo truncate -s 0 /var/log/postgresql/server.log`
      - Force a WAL archive + checkpoint: `psql -c "CHECKPOINT;"`
      - Drop old partitions you KNOW are safe: `CALL drop_chunks(...)`.

- [ ] Step 2 — if no quick win: scale the volume. For k8s-mounted
      PVCs, expand the PVC (requires `allowVolumeExpansion` on the
      StorageClass).

- [ ] Step 3 — once green again, investigate why the policies
      didn't keep up. Compression / retention are usually the
      real fix.

- [ ] Step 4 — verify WAL is recycling: `SELECT * FROM pg_stat_wal_archiver;`.

- [ ] Verification: free space > 30 % sustained; no `No space` errors
      in the last hour; replica caught up.

## Root cause analysis

- 30-day growth curve of `/var/lib/postgresql`.
- Compression / retention job success rate over the period.
- WAL generation rate — did it jump recently?
- Was there a backfill / migration that bloated the DB?

## Known false-positive patterns

- **Backup-in-progress** stages data on disk before uploading.
  Temporary blip; resolves when backup finishes. Size the staging
  buffer headroom above the 20 % warning threshold.
- **pg_repack** runs rebuild a table's storage; doubles it briefly.

## Related

- `insert-errors.md` — downstream when writes start failing.
- `compression-lag.md` — the policy that should be pruning space.
- `cagg-stale.md` — separate issue that often correlates.
- `replica-lag.md` — replica can't keep up if primary has stopped
  writing WAL.

## Changelog

- 2026-04-23 — initial draft. Emphasises "create headroom first,
  investigate second" — at the edge you can't afford to root-cause
  before acting.
