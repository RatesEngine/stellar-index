---
adr: 0016
title: Per-region storage strategies for archival nodes (Hetzner full / AWS hybrid / Vultr hybrid)
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0016: Per-region storage strategies for archival nodes

## Context

Each region of the Stellar Index fleet (R1 Frankfurt, R2 US-East,
R3 Singapore per `infrastructure/multi-region-topology.md`) needs
to ingest galexie ledger-meta data into its indexer. The naive
plan — every region runs the same Hetzner-style "mirror everything
locally" stack — works but pays for storage we don't need at every
region. ADR-0015 establishes that what the API actually serves
(closed-bucket VWAP/TWAP/OHLC rows) is byte-equivalent across
regions; what differs is *how each region gets the input data*.

Two structural facts shift the analysis per region:

1. **AWS publishes a sponsored, content-addressed copy of the entire
   pubnet galexie dataset at `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`.**
   For a region inside us-east, reading from that bucket directly
   is sub-15 ms latency and free egress (Open Data Sponsorship).
2. **Vultr has S3-compatible Object Storage co-located with their
   Singapore bare-metal facility.** Pricing is ~$0.005/GB-month vs
   ~$0.080/GB-month for EBS-grade block storage — a 16× delta on
   the bulky read-mostly data.

These let us run *different storage shapes per region* without
breaking any consistency property:

- R1 Frankfurt: Hetzner bare metal + raidz2 NVMe (full local mirror).
  No nearby content-addressed source; transatlantic AWS-public
  reads add 80 ms RTT per object — too slow for live ingest. Local
  mirror is the only viable shape.
- R2 US-East-1: read galexie data directly from
  `aws-public-blockchain` S3, no local mirror. The 4.76 TB galexie-
  archive isn't on r2's disks at all; the indexer streams from
  AWS-internal S3.
- R3 Singapore: Vultr bare metal + Vultr Object Storage. galexie-
  archive (4.76 TB) lives on Object Storage at ~$25/mo; postgres +
  galexie-live + OS on the bare metal's local NVMe.

## Decision

**Adopt three different storage strategies, one per region, all
serving the same closed-bucket API contract.**

### R1 (Frankfurt) — full local mirror

- Storage: 4 × 7.68 TB NVMe raidz2 (current Hetzner spec)
- galexie-archive: local MinIO (~4.76 TB)
- /srv/history-archive: full SDF mirror (~7 TB)
- postgres + galexie-live + captive-core state: local NVMe
- **Verification capability:** all four tiers (A, B, D, E) run
  locally. R1 is the *integrity leader* — its periodic verify
  outputs are the trust anchor for r2 and r3.

### R2 (US-East-1, AWS) — AWS-hybrid (no local galexie mirror)

- Compute: AWS EC2 r7i.4xlarge (16 vCPU / 128 GiB) with 1-yr
  Compute Savings Plan
- Storage: ~1-2 TB EBS gp3 for postgres + galexie-live + OS
- galexie-archive: read directly from
  `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`
  via galexie's S3 datastore config
- /srv/history-archive: NOT mirrored locally; r2 trusts r1's
  Tier B + E verification
- **Verification capability:** Tier A (chain integrity, no external
  data needed) + Tier D (multi-peer HTTPS cross-check) run
  locally on a weekly cron. Tier B + E delegated to R1.
- **Estimated cost:** ~$10,500-13,000/year (compute + EBS + EBS
  snapshots + bandwidth) vs ~$15,000/year for full-mirror shape.
  See `multi-region-topology.md` §X for the price breakdown.

### R3 (Singapore, Vultr) — bare-metal + object-storage hybrid

