---
adr: 0017
title: Archive completeness invariants and dual-archive integrity model
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0017: Archive completeness invariants and dual-archive integrity model

## Context

Two physical archives back the indexer + the verifier. They serve
different purposes and have been treated as separate ad-hoc concerns:

1. **Primary archive** — `galexie-archive/` MinIO bucket on R1. Per-
   ledger XDR meta files (`<HASH>--<LEDGER>.xdr.zst`), one per
   ledger, ~62 M objects covering pubnet history. The indexer reads
   from this; it is the *source of rate data*.

2. **Cross-anchor archive** — `/srv/history-archive/` on R1, a
   traditional Stellar history archive (`bucket/`, `history/`,
   `ledger/`, `results/`, `scp/`, `transactions/`). Mirrored from
   `https://history.stellar.org/prd/core-live/core_live_001`. Used
   by `ratesengine-ops verify-archive -tier checkpoint` to confirm
   our LCM hashes match SDF's signed checkpoint hashes every 64
   ledgers.

Two real findings on 2026-04-27 motivated this ADR:

- The R1 verify-archive walk crashed at ledger 40,000,000 because
  ~35,000 contiguous per-ledger files were missing from
  `galexie-archive/`. Surrounding partitions (40M–40.5M range) were
  also 28–42K files short of the expected 64,000.
- The same walk reported `checkpointsMissed=6,273` against
  `/srv/history-archive/`. Investigation showed that counter is
  documented as "archive file absent, not a failure" — i.e. the
  verifier was *tolerating* gaps in the cross-anchor archive,
  silently skipping cross-checks at every gap. ~6,782 of 972,652
  expected `ledger-*.xdr.gz` files are actually missing.

The verifier's tolerance default ("missed = skip rather than fail")
plus the absence of a continuous completeness check meant gaps
accumulated unnoticed across both archives.

## Decision

**Archive completeness is a hard invariant, not a soft tolerance.
Both archives have explicit numerical contracts that must be true
at all times in steady state, and a daily process is responsible
for restoring the invariant when it breaks.**

### The four hard contracts (R1)

1. **Primary structural completeness.** For every closed partition
   `<HASH>--<N>-<N+63999>/` in `galexie-archive/` where `N+63999 <
   network_head`, file count is exactly 64,000. The currently-open
   partition has `network_head − partition.start + 1` files.

2. **Primary chain-link integrity.** For every adjacent pair `(N,
   N+1)` in `galexie-archive/`,
   `SHA256(ledger[N].header) == ledger[N+1].previousLedgerHash`.
   Sequence gaps are themselves chain breaks. No tolerance.

3. **Cross-anchor structural completeness.** For every ledger
   `seq` where `seq % 64 == 63` and `seq <= network_head`, the file
   `/srv/history-archive/ledger/XX/YY/ZZ/ledger-XXYYZZWW.xdr.gz`
   exists and decodes cleanly (`gzip -t` passes; SDK can read all
   64 entries).

4. **Cross-anchor anchor verification.** For every checkpoint
   `seq` where `seq % 64 == 63`, the cross-anchor file's
   `LedgerHeaderHistoryEntry.Hash` for ledger `seq` equals our
   primary's hash for ledger `seq`. No mismatches.

`verify-archive -tier all` is hardened so that **any** of these
failing aborts with non-zero exit. The pre-2026-04-27 default
(`checkpointsMissed > 0` → tolerated) is removed.

### Per-region application of the contracts

Per [ADR-0016](0016-per-region-storage-strategy.md), the three
regions have different storage shapes; the completeness contracts
apply asymmetrically.

| Region | Primary archive | Cross-anchor archive | Local checks | Trust model |
|---|---|---|---|---|
| **R1 Frankfurt** | `galexie-archive/` MinIO (full local mirror, ~4.76 TB) | `/srv/history-archive/` (full SDF mirror, ~7 TB) | All four contracts daily | Integrity leader for the fleet |
| **R2 US-East** | Reads `s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/` directly (no local mirror) | None local | Contract 2 (chain-link) weekly + Tier D (multi-peer) weekly | Trusts R1 for contracts 1, 3, 4 |
| **R3 Singapore** | `galexie-archive/` on Vultr Object Storage (region-local hybrid, ~5 TB) | None local | Contract 1 (structural) on local copy + contract 2 (chain-link) weekly + Tier D weekly | Trusts R1 for contracts 3, 4 |

