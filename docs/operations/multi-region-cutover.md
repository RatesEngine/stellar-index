---
title: Multi-region cutover runbook (L4.14 → L4.17 + L5.8)
last_verified: 2026-05-03
status: operator runbook
---

# Multi-region cutover runbook

Sequenced operator runbook for **bringing R2 + R3 online** so the
launch goes live multi-region. Closes
[`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md)
rows L4.14, L4.15, L4.16, L4.17, and L5.8 in order.

The bringup procedure for a *single* archival node lives in
[`archival-node-bringup.md`](archival-node-bringup.md) §"Per-region
variations". This runbook orchestrates **all five rows** end-to-end
with pass conditions per stage so an operator follows top-to-bottom
on the day(s) they bring multi-region online.

The architectural anchor: **[ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md)
makes this safe** — closed-bucket VWAP/TWAP/OHLC values are
byte-identical across regions given the same input data, so
DNS-routing a request to any healthy region returns the same
answer. ADR-0016 then lets us pick the cheapest viable storage
shape per region.

## Pre-flight

- [ ] **R1 (Hetzner FSN1) is fully healthy.** SLO dashboards
      green; SLA probe latest-pass < 4h; no fired alerts;
      verify-archive Tier A passing.
- [ ] **AWS account provisioned + IAM role with EC2 + EBS +
      Route53 permissions.**
- [ ] **Vultr account provisioned + Bare Metal Singapore SKU
      ordered + Vultr Object Storage bucket created.**
- [ ] **Cloudflare zone for `ratesengine.net` under operator
      control.**
- [ ] **`r2.yml` + `r3.yml` inventories prepared** (copy from
      `configs/ansible/inventory/r2.example.yml` and
      `r3.example.yml`; fill in IPs, secrets refs, etc. — these
      files are gitignored).
- [ ] **etcd quorum decision recorded** — per
      [`multi-region-topology.md` §5.1](../architecture/infrastructure/multi-region-topology.md):
      5 nodes across 3 regions (R1×2, R2×2, R3×1) tolerates
      one-region loss. Operator confirms this matches what
      they're about to deploy.

## Stage 1 — L4.14 R2 spinup (AWS us-east-1)

### Provision

1. **EC2 instance.** r7i.4xlarge (16 vCPU / 128 GiB) with a
   1-year Compute Savings Plan per ADR-0016 §"R2 — AWS-hybrid".
2. **Storage.** 1-2 TB EBS gp3 attached. No archive mirror —
   galexie reads `s3://aws-public-blockchain` direct (free
   egress; Open Data Sponsorship).
3. **Security groups.** Open 22 (SSH from operator), 5432
   (postgres, only from R1's egress IPs), 8080 (API, behind
   the eventual Cloudflare proxy).

### Deploy

```sh
ansible-playbook -i configs/ansible/inventory/r2.yml \
  configs/ansible/site.yml
```

