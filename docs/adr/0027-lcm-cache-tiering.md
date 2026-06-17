---
adr: 0027
title: LCM cache tiering — local galexie-archive as hot, aws-public-blockchain as cold
status: Proposed
date: 2026-05-20
supersedes: []
superseded_by: null
---

# ADR-0027: LCM cache tiering — local galexie-archive as hot, aws-public-blockchain as cold

## Context

R1's ZFS pool is sized for the entire pubnet history mirrored
locally. At 2026-05-20:

| Dataset | Used | Notes |
|---------|------|-------|
| `data/archive` | 6.95 TB | Stellar history-archive (HASH / ledger / transactions / results / scp / buckets). Tier-A + Tier-D verification primitive (ADR-0017). |
| `data/minio` | 4.96 TB | MinIO buckets: `galexie-archive` (durable LCM mirror, genesis→tip), `galexie-live` (rolling working set), `backups` (misc). |
| `data/postgres` | 610 GB | TimescaleDB hot data + CAGGs. |
| `data/galexie` | 7.7 GB | galexie's local working dir (state files). |

Pool: 27.7 TB raw → ~13.85 TB usable (raidz2 ×2 parity). 12.5 TB
used → 93% full, 1.35 TB free. The
2026-05-17 SEV showed what
happens at 100% — write amplification kills everything. We
recovered losslessly via a compression cascade, but the headroom
is now structurally tight: every new TimescaleDB chunk, every
backfill range write into trades, every CAGG materialisation
eats into the same 1.35 TB. The user constraint is explicit:
"I cannot expand the capacity of this server."

