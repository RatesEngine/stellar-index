---
title: r2 archival node — current state and next-steps
last_verified: 2026-05-03
status: skeleton — fill in after L4.14 provisioning
---

# r2-01 (us-east-1) deployment state

> **Skeleton.** R2 has not yet been provisioned at the time this
> file was written. The L4.14 row in
> [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md)
> tracks the spinup. Replace the `{{TBD}}` placeholders below as
> the operator works through
> [`multi-region-cutover.md` §Stage 1](multi-region-cutover.md)
> and flips the row 🔴 → ✅. Mirrors the shape of
> [`r1-deployment-state.md`](r1-deployment-state.md) so a reader
> can compare per-region differences at a glance.

Snapshot of what's running on the r2 host (AWS EC2; public IP +
security group IDs in `configs/ansible/inventory/r2.yml`,
gitignored). Updated at each session.

> **Bringing up r2 from scratch?** Follow
> [`archival-node-bringup.md`](archival-node-bringup.md)
> §"Per-region variations — R2" + the multi-region orchestration
> in [`multi-region-cutover.md`](multi-region-cutover.md). This
> doc is a snapshot of the running node, not a how-to.

## Hardware

- {{TBD: e.g. AWS EC2 r7i.4xlarge — 16 vCPU / 128 GiB}}
- {{TBD: EBS gp3 sizing — 1-2 TB target per ADR-0016 §"R2"}}
- AWS us-east-1, AZ {{TBD}}
- {{TBD: Ubuntu / Amazon Linux release}}

## Disk layout

EBS volumes (no ZFS — EBS provides durability + snapshot directly
per ADR-0016 §"R2 — AWS-hybrid"):

```
{{TBD: e.g.
/dev/nvme0n1   8 GB   gp3    /
/dev/nvme1n1  100 GB  gp3    /var/lib/postgresql
/dev/nvme2n1  500 GB  gp3    /var/lib/galexie     (galexie-live only)
/dev/nvme3n1   50 GB  gp3    /var/lib/minio        (galexie-live mirror)
}}
```

**No `/var/lib/galexie/galexie-archive` mountpoint** — galexie
reads `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`
direct per ADR-0016. Saves ~5 TB of EBS we don't need.

## Services (systemd)

| Service | State | Notes |
|---|---|---|
| postgresql@15-main | {{TBD}} | Patroni-managed; replica of R1's primary (sync via `synchronous_commit=remote_apply`). |
| galexie | {{TBD}} | Reads `s3://aws-public-blockchain` direct; writes galexie-live to local MinIO. |
| ratesengine-indexer | {{TBD}} | Reads MinIO for galexie-live + AWS S3 for galexie-archive. |
| ratesengine-aggregator | {{TBD}} | Standby until Patroni elects R2 leader. |
| ratesengine-api | {{TBD}} | Serves regional traffic via `api-r2.ratesengine.net`. |
| minio | {{TBD}} | galexie-live only (no archive mirror). |
| node_exporter | {{TBD}} | :9100 |
| node-healthcheck.timer | {{TBD}} | 5-min push to Healthchecks.io UUID {{TBD}}. |

## verify-archive tier coverage

Per ADR-0016 §"R2 verification capability":

- **Tier A** (chain integrity, no external data needed) — runs
  weekly on cron. Status: {{TBD}}
- **Tier D** (multi-peer HTTPS cross-check) — runs weekly on
  cron. Status: {{TBD}}
- **Tier B + E** delegated to R1 (the integrity leader). R2
  trusts R1's verification artefacts.

## Architecture

```
{{TBD — fill in once provisioned. Expected shape per
ADR-0016 §"R2":}}

aws-public-blockchain S3 ─┐ (free egress, sub-15ms RTT)
                          │
                          ▼
              galexie (configured to read from s3://...)
                          │
                          ▼
              MinIO galexie-live (local, EBS-backed)
                          │
                          ▼
              cmd/ratesengine-indexer
                          │
                          ▼
              TimescaleDB (replica from R1; sync replication)
                          │
                          ▼
              `/v1/{price,vwap,...}` API
```

## DNS

- `api-r2.ratesengine.net` → R2's HAProxy frontend IP / ALB
  (Cloudflare DNS-only, grey cloud — the public-facing record
  `api.ratesengine.net` is the proxied one)
- {{TBD: ALB DNS name if using AWS ALB instead of HAProxy}}

## Cross-references

- [`r1-deployment-state.md`](r1-deployment-state.md) — sibling
  doc for R1; compare for per-region differences.
- [`r3-deployment-state.md`](r3-deployment-state.md) — sibling
  doc for R3.
- [`multi-region-cutover.md`](multi-region-cutover.md) §Stage 1
  — the bringup procedure that fills in this skeleton.
- [`archival-node-bringup.md`](archival-node-bringup.md) §"Per-
  region variations — R2 — AWS-hybrid" — per-host details.
- [ADR-0016](../adr/0016-per-region-storage-strategy.md) §"R2 —
  AWS-hybrid" — design intent.
- L4.14 in [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md).