R1 is the integrity leader. Its daily completeness verification
output is published to a central metric endpoint that R2/R3 read as
a precondition for declaring themselves healthy. If R1's last
successful run is older than 26 hours, R2 and R3 mark themselves
*reduced redundancy* (the same `ReducedRedundancy` envelope flag
the API already surfaces).

R2 and R3 don't duplicate contracts 3 + 4 because:
- They have no local cross-anchor archive (it's ~7 TB and
  redundant with R1's).
- The cross-region CAGG-comparison alert (already specified in
  ADR-0016 §"Trust model") catches indexer-output divergence
  faster than re-running checkpoint verification at every region
  would.

## Consequences

- **Positive — gap-detection latency drops from "next manual
  verify-archive run" to ≤ 26 h.** Any new gap in either archive
  is detected within one cron cycle and either auto-repaired or
  pages an operator.

- **Positive — verify-archive's exit code is now meaningful.**
  Pre-2026-04-27 a non-zero `checkpointsMissed` was a logged-but-
  ignored stat. Post-ADR, the verifier's pass/fail signal is the
  ground truth for archive integrity at all four contract levels.

- **Positive — defence-in-depth at R2/R3 is explicit, not
  implicit.** The trust delegation from R2/R3 to R1 was previously
  documented in ADR-0016 prose; this ADR ties it to a concrete
  staleness budget (26 h) and an envelope flag.

- **Negative — daily cron adds operational surface.** A new
  systemd timer, a new Prometheus metric set, a new alert family,
  a new runbook. Net cost: small. Per-run wall-clock for an
  incremental day-of-data check is < 2 minutes on R1.

- **Operational impact — bootstrap is one-shot, ~12 hours.**
  Before the daily cron can begin enforcing the invariants,
  R1's existing gaps must be filled. This is documented in
  `docs/operations/archive-completeness.md` §"Bootstrap procedure".

- **Downstream design impact — the API's `ReducedRedundancy`
  envelope flag becomes load-bearing.** When R1's last successful
  completeness run is stale, R2 and R3 set this flag on every
  response. Customers that depend on full-fleet integrity (e.g.
  divergence-monitoring partners) gate on this flag. Today the
  flag is documented but unused; this ADR pins down its first
  semantic meaning.

## Alternatives considered

1. **Keep the "missed = tolerated" default; document it as known
   limitation.** Rejected: the verifier's exit code becomes
   meaningless for integrity claims; downstream policies (the
   API's `ReducedRedundancy` flag, the R2/R3 trust delegation in
   ADR-0016) lose their anchoring point.

2. **Run completeness verification per ingest cycle (every ~5 s).**
   Rejected: that's 12,960× more frequent than needed for a system
   where new data arrives in ~17,280 ledger/day chunks. Daily is
   the right granularity; faster adds load without additional
   evidence.

3. **Mirror `/srv/history-archive/` to R2 and R3 so each region
   verifies all four contracts independently.** Rejected: ~7 TB
   per region for a defence-in-depth check whose unique signal
   over Tier A + D is small. The ADR-0016 trust model — R2/R3
   delegate to R1 for contracts 3, 4 — is the cheaper shape and
   was explicitly chosen there. This ADR commits to that shape.

4. **Use SDF's published archives directly as the cross-anchor
   without a local mirror.** Rejected: every checkpoint verify
   would do an HTTPS round-trip to SDF; running the full chain
   walk would issue 972,652 requests and depend on SDF's archive
   being reachable + the rate limits permitting. The local mirror
   is ~7 TB at one-time cost and lets verification run cold.

## References

- [ADR-0015](0015-last-closed-bucket-rate-serving.md) — the
  closed-bucket API contract that the completeness invariants
  protect.
- [ADR-0016](0016-per-region-storage-strategy.md) — per-region
  storage shapes; this ADR commits to the trust model that ADR
  describes.
- [`docs/operations/archive-completeness.md`](../operations/archive-completeness.md)
  — the implementation procedure (bootstrap + daily cron + the
  `ratesengine-ops archive-completeness` tool).
- [`docs/operations/galexie-backfill.md`](../operations/galexie-backfill.md)
  — the original backfill procedure; the completeness daemon
  reuses its `galexie-archive-fill` script for primary repair.
- [`docs/operations/alerts-catalog.md`](../operations/alerts-catalog.md)
  — the new `ratesengine_archive_*` alert family this ADR adds.
