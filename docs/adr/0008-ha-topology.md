---
adr: 0008
title: Per-region HA topology — colo primary + cloud DR, three-tier hot/warm/cold storage
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0008: Per-region HA topology

## Context

The Stellar RFP §Availability requires ≥ 99.99 % uptime
(coverage-matrix S9.1). At one nine of slack against full failure
that's 52 min/year of downtime — well below the cost of a single
cold-start of stellar-core's catchup-recent. So the HA target
forces per-component redundancy *and* a graceful-degradation
contract that defines what the API serves when individual planes
fail.

[`docs/architecture/ha-plan.md`](../architecture/ha-plan.md)
captures the full design (558 lines — physical topology,
per-component HA, capacity math, failure matrix, degradation
modes, backup/restore). That doc is comprehensive but was tagged
`status: draft` pending Week-2 design review. This ADR ratifies the
load-bearing decisions from it as the binding commitment for
Phase 6 (Weeks 8–9) infrastructure work.

Cross-references:

- ADR-0001 — Horizon out of architecture (constrains ingest path).
- ADR-0002 — S3-compatible storage (constrains MinIO + DR target).
- ADR-0004 — Tier-1 three-validator aspiration (constrains
  stellar-core redundancy to N+2).
- ADR-0007 — Redis as hot-path cache + rate-limit (constrains the
  hot tier).
- ADR-0015 — Closed-bucket-only API serving (constrains the
  cross-region invariant).
- ADR-0016 — Per-region storage strategies (R1/R2/R3 storage
  shapes; this ADR is the *per-region* HA topology, ADR-0016 is
  *across regions*).

## Decision

**Adopt the per-region HA topology specified in `ha-plan.md`,
binding the following decisions:**

### 1. Single-region HA first; multi-region DR second

At launch we run exactly **one** region (R1 / Frankfurt) at full
HA. R2 and R3 join over Weeks 6–8 with the same per-region shape.
Multi-region active/active is **explicitly out of scope for v1.**
What we have is per-region full HA + cold DR in cloud + cross-
region async replicas (per ADR-0016) for read-only failover.

### 2. Decouple ingest from serving — three failure domains

- **Hot tier (≤30s window):** Redis cluster (3 masters + 3
  replicas + Sentinel). Per ADR-0007.
- **Warm tier (≤90 days raw, indefinite for 1h+ aggregates):**
  TimescaleDB Patroni-managed HA — 1 primary + 2 sync replicas.
- **Cold tier (raw history, indefinite):** MinIO erasure-coded
  EC(6+3) across 9 hosts; bucket versioning on.

Three tiers, three failure domains. **Ingest must never block
serving.** If the ingestion plane slows, the serving plane returns
stale-marked responses (per the ADR-0015 closed-bucket contract +
the envelope `flags.stale=true`). Never errors.

### 3. Redundancy is N+1 minimum; N+2 for stellar-core

Every stateful component runs ≥ N+1. stellar-core / galexie /
stellar-rpc run **N+2** because the Tier-1 aspiration (ADR-0004)
requires three independent archives post-launch and we want the
Tier-1 fleet to survive a single-host failure plus a single-host
maintenance window concurrently.

### 4. Stateless services scale horizontally; one leader-elected aggregator

`ratesengine-api` runs as N=3 stateless instances behind HAProxy
(keepalived VIP). `ratesengine-indexer` runs one process per
configured source (per-source orchestration). `ratesengine-
aggregator` runs **one active + one standby**, leader-elected via
a Redis lease — only one instance writes to the trades hypertable
at a time to avoid duplicate emissions.

### 5. Colocated bare metal primary; cloud DR

Captive-core + galexie + Postgres + MinIO live on dedicated
R640-class colocated hardware (per ADR-0002 alternatives — the
3× cost differential vs cloud IOPS-matched instances at our scale
ratifies this). Cloud (AWS) is the DR target:

- **Stateless services:** warm-standby in AWS, scale-to-zero,
  scale-out on DNS flip.
- **TimescaleDB:** async logical replica via pg_logical at AWS
  RDS, **5-minute RPO** budget.
- **Redis:** NOT replicated cross-region. Warm-standby is cold;
  re-hydrates from Timescale within minutes after failover.
- **MinIO:** `mc mirror` to AWS S3, **1-hour RPO** for the
  archive bucket; `galexie-live/` replicated at 5 min.
- **stellar-core / stellar-rpc:** NOT replicated. Rebuilt from
  our own MinIO archive on DR activation (~4 h to
  `CATCHUP_RECENT`). Running captive-cores in AWS would
  violate the cost envelope.

### 6. Every component has a defined degraded mode up front

The API never silently fails. Per `ha-plan.md` §9, each component
failure has a documented degradation:

- Indexer source down → response includes `sources` list with the
  outage marked + envelope `flags.reduced_redundancy=true`.
- Aggregator down → API serves the last successfully published
  aggregate row + `flags.stale=true` with `as_of` timestamp.
