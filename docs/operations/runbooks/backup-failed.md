---
title: Runbook — backup-failed
last_verified: 2026-05-03
status: draft
severity: P1
---

# Runbook — `ratesengine_timescale_backup_failed` / `_backup_none_24h`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `_backup_failed` (missed one expected cycle, ticket) / `_backup_none_24h` (no backup in 24h, **SEV-1**) |
| Severity | P2 (missed cycle) / P1 (24h gap) |
| Detected by | `deploy/monitoring/rules/storage.yml` |
| Typical MTTR | 30 min – 4 h |
| Impact | Increasing RPO. Our declared RPO is 5 min; at 24 h without a backup we are 288× over. If the primary dies in this window, we lose everything after the last good backup. |

## Symptoms

- `_backup_failed`: last successful backup > 2× the expected
  interval (typically we back up every 1 h → alert at > 2 h).
- `_backup_none_24h`: nothing successful in 24 h; pages directly.
- pgBackRest log: `ERROR: ...` — specific cause varies.

## Quick diagnosis (≤ 5 min)

```sh
# Most recent backup status
pgbackrest --stanza=main info
#   Look at: backup timeline, last backup status, repository size.

# Why did the last run fail?
ssh root@<patroni-leader> "journalctl -u pgbackrest-backup.service \
  --since '2 hours ago' --no-pager"

# Is the backup target (MinIO/S3) reachable?
mc ls myminio/pgbackrest/
mc admin info myminio

# Is primary Postgres healthy? (Backup requires primary access.)
psql -c "SELECT now(), pg_is_in_recovery();"
```

## Typical root causes

1. **Backup target auth / space issue.** Credential rotation
   missed pgBackRest; bucket full; bucket policy changed.
   - Mitigation: fix credentials; ensure bucket has space.

2. **Primary resource pressure** — backup can't get a backup
   slot / buffer. Rare but has happened during heavy write
   bursts.

3. **WAL archive fallen behind** and pgBackRest's
   `archive-async` queue filled up.
   - Signal: `pg_stat_archiver` shows high `failed_count`.
   - Mitigation: find why archive-push is failing — usually the
     same MinIO/S3 issue as above.

4. **pgBackRest version mismatch** between the pgBackRest binary
   and the repository format after a major upgrade.

5. **Scheduler didn't run.** CronJob failure, systemd timer
   disabled, k8s node unavailable at scheduled time.

## Mitigation

- [ ] Step 1 — immediate: run a manual backup to verify the
      system works:
      ```sh
      pgbackrest --stanza=main backup --type=diff
      ```
- [ ] Step 2 — if manual works: investigate scheduler.
- [ ] Step 3 — if manual fails: fix the specific error
      (credentials, network, space, version).
- [ ] Step 4 — for `_backup_none_24h`: **declare SEV-1**. This is
      an RPO breach. Additionally, immediately check that
      replication is healthy — replication is our *other* data
      safety net; if both are broken, we're on a tightrope.
- [ ] Step 5 — once healthy, schedule a full backup (not
      differential) to establish a known-good restore point.
- [ ] Verification: `pgbackrest info` shows a fresh successful
      backup within the expected interval.

## Root cause analysis

- Backup log from the last successful through the first failure.
- Storage backend logs (MinIO / S3) for the window.
- Any secret / config / binary upgrade around the failure time?
- RPO math: what data would have been lost if primary failed?

## Known false-positive patterns

- **Backup target under maintenance** — MinIO rolling restart,
  S3 region outage. Backup fails; resumes when storage recovers.
  The `_backup_failed` alert is reasonable; `_backup_none_24h`
  should not trigger unless the outage really is sustained.

## Related

- `db-disk-full.md` — if disk is full, WAL archive stops first,
  then backups fail.
- `timescale-primary-down.md` — primary must be reachable to
  take backups.
- HA plan §RPO-RTO: `docs/architecture/ha-plan.md`.

## Changelog

- 2026-04-23 — initial draft. Emphasises the RPO math —
  "missed one backup" and "no backup 24h" are different severity
  levels because the cost curve is non-linear.