Watch for:
- Galexie config points at `aws-public-blockchain` (operator-set
  per `r2.example.yml`'s `galexie_archive_endpoint`).
- Patroni starts in REPLICA mode (R1 is the primary; R2 is the
  sync replica per `multi-region-topology.md` §5.1).
- `verify-archive -tier=chain` runs as a post-install gate.

### Pass condition

```sh
ssh r2 "systemctl status ratesengine-{indexer,aggregator,api}"
# All three units active (running)

ssh r2 "curl -sf http://localhost:8080/v1/healthz"
# {"status":"ok",...}

ssh r2 "psql -c 'SELECT pg_is_in_recovery();'"
# t  (R2 is a replica)
```

L4.14 ✅ when these all pass + the row in
`launch-readiness-backlog.md` flips 🔴 → ✅.

## Stage 2 — L4.15 R3 spinup (Vultr Singapore)

### Provision

1. **Vultr Bare Metal SKU.** Intel Xeon E-2388G (8c/16t,
   128 GB DDR4 ECC) + 2 × 1.92 TB local NVMe per ADR-0016
   §"R3 — Vultr-hybrid".
2. **Vultr Object Storage bucket.** Region-local Singapore;
   ~$25/mo for 5 TB. Set `r3.yml`'s
   `galexie_archive_endpoint` to the bucket URL.
3. **ZFS mirror across the 2 NVMes** — single-drive failure
   tolerance, acceptable for an async DR replica (R1's
   raidz2 is the integrity leader).

### Deploy

```sh
ansible-playbook -i configs/ansible/inventory/r3.yml \
  configs/ansible/site.yml
```

Galexie-archive bootstrap on R3 is **not** "download all 4.76 TB
to local disk"; it's "configure galexie to read from Vultr
Object Storage on demand". Initial bucket fill copies from
AWS public bucket → Vultr — operator-driven, ~6-12h depending
on Vultr's ingress bandwidth. Procedure in
[`archival-node-bringup.md §"R3 — Vultr Singapore"`](archival-node-bringup.md).

### Pass condition

```sh
ssh r3 "systemctl status ratesengine-{indexer,aggregator,api}"
ssh r3 "curl -sf http://localhost:8080/v1/healthz"
ssh r3 "psql -c 'SELECT pg_is_in_recovery();'"
# Same shape as R2 pass condition; R3 is also a replica (async).
```

L4.15 ✅.

## Stage 3 — L4.17 Cross-region Postgres replication wired

(Done in parallel with Stages 1 + 2's ansible runs, which
already lay down Patroni + etcd. This stage verifies it.)

### Verify

```sh
# From R1 — confirm both replicas are visible.
ssh r1 "patronictl -c /etc/patroni.yml list"
# Expected output:
#  + Cluster: ratesengine ----------+----+-----------+
#  | Member | Host | Role | State | TL | Lag in MB |
#  | r1     | ...  | Leader | running | 12 |        |
#  | r2     | ...  | Replica | running | 12 |        0 |
#  | r3     | ...  | Replica | running | 12 |        0 |
```

```sh
# From R1 — confirm sync_state for R2.
psql -c "SELECT application_name, sync_state, replay_lag
         FROM pg_stat_replication;"
# r2_replica should show sync_state=sync; r3_replica=async.
```

### Smoke test (failover drill)

This bleeds into Stage 5 (L5.8) — but it's a useful early
check. Pause R1's pg with `pg_ctl stop` (NOT a kill — clean
stop so Patroni demotes gracefully):

```sh
ssh r1 "sudo -u postgres /usr/lib/postgresql/15/bin/pg_ctl \
  -D /var/lib/postgresql/15/main stop -m fast"
# Wait 10s
ssh r1 "patronictl list"
# R2 should now show Role=Leader.
```

Restart R1's pg; Patroni rejoins R1 as a replica:
```sh
ssh r1 "sudo systemctl start postgresql@15-main"
# After ~30s, patronictl list shows r1 back as Replica.
```

L4.17 ✅.

## Stage 4 — L4.16 Cloudflare Anycast / GeoIP

### Configure

```
0. Pre-reqs (already done in cdn-setup.md):
   - api.ratesengine.net is proxied through Cloudflare.
   - DNS for ratesengine.net under Cloudflare control.

1. Per-region origin records:
   - api-r1.ratesengine.net → R1's HAProxy frontend IP
   - api-r2.ratesengine.net → R2's ALB / HAProxy frontend
   - api-r3.ratesengine.net → R3's HAProxy frontend
   All Proxy: DNS-only (grey cloud) — operator wants the
   public-facing record (api.) proxied; per-region origins
   stay direct.

2. Cloudflare Load Balancer (Smart Routing / GeoIP):
   - Pool: ratesengine-api-pool
     - Origin 1: api-r1.ratesengine.net  weight=1
     - Origin 2: api-r2.ratesengine.net  weight=1
     - Origin 3: api-r3.ratesengine.net  weight=1
   - Health check: GET /v1/healthz, expect 200 within 5s,
     check every 30s. 3 consecutive failures → unhealthy
     (under-30s eviction).
   - Routing: Geo Steering — EU → r1, NA → r2, APAC → r3,
     fallback ordering r1→r2→r3.

3. Public-facing record:
   - api.ratesengine.net → Cloudflare Load Balancer
   - Proxy: Proxied (orange cloud)
```

### Verify

```sh
# From three different network egress points (or via curl --resolve):
for region in r1 r2 r3; do
  curl -sH "Host: api.ratesengine.net" \
    --resolve api.ratesengine.net:443:$(dig +short api-${region}.ratesengine.net | head -1) \
    https://api.ratesengine.net/v1/healthz | jq .
done
# Each region's healthz should return 200 with its own region label
# (if the binary stamps cfg.Region.ID into the response).
```

```sh
# Smoke test that GeoIP routing actually picks the right region.
# Requires a VPN or remote shell:
curl -s https://api.ratesengine.net/v1/healthz \
  -w '%{remote_ip}\n' -o /dev/null
# IP should be the closest region's HAProxy frontend.
```