- Compute: Vultr Bare Metal Singapore, Intel Xeon E-2388G
  (8c/16t Coffee Lake, 128 GB DDR4 ECC) + 2 × 1.92 TB local NVMe
  (Vultr's standard SG SKU — RAID-1 → 1.92 TB usable). List price
  **$350/mo ($0.479/hr on the 730 h/mo basis)**.
- galexie-archive: Vultr Object Storage (S3-compatible, region-local
  to Singapore facility) at ~$25/mo for 5 TB. Mandatory because the
  4.76 TB galexie-archive doesn't fit in 1.92 TB local.
- postgres + galexie-live (rolling ~30 days) + captive-core state +
  OS: local NVMe. ~500 GB-1 TB working set; comfortable in 1.92 TB.
- Older galexie-live (>30 days) tiered to Object Storage if needed,
  or pruned (regional async-replica role doesn't need long retention —
  postgres replication from R1 is the canonical history).
- /srv/history-archive: NOT mirrored locally; r3 trusts r1's
  Tier B + E verification.
- **Verification capability:** same as r2 — Tier A + D run
  locally weekly; Tier B + E delegated to R1.
- **Estimated cost:** $350/mo bare metal + ~$25/mo Object Storage =
  **~$375/mo ≈ $4,500/year**. Vultr does not offer formal commit
  discounts; sales may negotiate ~5-10 % off list for annual prepay.
- **Redundancy trade-off:** RAID-1 across 2 drives (single-drive
  failure tolerance) vs r1's raidz2 across 4 drives (two-drive
  tolerance). Acceptable for an async DR replica because a
  multi-drive failure on r3 is recoverable by promoting r1 or
  rebuilding r3 from scratch via the bring-up recipe (~half day).

### Trust model and defence-in-depth

R1 is the *integrity leader*: its periodic Tier A + B + D + E
verification establishes that R1's local galexie-archive bytes are
network-correct. R2 and R3 do not duplicate Tier B + E (which would
require local /srv/history-archive mirrors), but they DO run two
checks that catch upstream divergence cheaply:

1. **Local Tier A on a cron** (~weekly): walks each region's own
   ingested ledgers and confirms `header.PreviousLedgerHash ==
   prev.LedgerHash`. Catches local corruption and any
   internally-inconsistent stream from upstream (e.g. AWS bucket
   gets bit-flipped during republish).

