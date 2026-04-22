---
title: Existing Kubernetes Cluster — Assessment & Options
last_verified: 2026-04-22
status: investigation — read-only probe, no changes made
---

# Existing Kubernetes Cluster — Assessment & Options

**Owner:** @ash.
**Context:** @ash granted read-only access (via `~/.kube/ctx.conf`) to
explore an existing Talos-on-Proxmox Kubernetes cluster that is
already running a stellar-core, stellar-rpc, and the legacy CTX Rates
production API. No changes were made during this investigation.

**Cofounder note:** the existing stellar-core "has been struggling to
stay synced — maybe ZFS, maybe something else." This doc diagnoses
the root cause and lays out options for leveraging the cluster for
Rates Engine bring-up.

---

## 1. Cluster inventory (read-only probe, 2026-04-22)

**Cluster:** Talos Linux v1.13-alpha (k8s 1.33.1), 316 days old.
**Hypervisor:** Proxmox VE (CSI driver `csi-proxmox.sinextra.dev`).
**Public IP:** `135.84.178.60` (via `istio-ingress/public-istio`
LoadBalancer, MetalLB-managed — so this is bare-metal/colo, not
AWS/GCP).

### 1.1 Nodes

| Node | Role | CPU | RAM | Ephemeral | Current usage |
| ---- | ---- | --- | --- | --------- | ------------- |
| talos-cp-1 | control-plane | 4 | 8 GB | 48 GB | 15 % CPU / 41 % mem |
| talos-w-1 | worker | 8 | 40 GB | 48 GB | 4 % / 8 % |
| talos-w-2 | worker | 8 | 40 GB | 48 GB | 4 % / 11 % |
| talos-w-3 | worker | 8 | 40 GB | 48 GB | 8 % / 12 % |
| talos-w-4 | worker | 8 | 40 GB | 48 GB | 17 % / 13 % |
| talos-w-5 | worker | 8 | 40 GB | 48 GB | 31 % / 26 % |

**Aggregate worker capacity:** **40 CPU / ~200 GB RAM / 240 GB
ephemeral** across 5 worker VMs. Heavily under-utilised today.

### 1.2 Storage classes

| Class | Medium | Default | Notes |
| ----- | ------ | ------- | ----- |
| `pve-zfs-nvme` | NVMe-backed ZFS on Proxmox host | no | used by stellar-* + ctx `dash` |
| `pve-zfs-sas` | SAS HDD ZFS | no | backups, mongodb backups |
| `pve-zfs-sata` | SATA SSD/HDD ZFS | **yes** | general workloads |

### 1.3 Relevant existing workloads

| Namespace | Workload | State | Storage |
| --------- | -------- | ----- | ------- |
| `crypto-stellar` | `stellar-core-v1` (StatefulSet, 1 replica, 3 containers: core + archive-http + prom-exporter) | Running, 6 restarts in 9 d | `stellar-core-v1-data` — **5.5 TiB NVMe** |
| `crypto-stellar` | `stellar-db` (Postgres) | Running 28 d, no restarts | `stellar-db-pvc` — **2 TiB NVMe** (Retain) |
| `crypto-stellar` | `stellar-rpc` v26.0.0 | Running 12 d, keeping up with tip | `stellar-rpc` — **400 GiB NVMe** (Retain) |
| `crypto-stellar` | `horizon` | scaled to 0 | — |
| `ctx-rates` | `rates-deployment` (the legacy CTX Rates API) | Running 28 d | `rates-service` ClusterIP |
| `ctx` | `dash` | — | 150 GiB NVMe |
| `ctx-staging` | `spend-api` | — | 50 GiB SATA |
| `mongodb` | MongoDB + backup | — | 20+50 GiB |
| `elastic` | Elasticsearch ES-default | — | 250 GiB SATA |
| `prometheus` | kube-prometheus-stack | Running | 50 GiB SATA |
| `drone` | CI | — | 10 GiB SATA |
| `registry-system` | container registry | — | 20 GiB SATA |

### 1.4 Ingress

- `istio-ingress/public-istio` → public IP `135.84.178.60`
  (LoadBalancer; serves 80 / 443 / Istio status on 15021).
- `istio-ingress/private-istio` → `172.20.2.1` (internal).
- cert-manager active.
- MetalLB running on all 5 workers.

---

## 2. Why the existing stellar-core is struggling to stay synced

### 2.1 The data

- **Network tip (observed via SCP):** ~62,240,851 (2026-04-22 21:19 UTC).
- **`stellar-rpc` captive-core has applied:** ~61,520,090 (within
  ~700 k ledgers of tip, ~1 hour behind — **normal catchup lag**).
- **`stellar-core-v1` has applied:** ~52,942,513 (**9.3 million
  ledgers / ~18 months behind**).