L4.16 ✅.

## Stage 5 — L5.8 Region-failover chaos test

The verification that "any one region down → service stays up"
holds end-to-end. Combines the Stage 3 + Stage 4 smoke tests
into a single coordinated kill.

### Pre-flight

- [ ] All three regions ✅ from prior stages.
- [ ] Cross-region consistency check passes (`make verify-cross-region`,
      see [`scripts/dev/verify-cross-region.sh`](../../scripts/dev/verify-cross-region.sh)).
- [ ] Customer comms drafted: maintenance window
      ([`deploy/comms/maintenance-window.md`](../../deploy/comms/maintenance-window.md))
      posted to the status page if the test runs against
      production-routed traffic; skipped if running against
      staging URL.
- [ ] On-call has [`rollback.md`](rollback.md) tabbed.

### Run

```sh
# 1. Take R1 offline at the HAProxy layer (NOT a hard reboot —
#    we want a clean shutdown so Patroni promotes cleanly).
ssh r1 "sudo systemctl stop haproxy keepalived"

# 2. Watch Cloudflare LB pool — the r1 origin should fall to
#    "unhealthy" within 90s (3× 30s checks).
#    Run from any non-r1 host:
while true; do
  date -u
  curl -sH "Host: api.ratesengine.net" \
    --resolve api.ratesengine.net:443:r2-ip-here:443 \
    https://api.ratesengine.net/v1/healthz \
    -w 'http=%{http_code} t=%{time_total}\n' -o /dev/null
  sleep 5
done
```

### Pass conditions

| # | Check | Bar |
|---|---|---|
| 1 | Cloudflare evicts R1 from the pool | within 90s |
| 2 | Patroni promotes R2 to pg leader | within 30s of R1 going dark |
| 3 | `/v1/healthz` from external network | continues 200 throughout (request rate may drop briefly during eviction) |
| 4 | `/v1/price` for popular pair | continues serving; `flags.stale=false` (the surviving regions have fresh data) |
| 5 | Cross-region consistency check (`verify-cross-region.sh`) | passes after R1 is restored — same byte-identical close-bucket value across all three regions |
| 6 | Reverse: bring R1 back, verify it rejoins as replica | Patroni adds R1 back at TL+1 (timeline incremented) |

### Capture

Record the run under
`test/chaos/reports/<UTC-timestamp>/region-failover.md` per the
[chaos Wave 1 runbook](chaos-wave1-runbook.md) format. Include:
- R1-offline timestamp + R1-online timestamp.
- Cloudflare pool transition timestamps (from CF dashboard).
- Patroni promotion event log.
- Any `flags.stale=true` observed (and how long).
- Any 5xx during the transition.

L5.8 ✅ when the run is clean and the report is committed.

## When something fails mid-cut

Stop. Open [`rollback.md`](rollback.md) and follow the matching
failure-mode section — the multi-region cutover is reversible
at every stage:

| Stage | Rollback |
|---|---|
| 1 (R2 spinup) | Decommission EC2 instance; remove from Patroni cluster (`patronictl remove`). R1 keeps serving solo. |
| 2 (R3 spinup) | Decommission Vultr; remove from Patroni cluster. R1 + R2 keep serving. |
| 3 (replication wired) | If replication is broken but R1 is healthy, point Cloudflare at R1 only (set R2/R3 origin weight=0); fix replication; revert. |
| 4 (Cloudflare GeoIP) | DNS revert: edit Load Balancer pool to weight=1 on r1, weight=0 on r2/r3. |
| 5 (chaos test) | Restart the killed services; the LB recovers automatically. |

## Cross-references

- [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md) — L4.14, L4.15, L4.16, L4.17, L5.8.
- [`launch-day-checklist.md`](launch-day-checklist.md) — orchestrates this runbook into the broader cutover.
- [`archival-node-bringup.md`](archival-node-bringup.md) §"Per-region variations" — per-host bringup details that this runbook calls into.
- [`multi-region-topology.md`](../architecture/infrastructure/multi-region-topology.md) — design intent.
- [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) — closed-bucket consistency contract that makes multi-region byte-equivalent.
- [ADR-0016](../adr/0016-per-region-storage-strategy.md) — three storage shapes per region.
- [`scripts/dev/verify-cross-region.sh`](../../scripts/dev/verify-cross-region.sh) — automated cross-region consistency check.
- [`rollback.md`](rollback.md) — failure-mode rollback procedures.
