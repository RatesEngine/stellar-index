---
title: r3 archival node — current state and next-steps
last_verified: 2026-05-03
status: skeleton — fill in after L4.15 provisioning
---

# r3-01 (Singapore) deployment state

> **Skeleton.** R3 has not yet been provisioned at the time this
> file was written. The L4.15 row in
> [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md)
> tracks the spinup. Replace the `{{TBD}}` placeholders below as
> the operator works through
> [`multi-region-cutover.md` §Stage 2](multi-region-cutover.md)
> and flips the row 🔴 → ✅. Mirrors the shape of
> [`r1-deployment-state.md`](r1-deployment-state.md) so a reader
> can compare per-region differences at a glance.

Snapshot of what's running on the r3 host (Vultr Bare Metal
Singapore; public IP held in `configs/ansible/inventory/r3.yml`,
gitignored). Updated at each session.

> **Bringing up r3 from scratch?** Follow
> [`archival-node-bringup.md`](archival-node-bringup.md)
> §"Per-region variations — R3" + the multi-region orchestration
> in [`multi-region-cutover.md`](multi-region-cutover.md). This
> doc is a snapshot of the running node, not a how-to.

## Hardware

- Intel Xeon E-2388G (8 cores / 16 threads)
- 128 GB DDR4 ECC
- 2 × 1.92 TB local NVMe
- Vultr Bare Metal, Singapore datacenter
- {{TBD: Ubuntu release}}

## Disk layout

```
nvme0n1 / nvme1n1 : ZFS mirror across both NVMes
                     p1  /boot/efi  512M  (per-drive vfat, not RAID)
                     p2  swap       4G    (md0, raid1)
                     p3  /          50G   (md1, raid1)
                     p4  ~1.92T     (ZFS mirror vdev)
```

ZFS pool `data` (mirror, ~1.92 TB usable — single-drive failure
tolerance) with these datasets:

- `data/os` → `/var/lib/ratesengine`
- `data/postgres` → `/var/lib/postgresql` (recordsize=8K, logbias=throughput)
- `data/galexie` → `/var/lib/galexie` (galexie-live + captive-core state only)
- `data/minio` → `/var/lib/minio` (galexie-live mirror)

**No `data/archive` dataset** — R3's galexie-archive (~4.76 TB)
lives on **Vultr Object Storage** per ADR-0016 §"R3 — Vultr-hybrid"
(~$25/mo for 5 TB, region-local to Singapore at sub-10ms RTT).

R1's raidz2 (two-drive failure tolerance) is the integrity
leader; R3's mirror (single-drive tolerance) is acceptable for
an async DR replica.

## Services (systemd)

| Service | State | Notes |
|---|---|---|
| postgresql@15-main | {{TBD}} | Patroni-managed; **async** replica of R1's primary (160-200ms RTT forces async per multi-region-topology.md). |
| galexie | {{TBD}} | Reads from Vultr Object Storage `s3://{{bucket}}` (configured in `r3.yml`); writes galexie-live to local MinIO. |
| ratesengine-indexer | {{TBD}} | Reads local MinIO for galexie-live + Vultr Object Storage for galexie-archive. |
| ratesengine-aggregator | {{TBD}} | Standby (R1 is leader at launch; failover scenarios elect R2 ahead of R3 per Patroni priority). |
| ratesengine-api | {{TBD}} | Serves regional traffic via `api-r3.ratesengine.net`. |
| minio | {{TBD}} | galexie-live only. |
| node_exporter | {{TBD}} | :9100 |
| node-healthcheck.timer | {{TBD}} | 5-min push to Healthchecks.io UUID {{TBD}}. |

## verify-archive tier coverage

Per ADR-0016 §"R3 verification capability":

- **Tier A** (chain integrity, no external data needed) — runs
  weekly on cron. Status: {{TBD}}
- **Tier D** (multi-peer HTTPS cross-check) — runs weekly on
  cron. Status: {{TBD}}
- **Tier B + E** delegated to R1 (the integrity leader). R3
  trusts R1's verification artefacts.

## Architecture

```
{{TBD — fill in once provisioned. Expected shape per
ADR-0016 §"R3":}}

Vultr Object Storage (galexie-archive, region-local)
                          │
                          ▼
              galexie (configured to read from Vultr S3 endpoint)
                          │
                          ▼
              MinIO galexie-live (local, ZFS-backed)
                          │
                          ▼
              cmd/ratesengine-indexer
                          │
                          ▼
              TimescaleDB (async replica from R1)
                          │
                          ▼
              `/v1/{price,vwap,...}` API
```

## Initial Vultr Object Storage bucket fill

R3's galexie-archive is **not** "download all 4.76 TB to local
disk" — it's "configure galexie to read from Vultr Object Storage
on demand". The initial bucket fill is a one-shot copy from AWS
public bucket → Vultr, ~6-12h depending on Vultr's ingress
bandwidth. Procedure:

```sh
# On any operator workstation with rclone configured:
rclone copy aws-public:aws-public-blockchain/v1.1/stellar/ledgers/pubnet/ \
  vultr-sg:{{ratesengine-galexie-archive-bucket}} \
  --transfers 16 --checkers 32 --progress
```

Alternative: drive the fill from R3 itself (faster ingress to
Vultr Object Storage) — see
[`archival-node-bringup.md` §"R3 — Vultr Singapore"](archival-node-bringup.md).

## DNS

- `api-r3.ratesengine.net` → R3's HAProxy frontend IP
  (Cloudflare DNS-only, grey cloud — the public-facing record
  `api.ratesengine.net` is the proxied one)

## Cross-references

- [`r1-deployment-state.md`](r1-deployment-state.md) — sibling
  doc for R1; compare for per-region differences.
- [`r2-deployment-state.md`](r2-deployment-state.md) — sibling
  doc for R2.
- [`multi-region-cutover.md`](multi-region-cutover.md) §Stage 2
  — the bringup procedure that fills in this skeleton.
- [`archival-node-bringup.md`](archival-node-bringup.md) §"Per-
  region variations — R3 — Vultr Singapore" — per-host details.
- [ADR-0016](../adr/0016-per-region-storage-strategy.md) §"R3 —
  Vultr-hybrid" — design intent.
- L4.15 in [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md).