2. **Local Tier D on a cron** (~weekly): HTTPs to ~6 tier-1
   validator archives (LOBSTR, SatoshiPay, Blockdaemon, SDF,
   PublicNode, Franklin Templeton) and compares 20 sampled
   checkpoint hashes against the local view. Catches **forks**
   (where a chain is internally consistent but doesn't match
   the network's signed reality).

Optional layer (defer until needed): a per-region `(ledger_seq →
sha256(LCM bytes))` hash database populated as the indexer reads.
If upstream retroactively rewrites bytes for a previously-seen
ledger, the hash mismatch is detected on next read. Cost:
~32 bytes × 62 M ledgers = ~2 GB per region. Implement only if
ops sees an actual drift event.

Cross-region consistency check (orthogonal to the above): a
monitoring job samples a few `(pair, window, from_ts)` triples
across r1/r2/r3 and asserts the closed-bucket VWAP rows match.
If indexer output ever diverges across regions, the alert fires
within minutes. This is the *strongest* check because it tests
the actual outcome the API serves rather than intermediate bytes.

## Consequences

- **Positive — total fleet storage cost meaningfully lower than
  three identical r1-spec boxes.** Estimated annual fleet cost in
  the chosen shape (R1 Hetzner + R2 AWS hybrid + R3 Vultr hybrid):
  ~$5K + ~$11K + ~$4.5K = **~$20.5K/year** vs an all-bare-metal
  fleet at ~$35-45K/year. Saves ~$15K-25K/year while preserving
  the consistency property and adding cross-region defence-in-depth.

- **Positive — each region's storage shape matches its provider's
  natural strengths.** AWS lets us read from public S3 cheaply;
  Vultr's Object Storage is dramatically cheaper than EBS at our
  scale; Hetzner's bare-metal-with-NVMe is what we already have
  on r1.

- **Positive — defence-in-depth doesn't depend on local mirrors.**
  Tier A + D cover the realistic upstream-divergence failure modes;
  the cross-region CAGG check catches indexer-output divergence;
  forks are caught by multi-peer cross-validation.

- **Negative — r2 and r3 cannot independently verify their bytes
  against SDF's signed history archive.** If r1 is destroyed AND
  external validator archives are simultaneously unreachable,
  r2/r3 can't run a Tier B check from cold. Acceptable: that's a
  triple-failure scenario; recovery path is rebuild /srv/history-
  archive on demand (~4 h from SDF).

- **Negative — r2's reliance on AWS public bucket is a single-
  point-of-failure for ingest.** If AWS deprecates the public
  bucket or it goes hostile, r2 needs an alternative ingest path
  (typically: switch to mirroring from r1's MinIO, which would
  require provisioning ~5 TB of EBS — turns r2 from hybrid to
  full-mirror at runtime). Mitigation: monitor `aws-public-
  blockchain`'s availability via Tier A failures; have the
  fallback documented in r2's runbook.

- **Operational impact — three different storage shapes mean three
  different runbooks** for "the box's disks filled up", "the
  upstream ingest source went down", "what to back up", etc. The
  archival-node-bringup.md doc grows a per-region appendix.

- **Downstream design impact — postgres growth bounds the long-term
  cost** at all three regions. Per ADR-0006 retention, raw trade
  rows compress 10× after 7 days and the hourly+ aggregates live
  forever. Postgres reaches steady-state at ~3-5 TB after several
  years; this fits comfortably on every region's spec.

## Alternatives considered

1. **Identical full-mirror at every region (Hetzner-shape on AWS
   and Vultr).** Rejected: ~$45 K/yr vs ~$30 K/yr for the chosen
   shape, with no consistency benefit (closed-bucket API contract
   makes the storage shape invisible to clients per ADR-0015).
   Plus: AWS at full-mirror requires 13 TB EBS at ~$1,000/month —
   absurd vs reading from the public bucket for free.

2. **R2 mirrors from r1's MinIO over the WAN** (instead of reading
   from AWS public bucket). Rejected: trans-Atlantic read latency
   per object is ~85 ms vs ~5-10 ms for AWS-internal. Cost is
   bandwidth ($0.09/GB egress from Hetzner) × 4.76 TB = ~$430 one-
   time. Manageable but pointless when the same bytes sit in
   Virginia at zero latency from R2.

3. **R3 mirrors from R1 instead of using Vultr Object Storage.**
   Rejected: Singapore-Frankfurt RTT is 165 ms; live-tail re-mirror
   would lag noticeably. Vultr Object Storage in the same
   Singapore facility is region-local (~5-10 ms) at $25/mo for the
   whole dataset.

4. **All three regions on AWS (replicate the hybrid pattern).**
   Rejected: r1's compute + EBS at AWS would cost ~$15K/yr vs
   Hetzner's ~$5K/yr for equivalent perf. We chose Hetzner for
   r1 specifically to avoid the AWS premium on the workload-heavy
   primary; doubling down would erase that advantage.

5. **All-bare-metal across all three regions (no cloud).**
   Rejected: per-region storage utilisation is heavily skewed —
   r1 needs the local mirror, r2/r3 don't. Forcing identical
   bare-metal shapes pays for storage that goes unused on the
   replicas.

## References

- [ADR-0015](0015-last-closed-bucket-rate-serving.md) — the
  consistency property that makes per-region storage divergence
  invisible to API clients.
- [`docs/architecture/infrastructure/multi-region-topology.md`](../architecture/infrastructure/multi-region-topology.md)
  §6 — the concrete per-region topology this ADR specifies.
- [`docs/operations/archival-node-bringup.md`](../operations/archival-node-bringup.md)
  — the bring-up recipe (per-region appendices to be added when
  r2/r3 are provisioned).
- [`docs/operations/galexie-backfill.md`](../operations/galexie-backfill.md)
  — the AWS-public-bucket-as-source pattern, originally documented
  for r1's bring-up but applicable to r2's continuous ingest.