ADR-0016 already established that **R2 reads galexie LCM data
directly from `aws-public-blockchain` S3** at sub-15ms latency, no
local mirror — the AWS Open Data Sponsorship publishes every
pubnet LCM at
`s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`. For R1
the cross-Atlantic RTT (~80 ms) ruled out using it as the **live**
LCM source. But for **older ranges**, latency is amortised — a
backfill that processes 10⁵–10⁶ ledgers in a single job tolerates
80 ms per-object easily because galexie writes whole-partition
manifests (one S3 GET pulls 64 ledgers' worth of metadata). The
AWS bucket is therefore a viable cold-tier target *for ranges
older than the live window*, even from R1.

The 4.96 TB `data/minio` is the biggest single lever sized to
local tier-out without touching the integrity-leader role of
`data/archive`. Of that 4.96 TB, `galexie-archive` is genesis-to-
tip (the bulky multi-year history); `galexie-live` is the rolling
recent working set galexie writes into before promotion; `backups`
is small (~tens of GB). Trimming `galexie-archive` of cold ranges,
with the AWS public bucket as the rehydration tier, is the focus
of this ADR.

`data/archive` (the history-archive mirror) is a separate problem:
it's not a "cache" of anything — it IS the integrity primitive
for Tier-A + Tier-D (ADR-0017). Tiering it out forfeits R1's role
as the cross-region verification leader (ADR-0016: "R1 is the
full-mirror integrity leader; R2 + R3 trust R1's primary
verification but run their own Tier A + Tier D periodically as
defence-in-depth"). A separate ADR (deferred) would explore
chunked-tier history-archive offload to Vultr Object Storage —
out of scope here.

## Decision

Implement **two-tier LCM read path** for r1, with `galexie-
archive` (local MinIO) as **hot** and `aws-public-blockchain` S3
as **cold**, and a periodic trim operator that deletes
cold-eligible LCMs from local storage once they're verified
present upstream.

### Tier boundaries

- **Hot tier (local `galexie-archive`):** ledgers in
  `[tip - HotWindow, tip]`. `HotWindow` defaults to **90 days**
  (~1.56 M ledgers at 5 s ledger close time). Live ingest +
  re-ingest within the verifiable-replay-window
  (`config.verify.replay_window`) stays sub-15ms.
- **Cold tier (`aws-public-blockchain` S3 — read-only):** every
  ledger ever, including hot. R1 doesn't write to it; we depend
  on AWS Open Data's freshness SLA (LCMs land within
  ~30 minutes of close per the public bucket's manifest).

### Read path (`internal/ledgerstream`)

The `ledgerstream` package gains a **fallback source** chain:

```go
type ledgerSource struct {
  hot  s3compat.Reader  // local MinIO galexie-archive
  cold s3compat.Reader  // aws-public-blockchain (HTTPS, no auth)
}

func (s *ledgerSource) ReadLCM(ctx context.Context, seq uint32) (lcm xdr.LedgerCloseMeta, err error) {
    if lcm, err = s.hot.GetObject(ctx, lcmPath(seq)); err == nil {
        return lcm, nil
    }
    if !errors.Is(err, s3compat.ErrNoSuchKey) {
        return lcm, fmt.Errorf("hot read: %w", err)
    }
    // Cold fallback — only on NoSuchKey, never on transient
    // network errors. Avoids silently masking a misconfigured
    // hot endpoint by always hitting the cold path.
    return s.cold.GetObject(ctx, lcmPath(seq))
}
```

The `hotMiss / coldHit` outcomes feed two new Prometheus metrics:
`ledgerstream_tier_read_total{outcome="hot"|"cold"|"both_missing"}`
and `ledgerstream_cold_read_duration_seconds`. Operators chart
`cold` rate as a proxy for "is the trim window correctly sized,
or am I paying cross-Atlantic latency for ranges that should
be hot?". A cold-rate spike on the live ingest path = trim
window too tight; cold-rate on backfill is expected.

### Trim operator (`stellarindex-ops trim-galexie-archive`)

New subcommand:

```
stellarindex-ops trim-galexie-archive \
  --older-than 90d \
  --verify-upstream \
  --dry-run | --commit
```

- `--older-than` accepts duration or absolute ledger sequence;
  default is the configured `HotWindow`.
- `--verify-upstream` does a HEAD against the AWS bucket for
  each LCM about to be deleted; never delete a local object
  without first proving the upstream copy exists. (One in
  flight at a time, ~5ms per HEAD → ~hours for the first run
  on a full mirror; acceptable for a one-time + monthly cadence.)
- `--dry-run` lists deletions without executing.
- `--commit` is required for actual deletion (typo guard).
- Operator runs monthly from a systemd timer
  (`galexie-archive-trim.timer`) once initial bulk-trim is
  done. Bulk-trim is operator-triggered, not on a timer, to
  avoid pool-pressure surprises.

### Rollback

If the cold-tier read path proves unreliable, rehydrating is
mechanical: `stellarindex-ops rehydrate-galexie-archive --from <seq>
--to <seq>` GETs ranges from AWS and writes them back to local
MinIO. The local bucket layout is identical to the upstream
(galexie uses the same partition scheme), so rehydration is
byte-equivalent — no decode/re-encode step. Verifies via
SHA-256 against AWS's published checksums per partition.

### Sequencing — when do we cut over

1. **Land the dual-source read path** in `ledgerstream` behind a
   `LCM_TIER_ENABLED=false` flag. Defaults off; existing single-
   source path unchanged. Smoke-test in r1's test harness.
2. **Land the trim operator + rehydrate operator.** Run in
   `--dry-run` mode against the current archive. Verify the
   numbers match what we'd predict (~3-4 TB freeable at the
   90 d hot window with full-genesis-to-tip mirror).
3. **Enable the dual-source flag in r1's `/etc/stellarindex.toml`.**
   Hot-path remains the only source served until #4 deletes
   something cold; verifies live ingest behaviour unchanged.
4. **First bulk trim — operator-triggered.** Trim
   `[genesis, tip - 90d]` in 1M-ledger chunks, with a
   `Pause-If-Pool-Capacity-Exceeds 90%` safety guard between
   chunks (gives us back ~3-4 TB; pool drops from 93% to
   ~65%). After this run, the cold path will see
   non-zero traffic for any backfill of cold ranges (#35's
   Soroban-era range is the immediate beneficiary).
5. **Monthly trim cadence.** New ledgers age into cold tier
   one month at a time; the timer trims the freshest month
   that's past the 90 d cutoff.

### What this does NOT change

- **Hot live ingest path is unchanged.** Indexer continues to
  read from local MinIO for tip-following; that's the read
  path that's IOPS-sensitive and live.
- **History-archive tier (`data/archive`) is untouched.** Tier-A
  and Tier-D verification (ADR-0017) keep running against the
  full local copy. A future ADR may explore chunked offload of
  cold history-archive ranges to Vultr Object Storage; deferred.
- **`galexie-live` is untouched.** Galexie's writer state
  expects a contiguous local working set — trim never touches
  ranges newer than the configured live cursor.
- **R2 + R3 storage shapes (ADR-0016) are unchanged.** This is
  an r1-specific optimisation. R2 already reads cold direct from
  AWS; R3 already uses Vultr Object Storage.

## Consequences

- **Positive: Resolves the 93%-pool structural-tight headroom.**
  ~3-4 TB recovered at 90 d hot window; raises post-trim
  free-pool from 1.35 TB to ~4.5-5 TB. Unblocks #30
  (composite index on 2.7B-row trades hypertable) and #35
  (Soroban-era 50.5M–62.2M backfill resume — the SEV-class work
  frozen since the 2026-05-17 incident).
- **Positive: Faster recovery from a future hot-corruption
  event.** Local galexie-archive corruption (a bad MinIO disk,
  an accidental delete) becomes recoverable from AWS rather
  than requiring an offsite restore.
- **Positive: Symmetric with R2's storage shape.** R1 + R2 share
  the same logical read path for cold ranges; only the hot
  tier differs.
- **Negative: New external dependency on AWS Open Data
  Sponsorship.** The bucket is sponsored, not contractually
  guaranteed. We document its absence as a P3 alert (the cold
  path's HEAD-checks-fail-rate) and a runbook (rehydrate the
  trimmed range from upstream if available, else recover from
  R2's stored copies via cross-region pull). The sponsorship
  has been stable since 2024-Q3; not seen as a near-term risk.
- **Negative: Backfill of cold ranges pays cross-Atlantic
  latency.** ~80 ms per AWS GET amortised over 64-ledger
  partitions = ~1.25 ms/ledger; for a 12M-ledger backfill (#35)
  that's ~4 h of latency overhead on top of decode/write. Acceptable
  for the once-per-source operator action.
- **Negative: Bulk trim is hours-long + IOPS-intensive.**
  HEAD-verify + DELETE across ~70k partitions takes hours.
  Run during low-traffic windows; the safety guard prevents pool
  amplification.
- **Negative: Cold-tier reads add Prometheus cardinality.**
  Two new metrics, three outcomes each. Negligible at our
  metric-volume budget.

## Out of scope (separate ADRs)

- **History-archive offload to Vultr Object Storage.** The 6.95 TB
  `data/archive` is the bigger pool consumer but is the
  integrity-leader for ADR-0017 verification — offloading it
  forfeits that role. A future ADR may explore chunked offload
  with a cold-cache local index for the verification sweeps.
- **galexie-live → galexie-archive promotion cadence tuning.**
  Currently galexie's default; not blocking this work.
- **PostgreSQL chunk retention beyond the current
  `drop_chunks` policy.** trades, prices_1m, etc. have their
  own retention story per ADR-0006 + the chunk policy in
  migration 0001.
