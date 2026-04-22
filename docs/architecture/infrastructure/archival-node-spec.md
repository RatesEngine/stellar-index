---
title: Full Archival Node — Hardware & Software Spec
last_verified: 2026-04-22
status: draft — ratified at Week 2 procurement review
---

# Full Archival Node — Hardware & Software Spec

**Owner:** @ash + @alex (ops).
**Scope:** one node. Three of these ship into three regions; multi-
region topology lives in
[multi-region-topology.md](multi-region-topology.md).

A "full comprehensive archival node" here means a single host that
runs **every Stellar component we need** colocated — stellar-core in
`CATCHUP_COMPLETE` (full history since genesis), a published history
archive, Galexie for ledger-meta export, stellar-rpc with its own
captive-core, and all observability/agents. One host, one region,
does everything. Three of them in three regions = our pricing-layer
data plane.

The host can be **promoted** to a full validator (voting in SCP,
publishing history) at any time by swapping in HSM-backed validator
keys + flipping the `NODE_IS_VALIDATOR` flag. We ship as non-voting
archival at launch and promote once the Tier-1 paperwork clears
(ADR-0004).

---

## 1. Roles running on one host

| Role | Binary | Runs stellar-core? | Publishes archive? | Votes SCP? | Disk use |
| ---- | ------ | ------------------ | ------------------ | ---------- | -------- |
| Archival core | `stellar-core` (native) | yes | yes | yes if promoted | dominant |
| History publisher | `stellar-core --writequorum` + cron | via core | yes | no | shares with core |
| Galexie | `stellar-galexie` | yes (captive) | no | no | moderate |
| stellar-rpc | `stellar-rpc` | yes (captive) | no | no | moderate |
| Rates-engine indexer | `ratesengine-indexer` | no | no | no | negligible |
| Prometheus scrape | `node_exporter` + `stellar-core-prometheus-exporter` | no | no | no | tiny |
| Log shipper | `promtail` | no | no | no | tiny |
| Backup agent | `pgbackrest` / `mc` sidecar | no | no | no | bandwidth only |
| HSM agent | `yubihsm-connector` (if validator-promoted) | no | no | no | tiny |

