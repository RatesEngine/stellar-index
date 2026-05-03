---
title: Hosting Options — Skipping Colo Without Boxing Ourselves In
last_verified: 2026-05-02
status: draft — decision at Week 1 procurement call
---

# Hosting Options — Skipping Colo Without Boxing Ourselves In

**Owner:** @ash + @alex.
**Decision deadline:** before end of Week 1 (we need a node syncing by
Week 2–3 per [delivery-plan.md](../../discovery/delivery-plan.md)).
**Binds:** [archival-node-spec.md](archival-node-spec.md) still defines
the shape of what we deploy; this doc answers *where* we deploy it.

---

## 1. The question

> "What if we skipped colo for now to move fast?"

Colo has two costs: (a) *procurement time* — ordering hardware,
shipping, racking, remote hands, network hand-off — all measured in
weeks; (b) *ownership overhead* — spares, repairs, contract mgmt.
In a 10-week delivery window (a) is the killer: a week of colo
wait is a week not ingesting ledgers.

**Good news:** every "move fast" option delivers the same logical
stack. Code, config, monitoring, data layout — identical across all
of the options below. What varies is *bandwidth cost*, *IOPS
profile*, *egress pricing*, and *monthly TCO*.

**Non-goal:** this doc does **not** argue for skipping colo
permanently. ADR-0004 (Tier-1 three-validator aspiration) is still
the north star. The question is only **"what do we stand up in
Weeks 2–10 so we don't burn the delivery window?"**.

---

## 2. The "move fast" axes

Four axes that separate the options below:

| Axis | Matters because |
| ---- | --------------- |
| **Time-to-running** | Week 2 wants a syncing node; every day of provisioning wait is a day without data. |
| **Bandwidth included** | Catchup downloads hundreds of GB; first month of backfill pushes several TB. Cloud egress at $0.09/GB destroys the cost model. |
| **NVMe IOPS profile** | stellar-core catchup is bound by small-random-write latency on the buckets directory. SATA SSDs push catchup from hours to days. Network-attached SSD (EBS gp3) works if provisioned high IOPS; most default tiers don't. |
| **Month-to-month commit** | Colo contracts run 12–36 months; cloud is hourly. If we're only there 4 months before migrating to owned hardware, the "expensive" cloud option can be cheaper total than a 24-month colo contract. |

---

## 3. Options, ranked by time-to-running

### 3.1 Hetzner Dedicated — **cheapest bare-metal, 12–24 h provisioning**

Germany / Finland. Order online; delivered and accessible within a
day, often same-day.

| Aspect | Detail |
| ------ | ------ |
| Representative SKU | `AX162-R` — AMD Ryzen 9 7950X3D, 128 GB DDR5 ECC, 2× 1.92 TB NVMe |
| Monthly | ~€150 |
| Add storage | Add-on 3.84 TB NVMe up to 4× → ~€350/mo |
| Bandwidth | **20 TB/mo included**, then €1/TB (basically free for our profile) |
| IOPS profile | Local NVMe; matches our archival-node-spec exactly |
| Provisioning | API or web UI; delivery 1–24 h |
| Regions | Falkenstein, Helsinki, Nuremberg (+ a small US presence via Hetzner Online US LLC) |
| Fit for our use | **Great for R1 shake-out**, less great for geographic diversity |

**Why this is the pragmatic default:** cheapest path to "a node is
syncing" in <48 h from procurement approval. Near-zero bandwidth
anxiety. NVMe-local matches our spec.

**Caveats:**
- Not available in Americas / APAC at Hetzner-standard pricing —
  for 3-region rollout we need another provider or a long-haul peer.
- No HSM integration in a shared facility (the HSM goes in a
  specific USB port — not possible on a remote host we don't
  physically touch). For Phase A (archival-only, no validator)
  that's fine; validator promotion (Phase B) requires either
  shipping an HSM to Hetzner via their remote-hands service (they
  support this) or relocating the node first.

### 3.2 Latitude.sh — **API-driven bare-metal, 5-minute provisioning**

| Aspect | Detail |
| ------ | ------ |
| Representative SKU | `c3.large.x86` — AMD EPYC 7502P (32c/64t), 256 GB, 2× 960 GB NVMe |
| Monthly | ~$700 |
| Add storage | Additional NVMe drives; ~$200/mo per 3.84 TB |
| Bandwidth | 20 TB/mo included |
| IOPS profile | Local NVMe; matches spec |
| Provisioning | API or web UI; **5 minutes to SSH** |
| Regions | Miami, NYC, Santiago, São Paulo, London, Amsterdam, Sofia, Frankfurt, Mumbai, Singapore, Tokyo, Sydney |
| Fit for our use | **Best for 3-region rollout**; single provider covers London / US / Asia |