- Redis down → API queries Timescale directly + `flags.stale=true`.
- Timescale primary down → automatic failover to sync replica via
  Patroni (RPO 0; RTO ~30s).
- All three regions disagree on a closed bucket →
  `cross-region-monitor` alerts; serving continues from each
  region's local view.

Anything that changes a `flags.*` value gets an explicit ADR or
test pinning the contract.

### 7. Aspirational cost envelope — colo + cloud DR ≤ $80k/year

Per `ha-plan.md` §12 the per-region cost target is ~$30k/year
hardware amortisation + ~$20k/year cloud DR + ~$30k/year colo
power/bandwidth for R1. R2 and R3 land under ADR-0016's hybrid
shape so they're cheaper than a third full-stack copy. This is a
**target**, not a binding constraint — the architectural shape is
load-bearing; the budget is informative.

## Consequences

- **Positive — covers the 99.99 % SLA without vendor lock-in.**
  Self-hosted bare metal on rented colo space; cloud as DR fallback;
  every component has a defined failure mode. The numbers align
  per the napkin math in `ha-plan.md` §4.

- **Positive — three-tier separation makes the degradation
  contract enforceable.** Each tier's failure has a clear answer
  for the API. Reviewers can call out a PR that introduces a path
  bypassing the contract (e.g. a handler that reads MinIO
  synchronously on a /v1/price hit) without arguing about whether
  it's "really" a violation.

- **Positive — N+2 for stellar-core defends the ADR-0004
  aspiration.** Three independent archives are a hard requirement
  for Tier-1 quality; N+2 deployment ensures we don't fall below
  three even during single-host maintenance.

- **Negative — operational complexity.** Patroni, keepalived,
  Sentinel, leader-election, mc mirror schedules — every
  redundancy layer is a moving part. Mitigated by the runbook
  catalog (`docs/operations/runbooks/`) requiring one runbook per
  alert + the SEV playbook tying everything together.

- **Negative — colo + cloud is a hybrid posture.** Egress charges
  on cloud, manual hardware refresh on colo, two failure-mode sets
  to operationalize. Justified by the cost envelope (cloud-only at
  our IOPS profile is 3× more expensive) and the existing R1
  hardware. Re-evaluate if either factor changes.

- **Operational impact — every PR that adds a service needs to
  declare its tier, redundancy, and degradation mode.** Captured
  as a checklist line in the PR template (added in a follow-up to
  this ADR).

- **Downstream design impact — this ADR fixes the shape; specific
  decisions about backup retention, alert thresholds, and
  failover procedures are runbooks in
  `docs/operations/runbooks/`.** Their content evolves; this ADR
  doesn't.

## Alternatives considered

1. **Full cloud (no colo)** — rejected per ADR-0002 alternatives.
   The 3× cost differential at our IOPS profile + the captive-core
   fleet's existing R640 provisioning make hybrid the right call.

2. **Multi-region active/active at v1** — rejected. The 10-week
   delivery window doesn't permit multi-master Postgres / Redis at
   launch. ADR-0016 picks up cross-region read replicas with
   ADR-0015's closed-bucket invariance providing the
   "byte-equivalent across regions" property; that's enough for v1.

3. **Single-replica Postgres (warm standby only)** — rejected.
   Patroni with two sync replicas costs negligibly more in
   storage and earns RPO=0 + automatic failover. The ops cost of
   manual standby promotion under stress would be far higher than
   the marginal infra cost.

4. **Run stellar-core in AWS for DR** — rejected. Captive-core
   IOPS in cloud at 8 vCPU / 32 GB scale violates the cost
   envelope. Re-bootstrapping from our own MinIO archive in ~4h is
   acceptable for a DR scenario where the entire colo is offline
   (which is itself a multi-failure scenario beyond what 99.99 %
   uptime targets).

5. **Stateless API + serverless aggregator (Lambda / Cloud Run)**
   — rejected. Cold-start latency would breach the p95 ≤ 200ms /
   p99 ≤ 500ms targets (ADR-0009 — to land — for the latency
   contract). Redis-leader-elected daemon in colo is the right
   profile for steady-state aggregation.

## References

- [`docs/architecture/ha-plan.md`](../architecture/ha-plan.md) —
  full design; this ADR ratifies its binding decisions.
- [`docs/architecture/coverage-matrix.md`](../architecture/coverage-matrix.md)
  §S9.1 — the 99.99 % uptime requirement this ADR closes.
- [`docs/architecture/infrastructure/multi-region-topology.md`](../architecture/infrastructure/multi-region-topology.md)
  — cross-region (R1/R2/R3) topology layered on top of this
  per-region one.
- [`docs/architecture/infrastructure/archival-node-spec.md`](../architecture/infrastructure/archival-node-spec.md)
  — per-host hardware spec for the colo fleet.
- ADR-0001, 0002, 0004, 0007, 0015, 0016 (cross-references above).