- Current `stellar-core-v1` apply rate: ~1 ledger/sec.
- 6 restarts in 9 days → progress keeps getting disrupted.

### 2.2 Root cause — not ZFS

Evidence ZFS / storage is **not** the bottleneck:

1. **`stellar-rpc`'s captive-core runs on the same cluster with the
   same ZFS-backed PV class** (`pve-zfs-nvme`) and is keeping up
   with the network tip fine.
2. Node `talos-w-4` (where stellar-core-v1 runs) is using **only
   17 % CPU / 13 % memory** — nowhere near resource-bound.
3. Apply rate of 1 ledger/sec is CPU-bound stellar-core work, not
   disk-bound — a disk bottleneck would show in IO wait, which
   doesn't appear in `kubectl top` averages.

### 2.3 Actual root cause — config-level, not infra

Reading the `stellar-core-v1` ConfigMap reveals:

```ini
CATCHUP_COMPLETE=true    # ← replay from genesis
CATCHUP_RECENT=100       # ← AND catch up recent 100
```

**Per SDF documentation these two are mutually exclusive.** Having
both set causes stellar-core to pick an unexpected mode. In practice
this configuration tries to replay from genesis (`CATCHUP_COMPLETE`)
while also setting a recent bound — which manifests as slow
progress.

Secondary contributors:

- **`[HISTORY.local]` only has `get`, not `put`** — the node reads
  from `/opt/stellar/history/` (which may not be populated) but
  doesn't publish its own archive. If the local history archive is
  incomplete, catchup must fetch from peers, and with only a small
  set of validator-attached `HISTORY=curl ...` sources
  (publicnode.org bootes/lyra), fetching historical files can be
  slow and single-point-of-failure.
- **6 restarts in 9 days** reset in-progress catchup state each
  time. The previous restart (April 13) was a config parse error
  on line 29 — "`Bare key ; put cannot contain whitespace`" — so
  the config has been hand-edited and broken once.
- **Postgres on a different node** (`stellar-db` is on talos-w-5,
  stellar-core on talos-w-4). Every ledger close requires Postgres
  round-trips over the cluster network → 1–2 ms per transaction,
  which at ~200 transactions/ledger = 200–400 ms of pure network
  RTT per ledger. On a single host this would be 10–100 μs.
- **Not using BucketListDB in captive-core mode** — this Postgres-
  backed stellar-core predates or opted-out of the BucketListDB
  performance improvements that shipped in August 2024.

### 2.4 What's *not* causing it

- **Not ZFS tuning.** The `pve-zfs-nvme` class is used by a working
  stellar-rpc captive-core at the same time.
- **Not lack of resources.** The node has 80 % idle capacity.
- **Not network.** SCP quorum info shows `lag_ms: 271`, `missing:2`
  — normal behaviour.
- **Not CAP-67 or protocol upgrades.** Applied ledger 52 M predates
  P23's 2025-09-03 activation; the node is still in a pre-unified-
  events era of the network.

### 2.5 If we wanted to fix it (reference only — no change made)

Minimal config change that would unblock:

```diff
-CATCHUP_COMPLETE=true
-CATCHUP_RECENT=100
+CATCHUP_RECENT=262144          # ~2 weeks of ledgers
+# remove CATCHUP_COMPLETE entirely
```

Plus:

- Add history archive fallbacks (SDF's three `core-live` archives):
  ```
  [HISTORY.sdf1] get = "curl -sfL https://history.stellar.org/prd/core-live/core_live_001/{0} -o {1}"
  [HISTORY.sdf2] get = "curl -sfL https://history.stellar.org/prd/core-live/core_live_002/{0} -o {1}"
  [HISTORY.sdf3] get = "curl -sfL https://history.stellar.org/prd/core-live/core_live_003/{0} -o {1}"
  ```
- Co-schedule Postgres and stellar-core on the same node (nodeSelector
  or affinity), or move stellar-core to BucketListDB-in-SQLite + use
  local volume.
- Post-config-change: `stellar-core new-db` + fresh catchup-recent
  (takes <30 min vs weeks of history replay).

---

## 3. 🚨 Security findings (flagged, not remediated)

### 3.1 Validator seed in plaintext ConfigMap

The `stellar-core-v1` ConfigMap contains:

```
NODE_IS_VALIDATOR=true
NODE_SEED="SDLSLYN7…HMLI"
```

This violates ADR-0004 ("Validator keys live in an HSM; never on
disk unencrypted"). Any cluster administrator or anyone with RBAC
`get configmaps` in `crypto-stellar` can read this key. Git-backed
manifests (if the cluster is GitOps-managed) would include it too.

**Risk:** whoever holds the key can vote on the Stellar network as
"ctx.com" validator. At current QUALITY=MEDIUM this has limited
blast radius but is still a real compromise vector for a would-be
Tier-1 org.

**Recommended remediation** (do NOT perform — flagging only):

1. Rotate the validator key immediately — generate a new one, update
   ConfigMap, restart core. The old key is considered compromised.
2. Move the replacement key to a Kubernetes Secret *at minimum*; a
   sealed-secret or external-secret wrapping HashiCorp Vault is
   much better.
3. Long-term: HSM per ADR-0004 §5 + validator-rollout.md §5.
4. Audit `kubectl get events -n crypto-stellar` + etcd for any
   hint the key was accessed externally.

### 3.2 Postgres password in plaintext ConfigMap

```
DATABASE="postgresql://user=stellar password=a61d521ca96b478fcd62b6650d8ea36b host=stellar-db ..."
```

Same ConfigMap. Same risk surface. Should move to a Secret.

### 3.3 Observation discipline

This investigation reached both secrets by running `kubectl get
configmap stellar-core-v1 -o yaml`. That path is open to any
operator with namespace-level read. Recommend auditing RBAC bindings
in `crypto-stellar` as part of the remediation.

---

## 4. Capacity assessment for Rates Engine on this cluster

Can we slot the Rates Engine workloads into this cluster without
touching existing workloads?

### 4.1 What we'd need

Per our [archival-node-spec.md](archival-node-spec.md) § sizing
tiers, a single-node Phase A workload needs roughly:

- **CPU:** 16 cores ("comfortable tier")
- **RAM:** 64 GB ("comfortable tier")
- **Storage:** 4 TB NVMe ("comfortable tier")

### 4.2 What's available

With 5 workers × 8c/40GB and current usage ~10 % CPU average:

- **CPU headroom:** ~35 cores idle — enough.
- **RAM headroom:** ~160 GB idle — enough.
- **Storage:** NVMe-backed PV class exists; sizing is flexible via
  new PVCs. Existing `stellar-core-v1-data` is 5.5 TiB (could be
  reused post-remediation; otherwise fresh PVC).
- **Ingress:** public-istio LoadBalancer with cert-manager — ready.
- **Monitoring:** kube-prometheus-stack already running.

### 4.3 What doesn't fit the K8s model cleanly

- **SCP P2P on port 11625 needs a public IP** with predictable
  reachability. The `public-istio` LoadBalancer serves 80/443 but
  not 11625. Would need a NodePort or an additional MetalLB
  LoadBalancer service exposing 11625 TCP — doable but adds ops
  surface.
- **Stellar-core as a StatefulSet** is fine, but bucket-directory
  IO patterns benefit from a local-volume CSI driver rather than
  network-attached Proxmox ZFS (even if fast). The existing setup
  uses network-attached PVs — same tradeoff applies to any new
  deployment here.
- **HSM access from a pod** is tricky — YubiHSMs are USB devices
  physically attached to a host. Validator-class HSM in a K8s
  cluster requires either a USB-passthrough daemonset (complex)
  or an external signing service. Phase A (archival, non-voting)
  sidesteps this.
- **Single Proxmox cluster = single failure domain.** Our multi-
  region design assumes 3 geographically separate regions. This
  cluster can be **region R1 at best**; R2/R3 have to live elsewhere.
- **Worker RAM of 40 GB** means running all three captive-cores
  (stellar-core + Galexie's + stellar-rpc's) on a single pod risks
  OOM. Must split across pods/nodes, which we'd want to do anyway.

### 4.4 What we'd lose vs a dedicated box

- **ECC clarity.** Proxmox host ECC status unclear from a pod. The
  existing stellar-db has been running 240 days with zero restarts,
  suggesting the underlying hardware is stable — but ECC is not
  verifiable from inside the cluster.
- **Per-workload ZFS tuning.** On the dedicated box we'd tune
  recordsize per dataset (`postgres=8K`, `galexie=1M`, etc). On
  Proxmox PVs we inherit whatever the host zvol is tuned to.
- **Direct NVMe access.** The NVMe in the Proxmox host is behind the
  hypervisor's storage stack + the CSI driver. Real-world latency
  is "fast but not bare-metal" — good enough for Phase A based on
  the existing stellar-rpc keeping up; worth benchmarking before
  committing to production.

---

## 5. Options

Ranked by "get Rates Engine running in Weeks 2–3":

### Option A — Fix & reuse the existing stellar-core + stellar-rpc

- **Rotate the validator key.** Move to Secret. (Phase-B concern;
  Phase A we don't need to validate.)
- **Fix the CATCHUP config.** Flip to `CATCHUP_RECENT` only.
  Restart. Within 30 min we're synced.
- **Deploy our ratesengine-indexer + aggregator + api as new K8s
  manifests in a new `ratesengine` namespace** using the existing
  stellar-rpc at `crypto-stellar/stellar-rpc:8000` as our read
  endpoint. Galexie can live alongside or we defer Galexie until
  a dedicated box.
- **Benefit:** 0 hardware spend, running this week.
- **Cost:** region R1 is this cluster; R2/R3 still need external
  provisioning. Security posture on validator keys must be cleaned
  up.

### Option B — Clean deploy into this cluster, leave existing alone

- **Deploy brand-new** `ratesengine-stellar-core`, `ratesengine-
  galexie`, etc. in a fresh `ratesengine-infra` namespace with our
  own PVCs and configs (sourced from our Ansible-equivalent Helm
  chart, to land in follow-up PR).
- **Ignore the existing crypto-stellar setup.** It stays as-is.
- **Benefit:** zero risk to existing workloads; clean state.
- **Cost:** duplicate storage footprint (~5.5 TB additional NVMe
  allocation), duplicate compute, but we control the config.

### Option C — Cluster for the stateless parts, Hetzner for the stateful

- **Stateful plane on Hetzner** (once approved) — stellar-core,
  Galexie, Timescale, MinIO. Matches our existing archival-node-
  spec.
- **Stateless plane in this cluster** — `ratesengine-api`,
  `ratesengine-aggregator`, observability, ingress. Use istio for
  TLS termination + CDN edge, scale-out via K8s.
- **Benefit:** best of both — correct hardware for the heavy
  stateful work, elastic serving on existing K8s infra.
- **Cost:** two-provider operational surface; inter-site networking
  (VPN or public) to connect them.

### Option D — Wait for Hetzner, do nothing in K8s

- Status quo. Build everything in configs/ansible/, deploy once
  Hetzner approval clears.
- **Benefit:** architecture stays as designed.
- **Cost:** 1–3 additional days (or more) of calendar slip.

---

## 6. Recommendation

**Short-term (this week):** **Option C hybrid**, but with a twist —
use the existing cluster's **stellar-rpc** as our read-only ingest
surface right now. It's already synced and serving. Our
`ratesengine-indexer` can connect to
`crypto-stellar/stellar-rpc:8000` as a read endpoint and begin
pulling events into our trades hypertable. No changes to the
existing workloads required. **We get to start ingesting this
week while the Hetzner approval and fresh deploy work in parallel.**

This needs:
- No code changes to the existing cluster.
- Read-only network access from a new `ratesengine-indexer` pod in
  a new `ratesengine` namespace, pointed at the cluster-internal
  `crypto-stellar/stellar-rpc:8000`.
- Acceptance that during this bridge window, we don't have Galexie
  (historical backfill) — only the live stream from stellar-rpc.

**Medium-term (Weeks 3–6):** Hetzner-approved dedicated box
becomes the primary Phase-A/B archival node. Run it *alongside*
the cluster, not replacing. The two paths are redundant ingest.

**Long-term:** post-launch, the existing `crypto-stellar/stellar-
core-v1` either gets decommissioned (it's 18 months behind and has
key-hygiene issues), or gets rehabilitated (config fix + key
rotation) and becomes part of our multi-region topology. Not a
launch-blocker either way.

---

## 7. Security triage (the "do this regardless" list)

Items that should happen even if we never touch stellar-core
functionally:

1. Rotate the validator seed + move replacement to a Secret (or
   external-secrets operator). Today.
2. Move Postgres password to a Secret. Today.
3. Audit `kubectl auth can-i` for who can read ConfigMaps in
   `crypto-stellar`. This week.
4. Enable etcd encryption-at-rest if not already. This week.
5. Review whether the Git repo that manages this cluster has
   committed the sensitive ConfigMap — if yes, scrub history.

@ash: **do not** apply any of these from this session. The
investigation stopped at read-only per your instruction. Acting on
these is a follow-up decision with a real operator.

---

## 8. Open items (would confirm with further read-only probing)

- Exact Proxmox host hardware (ECC? NVMe model? RAM redundancy?).
- stellar-core-v1 Postgres IOPS and transaction rate.
- Whether the current cluster has scheduled backups of
  `stellar-db-pvc` (it's Retain policy — data survives PVC deletion,
  but a cluster-wide disaster still needs off-cluster backup).
- stellar-rpc v26.0.0's own retention settings (could be unbounded
  SQLite growth).
- Whether the Proxmox host has enough free NVMe for a second ~5 TB
  PVC (for Option B's clean deploy).
- Cross-check cluster's public IP `135.84.178.60` against
  stellar.org network-tools for known-validator status.

---

## 9. References

- [archival-node-spec.md](archival-node-spec.md) — sizing tiers
- [multi-region-topology.md](multi-region-topology.md) — single-
  region cluster = R1 at most
- [validator-rollout.md](validator-rollout.md) §5 — key ceremony
- [ADR-0004](../../adr/0004-tier1-validator-aspiration.md) —
  validator keys in HSM
- [hosting-options.md](hosting-options.md) — Hetzner + cloud
  alternatives