**Why it wins for multi-region:** one provider, API-driven, one
billing relationship, same image across regions. The multi-region
Ansible playbook hits the same shape everywhere.

**Caveats:** more expensive than Hetzner; less generous "free
NVMe" in base SKUs.

### 3.3 Equinix Metal — **API-driven bare-metal, global footprint**

| Aspect | Detail |
| ------ | ------ |
| Representative SKU | `m3.large.x86` — AMD EPYC 7513 (32c/64t), 256 GB, 2× 3.8 TB NVMe |
| Monthly (3-year reserved) | ~$1 500 |
| Monthly (on-demand) | ~$3 000 |
| Bandwidth | Tiered; first 1 TB free, then public-internet $0.05/GB (or free on interconnects) |
| IOPS profile | Local NVMe; matches spec |
| Provisioning | API-driven; minutes |
| Regions | 20+ globally (SV, DC, NY, LA, Dallas, Chicago, Ashburn, Amsterdam, Frankfurt, Tokyo, Singapore, Sydney, …) |
| Fit for our use | Best breadth; most expensive per hour |

**Why you'd pick it:** footprint matches our target regions
exactly, and Equinix interconnects make cross-region replication
*free* when both nodes are in Equinix facilities (this matters once
we migrate our MinIO + Timescale replication to real colo).

**Caveats:** pricing premium vs Hetzner/Latitude. Bandwidth
metering is real; backfills cost.

### 3.4 Hyperscalers — AWS / GCP / Azure

| Aspect | Detail |
| ------ | ------ |
| Representative AWS SKU | `i4i.8xlarge` — 32 vCPU, 256 GB, 2× 3 750 GB NVMe instance storage |
| Monthly (1-year reserved) | ~$1 800 |
| Monthly (on-demand) | ~$2 500 |
| Bandwidth | AWS egress $0.09/GB = **brutal for backfills** (10 TB/mo = $900 just on egress) |
| IOPS profile | `i4i` NVMe instance storage is local + high IOPS → matches spec |
| Provisioning | Minutes |
| Regions | Every major |
| Fit for our use | Works, but bandwidth pricing is the trap |

**Why you'd pick it:** already using AWS/GCP for other workloads,
want one bill, want managed services (RDS for Postgres+Timescale,
Elasticache for Redis). Our ADR-0002 MinIO decision is satisfied
by direct S3, so some components can move to managed services in
cloud.

**Caveats:**
- Egress cost dominates. If we do a full since-inception backfill
  (~2 TB download from SDF, ~2 TB processed output staying
  in-cloud), we're fine; subsequent cross-region replication of
  archives pushes several TB/month = hundreds of dollars just
  in network.
- `i4i` local NVMe is *ephemeral* (instance-store). Data vanishes
  on stop. We'd run everything on EBS gp3 with high IOPS
  provisioning instead — more expensive, slower, but durable.
- No HSM device access; would use AWS CloudHSM for validator
  keys. Fine but a different operational integration than YubiHSM.

### 3.5 Hybrid — bare-metal for ingest, cloud for the stateless serving plane

Rather than picking one provider for everything, split:

- **Ingest + storage tier** on Hetzner / Latitude / Equinix bare-
  metal (stellar-core, Galexie, stellar-rpc, Timescale, MinIO).
  Local NVMe, flat bandwidth, predictable cost.
- **Serving tier** (`ratesengine-api`, CDN integration) on AWS /
  Cloudflare — stateless pods that scale to zero and scale up to
  handle spikes. Near-zero cost at idle.

The multi-region topology ([multi-region-topology.md](multi-region-topology.md))
doesn't care whether region X is a bare-metal box or a cloud VM —
it's just "a region" to Patroni.

**Why this wins in practice:** the **storage + ingest workload is
where cloud is expensive**; the **serving workload is where cloud
is cheap** (elastic, CDN-cacheable, stateless). Split the difference.

### 3.6 What we're **not** considering

- **Kubernetes-everywhere (self-hosted k3s / Talos on cloud VMs).**
  Adds operational surface with no gain at our team size.
- **Managed Stellar-RPC providers (Nodies, Validation Cloud,
  BlockDaemon).** Useful for tx submission for a wallet; not useful
  for our bulk ledger-meta ingest.