Three distinct captive-core instances live on the host concurrently
(native core, galexie's, stellar-rpc's). This has been flagged as a
memory-pressure concern in
[adversarial-audit.md §6d](../../discovery/adversarial-audit.md#64-galexie--stellar-rpc-co-resident-captive-cores)
and is one of the reasons we over-provision RAM below.

---

## 2. Software stack

| Layer | Choice | Version pin |
| ----- | ------ | ----------- |
| OS | Ubuntu 22.04 LTS (server) | 22.04.4+ |
| Kernel | stock 5.15 HWE → 6.x | latest LTS |
| Filesystem | ZFS (rpool mirrored, data pool raidz2 on NVMe) | OpenZFS 2.2+ |
| Init | systemd | native |
| Container runtime (Galexie + stellar-rpc) | podman (rootless where possible) | 4.9+ |
| stellar-core | Debian package from `apt.stellar.org` | latest 22.x / 23.x |
| stellar-rpc | Debian package from `apt.stellar.org` | matches core |
| stellar-galexie | Go binary from source or release tarball | pinned in VERSIONS.md |
| Postgres (core state) | Postgres 15 from PGDG | 15.x |
| `stellar-archivist` | Rust `rs-stellar-archivist` from source | pinned |
| Prometheus node_exporter | upstream | latest LTS |
| Firewall | nftables | native |
| Secret agent | Vault Agent | 1.16+ |

All packages come from signed repos (SDF apt key `AEAF 01EE A6CA FCEF
DDAE 8AA7 0463 8272 A136 B5A6`, PGDG, Ubuntu main). No random
curl-to-bash install scripts.

---

## 3. Hardware spec — per node

### 3.1 CPU

| Requirement | Spec | Why |
| ----------- | ---- | --- |
| Architecture | x86-64 (AMD EPYC or Intel Xeon) | stellar-core upstream binaries target x86-64; ARM support exists but is not default-tested by SDF. |
| Core count | **32 physical cores / 64 threads** minimum; 64c/128t recommended | Three captive-cores + postgres + network I/O + Go binaries. Stellar-core is single-threaded-per-ledger during catchup but benefits from parallel validation work. |
| Base clock | ≥ 2.4 GHz | Catchup is latency-bound, not throughput-bound. Higher clock > more cores up to ~16c. |
| Feature requirements | AES-NI, AVX2, SHA extensions | stellar-core uses all three for XDR hashing + crypto. |

**Recommended parts (2026):**

- **Option A (AMD):** EPYC 9354 (32c / 64t, 3.25 GHz base, 3.8 GHz boost, 256 MB L3). Single-socket.
- **Option B (Intel):** Xeon Gold 6438Y+ (32c / 64t, 2.0 / 4.0 GHz). Single-socket.
- **Option C (budget):** AMD Ryzen 9 7950X (16c / 32t, 4.5 GHz base). Desktop silicon, acceptable for non-validator archival if colo supports.

SDF's reference baseline from April 2024 (`c5d.2xlarge` = 8c) is
massively under-spec for an **archival** node. That number is for a
30-day-retention validator.

### 3.2 Memory

| Requirement | Spec | Why |
| ----------- | ---- | --- |
| Capacity | **128 GB ECC** minimum; **256 GB** recommended | Three captive-cores, one native core, Postgres 15 (BucketListDB caches), OS page cache for NVMe-backed ZFS, Redis sidecar (if co-resident). |
| Type | DDR4-3200 ECC RDIMM or DDR5-4800 ECC RDIMM | ECC is mandatory for a node claiming archive integrity. Bit-flips in a 20TB archive are inevitable without ECC over multi-year runtime. |

Memory headroom is protection against the "3×captive-core" footprint
we've flagged as unmeasured risk. If measurement in Week 3 shows
actual steady-state at ~60 GB, we can skip the 256 GB tier. Until
measured, over-provision.

### 3.3 Storage

This is the load-bearing line item. Sizing numbers are **estimates**
for April 2026 and must be re-measured in Week 3; call-outs below
mark what's verified vs extrapolated.

#### 3.3.1 Growth projections (from 2024 baselines)

| Asset | 2024 size (verified) | 2026 estimate (extrapolated ×2) | 2028 plan-ahead |
| ----- | -------------------- | ------------------------------- | --------------- |
| Current ledger (BucketListDB + buckets) | ~10 GB | ~25 GB | ~60 GB |
| Full history archive (buckets + history since genesis) | ~800 GB (community reports) | ~1.6 TB | ~4 TB |
| 30-day transaction-meta lake (Galexie, zstd) | n/a | ~150 GB | ~300 GB |
| Full transaction-meta since P20 (Galexie, zstd) | n/a | ~2 TB | ~5 TB |
| stellar-rpc SQLite (full-event retention since P20) | n/a | ~500 GB (projected; untested) | ~1.5 TB |
| Postgres for core BucketListDB | ~40 GB | ~100 GB | ~250 GB |
| Logs, monitoring, misc | — | ~50 GB working set | ~100 GB |
| **Total working set** | | **~4.5 TB usable** | **~11 TB usable** |

**Verified vs extrapolated:**

- 10 GB / 800 GB figures: SDF `prerequisites.mdx` + community
  archive size reports from early 2024. Verified during Phase 1.
- 2026 `×2` extrapolation: based on historical Stellar data-growth
  ~doubling every 18 months. Not verified; re-measure in Week 3.
- SQLite retention-window size: not benchmarked (adversarial audit
  §6c). The ~500 GB number is a rough bounds-estimate; could be
  much larger. This is why we route historical event reads through
  Galexie, not stellar-rpc.

#### 3.3.2 Disk layout

| Pool | Devices | Topology | Capacity (usable) | Use |
| ---- | ------- | -------- | ----------------- | --- |
| `rpool` | 2× 512 GB NVMe (enterprise) | ZFS mirror | 512 GB | OS, packages, logs |
| `data` | 8× 3.84 TB NVMe U.2 (Kioxia CD6 / Samsung PM9A3) | ZFS raidz2 (6+2) | ~23 TB | history archive, Galexie lake, Postgres, stellar-rpc SQLite |
| `scratch` | 1× 1.92 TB NVMe | single device | 1.92 TB | catchup staging, replay scratch, backup repo cache |

**Why ZFS:**
- Block-level checksums catch silent bit-rot on the archive.
- Snapshots cheap for point-in-time rollback during a core upgrade.
- `zfs send | zfs recv` is our cross-region archive replication
  primitive.
- Native compression (zstd) reduces Postgres+SQLite footprint
  another ~2×.

**Why not raidz3:** at raidz2 with 8 drives we tolerate 2 simultaneous
failures. Raidz3 adds one more, at 12% capacity cost. For archival
(not hot-serving) raidz2 is the standard trade-off.

**Why U.2 NVMe, not SATA SSD:** latency during catchup is dominated
by many small synchronous writes to the Postgres WAL and the
BucketListDB. NVMe is ~100× lower-latency than SATA for this
workload. SATA SSDs push catchup times from "hours" toward "days."

**Do we need rotational storage?** No. Pure NVMe. Rotational drives
on a Stellar archival node were the 2023 norm; catchup times were
miserable. In 2026 NVMe pricing makes rotational a false economy for
a node this active.

#### 3.3.3 Catchup-time estimate

With the spec above:

- `CATCHUP_RECENT` (default): ~10-20 min from cold.
- `CATCHUP_COMPLETE` via `rs-stellar-archivist mirror` from SDF
  public archive + then local core catchup: **~48-72 h** on the
  first pass. Dominated by archive download (network-bound) + bucket
  application (CPU/NVMe).
- `CATCHUP_COMPLETE` re-seed from another of our nodes (intra-region
  or via `zfs send`): **~12-24 h**.

We run the first full catchup once, then siblings seed from it.

### 3.4 Network

| Requirement | Spec | Why |
| ----------- | ---- | --- |
| Primary NIC | 2× 25 GbE (bonded LACP) | stellar-core P2P + archive downloads + inter-region replication all share this. |
| Out-of-band management | 1× 1 GbE to OOB switch | for IPMI/iDRAC; isolated VLAN; no internet egress |
| Public bandwidth | ≥ 1 Gbps sustained, 10 Gbps burst | during initial catchup we saturate 1 Gbps for hours |
| Transit | 2× upstream providers, BGP where supported | avoid single-ISP dependency in colo |
| Firewall | stateful, nftables local + hardware at edge | SCP on 11625 open to world; 11626 firewalled to LAN only |
| Public IPv4 | 1× static routable | SCP peers can't NAT-traverse |
| IPv6 | enabled | stellar-core speaks IPv6 |

**Port inventory:**

| Port | Proto | Direction | Purpose |
| ---- | ----- | --------- | ------- |
| 11625 | TCP | in + out | `PEER_PORT` — SCP P2P. Required open. |
| 11626 | TCP | in (LAN only) | `HTTP_PORT` — core admin. Never public. |
| 5432 | TCP | in (LAN only) | Postgres BucketListDB. |
| 8080 | TCP | in (LAN only) | stellar-rpc HTTP. |
| 8000 | TCP | in (LAN only) | Galexie HTTP (if enabled). |
| 9100 | TCP | in (LAN only) | node_exporter. |
| 22 | TCP | in (jump-host only) | SSH, keys-only, no password. |
| 443 | TCP | out | history archive downloads + Vault sync. |
| 53 | UDP | out | DNS. |

### 3.5 Power, cooling, physical

- **Power:** 2× redundant PSUs on separate PDUs fed by separate UPS
  legs. Host idle ≈ 200 W, loaded ≈ 550 W.
- **Cooling:** 1U / 2U chassis in standard 23 °C aisle. Monitor
  drive temperature (NVMe thermals matter — throttling during
  catchup slows everything).
- **Physical security:** locked rack, cage if available. Badge
  access + CCTV minimum.
- **Console access:** IPMI / iDRAC on the OOB VLAN, reachable only
  via jump host, protected by 2FA + source-IP allowlist.

---

## 4. Suggested BOM (per node)

Reference build — "Option A (AMD)":

| Line | Part | Qty | Est. price (USD, 2026) |
| ---- | ---- | --- | ---------------------- |
| Chassis | Dell PowerEdge R7625 1U (or Supermicro AS-1125HS-TNR) | 1 | $3 000 |
| CPU | AMD EPYC 9354 (32c/64t, 3.25/3.8 GHz) | 1 | $3 500 |
| Motherboard + BMC | included with chassis | — | — |
| RAM | DDR5-4800 ECC RDIMM, 32 GB | 8 (= 256 GB) | $3 200 |
| OS NVMe | Kioxia XG8 512 GB M.2 | 2 | $200 |
| Data NVMe | Kioxia CD6-V 3.84 TB U.2 | 8 | $7 000 |
| Scratch NVMe | Samsung 980 Pro 2 TB | 1 | $200 |
| NIC | Mellanox ConnectX-6 Dx 25 GbE | 1 | $600 |
| Rail kit + cables | — | 1 | $150 |
| PSU | 2× 800 W platinum redundant | 1 kit | (incl.) |
| **Per-node total** | | | **~$17 850** |
| 3-node fleet | | | **~$53 550** |

Cheaper alternatives:

- **Option C (Ryzen):** drop to ~$8 000 / node by using 7950X + 128 GB
  DDR5 + 4× 3.84 TB raidz1. Not recommended for validator promotion;
  acceptable for non-voting archival during Tranche I.
- **Refurbished Dell R640 (existing):** @ash already owns at least
  one. R640 + 6× 3.84 TB NVMe = ~$2 000 marginal. Use as region-1
  node during bring-up; replace with new hardware only if measured
  performance is inadequate.

Recurring costs (per node per month):

| Line | Low | High | Notes |
| ---- | --- | ---- | ----- |
| Colocation (1U) | $100 | $400 | region + tier dependent |
| Power (600 W avg) | $50 | $150 | $0.10–0.30/kWh × 438 kWh/mo |
| Bandwidth (25 TB/mo sustained during backfills, ~3 TB steady) | $50 | $200 | depends on IP transit |
| IP transit / BGP | $100 | $300 | optional but recommended |
| Monitoring, remote hands quota | $50 | $150 | |
| **Per-node per-month** | **$350** | **$1 200** | |

Three-node fleet: **$1 k–3.6 k / month recurring**. Line with the
HA plan §12 cost envelope.

---

## 5. Host hardening

Beyond the per-component security in the HA plan §6:

- **Full-disk encryption:** LUKS on root pool; ZFS native encryption
  on data pool. Keys sealed in TPM + Vault; unsealed at boot only
  after operator confirmation (**not** auto-unlocked on reboot
  without supervision).
- **SSH:** keys only, FIPS curves only, `PermitRootLogin no`,
  `AllowGroups rates-ops`. All SSH through jump host on OOB.
- **Sudo:** logged to Loki. Elevated sessions expire in 15 min.
- **Audit daemon:** `auditd` with CIS Level 2 profile. Shipped to
  Loki.
- **Kernel hardening:** `sysctl` profile from CIS benchmark.
- **Package updates:** unattended-upgrades for security patches;
  stellar-core upgrades require a planned window.
- **Immutable OS image (aspiration):** deliver the host as an
  OS image built from a `packer` template, re-deployable. Not a
  launch-blocker but an "after launch" hygiene item.

### 5.1 Validator key management (when promoted)

- **YubiHSM 2** in a dedicated USB slot, PIN-protected, audit-
  logged. `stellar-core` integrates via the `NODE_SEED` resolution
  path + a signer daemon reading from the HSM.
- **Key ceremony:** generate on the HSM (never exported), witnessed
  by two operators + recorded.
- **Backup:** HSM backup file encrypted with split-knowledge Shamir
  shares, held in two geographically separated safes.
- **Rotation:** validator keys rotate on a 2-year cadence or
  immediately on suspected compromise.

---

## 6. Operational playbook (skeleton)

### 6.1 Bring-up

1. Rack + cable per §3.4–3.5; OOB reachable from jump host.
2. PXE-boot to Ubuntu 22.04; run our `packer`-built OS image.
3. `ansible-playbook bootstrap.yml` → installs stellar-core,
   stellar-rpc, galexie, postgres, prometheus, node_exporter, vault
   agent; configures ZFS pools + systemd units.
4. Vault unseal + bind secrets (DB password, HSM PIN if validator).
5. First catchup: `stellar-archivist mirror` from SDF → local
   `data/history/`. ~12-24 h.
6. `stellar-core new-db` + `stellar-core catchup complete/0`
   against local mirror. ~24-48 h.
7. Galexie + stellar-rpc captive-cores start in `CATCHUP_RECENT`.
8. Prometheus scraping begins → dashboards populate.
9. Join the ratesengine-indexer fleet as `region-X` consumer.

### 6.2 Upgrade

- stellar-core upgrades: follow SDF's "3 of 4" rule — one region
  upgrades at a time, waits 24 h, proves stability, next.
- Soroban protocol upgrade ledger announced weeks ahead; flag day
  is coordinated via SDF Discord `#validators`.
- We **never** skip a protocol upgrade.

### 6.3 Backup

- ZFS snapshots daily on `data` pool; weekly `zfs send` to
  MinIO-backed replication target.
- Postgres → `pgBackRest` → MinIO every hour WAL + daily base.
- History archive: published to MinIO, cross-region-replicated per
  multi-region topology.
- HSM backup: manual, in the operator safe. Annual audit.

### 6.4 Monitoring alerts (node-specific)

| Metric | Threshold | Severity | Runbook |
| ------ | --------- | -------- | ------- |
| `stellar_core_ledger_age_seconds` | > 30 s | P1 | [core-lag](../../operations/runbooks/core-lag.md) |
| `zfs_pool_degraded` | any | P1 | [zfs-degraded](../../operations/runbooks/zfs-degraded.md) |
| NVMe temp | > 70 °C | P2 | [nvme-thermal](../../operations/runbooks/nvme-thermal.md) |
| Archive publish failure | any | P2 | [archive-publish-fail](../../operations/runbooks/archive-publish-fail.md) |
| `galexie_export_lag_ledgers` | > 500 | P2 | [galexie-lag](../../operations/runbooks/galexie-lag.md) |
| stellar-rpc SQLite size growth | > 20 %/day | P3 | [rpc-sqlite-growth](../../operations/runbooks/rpc-sqlite-growth.md) |
| Host up | any missed scrape × 3 | P1 | [host-down](../../operations/runbooks/host-down.md) |

Runbooks live under `docs/operations/runbooks/` (Week 9).

---

## 7. Open questions / pre-procurement verification

These must close before we cut purchase orders:

1. **Colo provider + rack space** — per region. Probably:
   - Region A (EU): London equivalent of LONAP-adjacent colo.
   - Region B (NA): NYC or Ashburn.
   - Region C (APAC): Tokyo or Singapore.
   Bandwidth pricing varies 4× between these. Firm up in Week 1.
2. **HSM choice** — YubiHSM-2 vs Nitrokey HSM 2 vs cloud KMS
   (AWS CloudHSM). Budget + validator-compliance trade-off.
3. **Exact archive size today (April 2026)** — before buying 24 TB
   of NVMe we should point `rs-stellar-archivist scan` at SDF's
   public archive and sum the byte counts. ~1-hour task, will update
   §3.3.1 with real numbers.
4. **ARM64 servers?** Ampere Altra-based systems are cheaper per
   core; stellar-core on ARM is community-tested but not SDF-blessed.
   Save for evaluation post-launch.
5. **IPMI exposure** — some colos charge extra for OOB. Factor in.

---

## 8. References

- [ADR-0004 Tier-1 validator aspiration](../../adr/0004-tier1-validator-aspiration.md)
- [ADR-0002 MinIO / S3-compat storage](../../adr/0002-minio-s3-compat-storage.md)
- [Discovery — archival-nodes.md](../../discovery/data-sources/archival-nodes.md)
- [Discovery — stellar-archivist.md](../../discovery/data-sources/stellar-archivist.md)
- [HA plan](../ha-plan.md) — how three of these hang together
- [Multi-region topology](multi-region-topology.md) — the 3-region
  architecture this node fits into
