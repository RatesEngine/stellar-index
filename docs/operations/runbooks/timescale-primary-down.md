---
title: Runbook — timescale primary down
last_verified: 2026-04-22
status: ratified
severity: P1
---

# Runbook — `ratesengine_timescale_primary_down`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_timescale_primary_down` |
| Severity | **P1** (SEV-1) |
| Detected by | Prometheus rule; also `api_price_stale` + `api_error_rate_critical` will light up once failover begins |
| Typical MTTR | 60 s automatic Patroni failover; ≤ 15 min if manual intervention needed |
| Impact | Writes halt everywhere. Reads continue from replicas but `stale_flag=true` for ~30–60 s during the failover window. New trade ingestion blocks until a new primary promotes. |

## Symptoms

- Alert `ratesengine_timescale_primary_down` fires — `up{role="primary"}` = 0 for > 30 s.
- API latency spikes on write endpoints (`POST /v1/account/keys` etc.) — reads from Redis hot path still work.
- `ratesengine_ingestion_cursor_stuck` lights up across all sources within ~60 s.
- Patroni dashboard: primary missing from cluster view.

## Quick diagnosis (≤ 5 min)

Confirm the primary is actually gone (not a monitoring blip):

```sh
# From ops jump host
patronictl -c /etc/patroni/patroni.yml list
# Expect: one "Leader" row and two "Replica" rows. If the Leader
# row is missing or status != running, primary is really down.

# If patronictl is also unreachable, check etcd:
etcdctl --endpoints=https://etcd-1.internal:2379 get /ratesengine/leader
# Should return the primary host. Empty = no leader elected.

# And directly poke the primary's psql:
PGCONNECT_TIMEOUT=3 psql -h db-primary.internal -U ratesengine \
  -d ratesengine -c 'SELECT pg_is_in_recovery();'
# Any error = primary unreachable.
```

If all three confirm the primary is gone → real incident, proceed to mitigation.
If only Prometheus alert fires + patronictl says healthy → likely a monitoring scrape issue. Check the Prometheus-node's health + `alertmanager` routing. Treat as P3 scrape failure, not P1.

## Mitigation (≤ 15 min)

### A. Automatic Patroni failover (the happy path)

Patroni should promote the synchronous replica automatically within 30–60 s. Watch it happen:

```sh
watch -n 2 'patronictl -c /etc/patroni/patroni.yml list'
# Look for a replica transitioning to "Leader" state.
```

Verify post-failover:

- [ ] `patronictl list` shows one Leader, one Replica.
- [ ] DNS / PgBouncer routes to new primary:
      `psql -h db-primary.internal -c 'SELECT inet_server_addr();'`
      returns the promoted host.
- [ ] Indexer logs resume inserting trades (`journalctl -u ratesengine-indexer -f`).
- [ ] Prometheus alert clears within 60 s of the new leader.

If automatic failover happened cleanly → continue to RCA.

### B. Failover stuck (Patroni didn't promote)

Happens when etcd quorum is also broken, or the sync replica isn't actually in sync.

First check etcd quorum:

```sh
etcdctl --endpoints=https://etcd-1.internal:2379,https://etcd-2.internal:2379,https://etcd-3.internal:2379 endpoint status --write-out=table
# At least 3 of 5 must be "healthy" for leader election to work.
```

If etcd is fine but Patroni refuses to promote:

```sh
# Manually promote a specific replica — only when sure it's
# sync-replica (not async). Forcing promotion of an async replica
# loses up to ~5s of writes.
patronictl -c /etc/patroni/patroni.yml failover ratesengine --candidate db-replica-sync.internal
# Confirm yes to the prompt.
```

### C. Both primary AND sync-replica gone (SEV-1 worsens)

- Declare the incident on status page — writes unavailable.
- Promote the async replica (in R3 per multi-region-topology.md §5.3):
  ```sh
  patronictl failover ratesengine --candidate db-replica-async.internal
  ```
  Accept the ≤ 5 s RPO data loss.
- On return, reconcile cursor state: the indexer's idempotent
  upserts (ON CONFLICT DO NOTHING) handle most duplicate writes.
  Check `ratesengine_ingestion_cursor_stuck` clears within 2 min.

### D. Complete cluster loss (asteroid scenario)

Refer to [`runbooks/dr-activation.md`](dr-activation.md). Out-of-scope for
this runbook — DR activation is a SEV-1 cross-cutting flip with
its own decision tree, pre-flight checks, and post-flip
validation procedure. This runbook covers single-component
failure modes (A/B/C above) where the cluster is recoverable.

## Root cause analysis

Gather for the postmortem:

- `journalctl -u patroni --since "1 hour ago"` on all three Patroni hosts.
- Postgres logs: `/var/log/postgresql/postgresql-15-main.log` from around the event.
- etcd logs: `journalctl -u etcd --since "1 hour ago"`.
- Grafana screenshot of the "Postgres primary" dashboard.
- Disk-space + IOPS metrics — was it full, was it OOMKilled?
- Recent deploys: anything touching `deploy/timescale-statefulset.yaml` or the Ansible `archival-node` role in the last 24 h?

Common root causes observed in similar systems:
1. **Disk full** — WAL couldn't write; Postgres halted writes. Catches: `ratesengine_timescale_disk_warning` fires BEFORE this one usually. If it didn't, tune thresholds.
2. **OOMKill** — Postgres process killed by the kernel. Check dmesg. Often means `shared_buffers` + `work_mem` × active_connections exceeded host RAM.
3. **Kernel / ZFS issue** — NVMe drive dropped, ZFS pool degraded. Catches: `ratesengine_zfs_pool_degraded` fires first.
4. **Runaway query** — a long-running SELECT blocked WAL recycling. pg_stat_activity shows it.
5. **Patroni config drift** — manual edit on one node disagreed with another; etcd-based decision got stuck.

## Known false-positive patterns

- **30–60 s scrape gap during Patroni upgrade**: rolling restart of patroni daemons can briefly show all roles as "unknown". Don't page — we schedule these in maintenance windows.
- **Network partition between Prometheus and Postgres**: `up` returns 0 but Postgres is fine. Confirm via direct `psql` before declaring.

## Related

- ADR-0006 (TimescaleDB) — storage choice.
- HA plan §3.3 — Patroni topology.
- Multi-region topology §5 — cross-region replication shape.
- Postmortems: `docs/operations/postmortems/` (none yet; first one goes here).

## Changelog

- 2026-04-22 — initial draft. @ash.