- **Friends-and-family colo (someone's spare rack in a spare room).**
  Not a 99.9 % uptime environment.
- **Re-purposing @ash's existing R640 as-is.** Viable as a dev +
  staging target but dedicating it to mainnet bring-up couples our
  schedule to physical shipping for the eventual move. Leave it in
  place for dev.

---

## 4. Requirements fit-check

Running the four serious contenders against
[archival-node-spec.md §3](archival-node-spec.md#3-hardware-spec--per-node):

| Requirement | Hetzner AX162-R | Latitude c3.large.x86 | Equinix m3.large.x86 | AWS i4i.8xlarge |
| ----------- | --------------- | --------------------- | -------------------- | --------------- |
| ≥ 32 physical cores | ⚠ 16/32 threads (7950X3D) | ✅ 32c/64t | ✅ 32c/64t | ✅ 32c/64t |
| ≥ 128 GB ECC | ✅ 128 GB | ✅ 256 GB | ✅ 256 GB | ✅ 256 GB |
| Local NVMe with ≥ 10 TB | ⚠ 2× 1.92 TB base; add-ons to ~15 TB | ⚠ 2× 960 GB base; add-ons | ✅ 2× 3.8 TB | ⚠ 2× 3.75 TB ephemeral |
| 25 GbE NIC | ❌ 1 GbE typical | ✅ 10 GbE | ✅ 25 GbE available | ✅ 25 Gbps VPC |
| Flat bandwidth | ✅ 20 TB incl | ✅ 20 TB incl | ⚠ metered but cheap | ❌ $0.09/GB egress |
| HSM port access | ⚠ via remote hands | ⚠ via remote hands | ⚠ via remote hands | ❌ (CloudHSM instead) |
| Global regions | ❌ EU-heavy | ✅ 12+ | ✅ 20+ | ✅ 30+ |

Takeaways:
- **Hetzner AX162-R gets us running tomorrow** but is under-spec on
  CPU + NIC. Fine for Phase A archival-only; we'd upgrade when
  going to validator.
- **Latitude.sh or Equinix are the "buy at spec" options**. Equinix
  if geography matters; Latitude if cost matters.
- **AWS i4i works** but egress + ephemeral-storage caveats reshape
  the design — use EBS gp3 instead, pay for IOPS.

---

## 5. Recommended path — "move fast now, consolidate later"

### 5.1 Weeks 2–3: single node at Hetzner or Latitude

Pick one bare-metal provider. Stand up one node. Configure per
[archival-node-spec.md](archival-node-spec.md) with what we have —
fewer drives is fine for Phase A (`CATCHUP_RECENT` only needs
~100 GB).

- **Hetzner AX162-R + 2× 3.84 TB drive add-on**: €280/mo. 1 Gbps
  fine for non-saturated operation.
- **Latitude c3.large.x86 + 2× 3.84 TB NVMe**: $1 100/mo. API-
  provisioned, more regions.

Either gets us a **syncing non-voting archival node within 24–48 h
of procurement approval**. That unblocks Week 2–3 ingestion work in
`internal/sources/sdex` / `soroswap` etc.

### 5.2 Weeks 4–6: validator promotion on same hardware

Remote-hand the HSM in. Key ceremony can happen at @ash's location
with the HSM and a fresh laptop — the key material is generated
locally, then the HSM is shipped to the data centre via the
provider's secure-shipment program. The HSM socket over USB works
the same regardless of who installed it physically.

### 5.3 Weeks 6–8: two more regions

- Latitude covers 3 regions on one bill — cleanest multi-region
  story.
- Or mix Hetzner (R1/Falkenstein) + Latitude (R2/NYC, R3/Tokyo).
- Or move to Equinix Metal in all 3 regions for the premium path.

### 5.4 Post-launch (month 4+): migrate to owned colo

Run cloud/bare-metal for 3–6 months of real production. Once we
have measured numbers — actual NVMe utilisation, actual bandwidth
profile, actual failure rate — make an informed colo procurement
decision. The hardware spec in
[archival-node-spec.md](archival-node-spec.md) is still valid;
swap the host one region at a time.

**Migration mechanics:** adding a fourth node to the Patroni
cluster in a colo rack, promoting it to primary, decommissioning
the old cloud R1 → the same procedure as adding a region. The
architecture is built for this.

---

## 6. What stays the same regardless

Independent of which option we pick:

- All the code in `cmd/*` + `internal/*`.
- All the configs in `configs/`.
- All the ADRs. ADR-0002 (S3-compat) is satisfied everywhere.
  ADR-0004 (3 validators) applies regardless of provider.
- All monitoring, alerting, runbooks.
- The [multi-region-topology.md](multi-region-topology.md) design.
- The [validator-rollout.md](validator-rollout.md) phase plan.

What **does** change:

- Backup target (MinIO on-host vs direct S3 vs Hetzner Storage Box).
- Secret manager location (Vault on-host vs AWS Secrets Manager).
- Firewall syntax (nftables vs cloud security groups).

The code doesn't care; the Ansible role has a provider-specific
branch for each of these three.

---

## 7. 90-day cost comparison (estimate)

Running costs for three-region archival (no validator, Phase A
only) for 90 days, to sanity-check the "move fast" pitch:

| Option | 3× node cost/mo | 3× storage cost/mo | Egress (first 90 d) | 90-day total |
| ------ | --------------- | ------------------ | ------------------- | ------------ |
| Hetzner (EU only) | €450 | ~incl (~30 TB) | free | **€1 350** |
| Latitude (3 regions) | $2 100 | $600 | free (within 20 TB) | **$8 100** |
| Equinix Metal (3 regions, 3yr reserved) | $4 500 | (incl) | cheap if Equinix-to-Equinix | **$13 500** |
| AWS i4i (3 regions) | $5 400 | EBS $600 | ~$1 800 for first backfill | **$23 400** |
| Hybrid: Hetzner + AWS stateless | €150 + $200 | €130 | free | **~$1 000** |

Hetzner-only (single-region) is **one order of magnitude** cheaper
than AWS multi-region. Any of the first three gets us to "running
production" in less than the cost of one colo rack setup.

Colo is the long-term win (Year 2+), but the launch doesn't need
it.

---

## 8. Recommendation

**Pick Hetzner single-node for Phase A** (Weeks 2–3 shake-out) and
graduate to **Latitude.sh multi-region** for Phases C–D (Weeks 6–8)
if the measured single-region results look good.

Rationale:
1. Fastest possible "node is syncing" — Hetzner's order-to-SSH is
   hours.
2. Near-zero cost while we prove the stack.
3. One provider switch later (Hetzner → Latitude.sh) is painless —
   we ship a ZFS snapshot of the data pool over the internet
   (~2 TB at Hetzner's free 20 TB/mo = free), re-bootstrap on
   Latitude, re-parent cluster.
4. Colo procurement runs **in parallel** with cloud launch, not
   serialised. By Month 4 we can make a "move or stay" decision
   from data, not speculation.

Pre-approval checklist for @ash before we click "provision":

- [ ] Which region for the first node? (Falkenstein default;
      London/Helsinki if closer to home).
- [ ] Which drive profile? Minimal (2× 1.92 TB, Phase-A only) or
      bigger (2× 3.84 TB + 2× 3.84 TB add-ons, ready for
      CATCHUP_COMPLETE).
- [ ] How is the cost covered in Month 1? Grant-reimbursable or
      personal card up-front.
- [ ] Confirm: Phase-A happens at cloud, not shipping to colo.
      Validator promotion timing TBD post-measurement.

---

## 9. What we lose by skipping colo

Honesty bill:

- **Operational maturity discount.** Provider outages (Hetzner,
  Latitude) are someone else's runbook. We don't exercise our own
  "replace a failed NVMe" procedure. When we do migrate to colo,
  the team's first real hardware swap is in production.
- **HSM friction.** Remote-hand HSM install works, but the ceremony
  is best done somewhere the operators can physically attend. The
  current plan has @ash generating keys locally and shipping the
  provisioned HSM; that works but is one more moving part.
- **Audit / compliance story.** For Tier-1 listing, SDF looks at
  operational posture. "We run at Hetzner" is weaker than "we run
  in owned colo" on that front. Not disqualifying — many T1 orgs
  run on cloud — but a talking point during the listing review.
- **Cost crossover at ~Year 2.** Cloud dominates total cost beyond
  12 months. Not an immediate concern, but the roadmap should have
  "colo evaluation" as a Month-10 calendar entry.

None of these block Week 2 launch. All of them become Month 4–12
work-streams.

---

## 10. Decision timeline

- **Today:** @ash picks between Hetzner (cheap, fast, EU-only
  first) vs Latitude.sh (more expensive, 3-region-ready, 5-min
  provision).
- **+1 day:** node provisioned; Ansible runs our bootstrap.
- **+2 days:** stellar-core in `CATCHUP_RECENT`; Galexie writing to
  local MinIO.
- **Week 2:** ratesengine-indexer SDEX connector ingests first trade.
- **Week 3:** Phase-A exit criteria met (7 days synced).
- **Week 4:** validator key ceremony (if promoting at this provider;
  otherwise defer).
- **Week 6:** second region stood up; Patroni grows.
- **Week 8:** third region stood up; T1 org eligibility clock starts.
- **Month 4:** colo evaluation based on measured numbers.

---

## 11. References

- [archival-node-spec.md](archival-node-spec.md) — what we deploy
  (shape is the same regardless of provider)
- [multi-region-topology.md](multi-region-topology.md) — 3-region
  design
- [validator-rollout.md](validator-rollout.md) — 1→3 phased plan
- [HA plan](../ha-plan.md) — per-region HA rules
- [ADR-0002 MinIO / S3-compat storage](../../adr/0002-minio-s3-compat-storage.md)
- [ADR-0004 Tier-1 aspiration](../../adr/0004-tier1-validator-aspiration.md)
