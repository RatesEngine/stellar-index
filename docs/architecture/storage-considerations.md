# Storage considerations — r1 knowledge base

> Living document. Captures r1's storage layout, per-dataset
> touchpoints, trade-offs around space reclamation, and the
> rationale for any decisions we've made (or are pending). Not an
> ADR — when a decision IS made here, an ADR captures the
> commitment; this doc captures the **why we considered the options
> we did**.
>
> Maintenance: append findings as they arise. Mark obsolete sections
> rather than deleting (we want to be able to trace why we ruled
> something out). Last-modified header per section.

---

## Audience

If you (Claude, human, future agent) are about to:

- Recommend trimming, deleting, or moving a multi-TB dataset on r1
- Change which storage tier serves which read path
- Adjust ADR-0016 (per-region storage) or ADR-0017 (archive
  completeness) or ADR-0027 (LCM cache tiering)
- Diagnose "why is r1's pool at 93%"

**Start here.** Most of the bad-recommendation paths I've taken
across sessions came from operating on partial datastore knowledge.

---

## r1 ZFS pool inventory (snapshot 2026-05-20)

```
zpool: data
  topology:  raidz2 across 4 × 7.68 TB Samsung MZQL27T6HBLA-00A07
  raw:       27.7 TB
  usable:    13.85 TB
  used:      12.5 TB (93%)
  free:      813 GB
```

OpenZFS version: 2.2.2 (Ubuntu 24.04 default). raidz expansion
(grow-vdev) requires 2.3+ — not currently available without a
manual PPA + reboot.

### Per-dataset breakdown

| Dataset | Mount | Used | Role | Trim sensitivity |
|---|---|---|---|---|
| `data/archive` | `/srv/history-archive` | **6.95 TB** | Stellar history-archive (SDF format) | Mixed — see below |
| `data/minio` | `/var/lib/minio` | 4.96 TB | MinIO buckets (galexie-archive + galexie-live) | LCM tiering candidate (ADR-0027) |
| `data/postgres` | `/var/lib/postgresql` | 606 GB | TimescaleDB | Managed by ADR-0006 retention |
| `data/galexie` | `/var/lib/galexie` | 7.83 GB | Galexie captive-core working dir | NA |
| `data/os` | `/` | 645 KB | (rounding artefact) | NA |

---

## `/srv/history-archive` — full touchpoint map

> Last verified 2026-05-20 (Task #44 audit).

### Subdir-level inventory

| Subdir | Size | What it is |
|---|---|---|
| `bucket/` | **4.2 TB** | Stellar-core bucket files — historical state snapshots, content-addressed by SHA256. Used by stellar-core for catchup-mode and by stellar-archivist `scan` for state reconstruction at a checkpoint. |
| `transactions/` | **2.0 TB** | Per-checkpoint transaction XDR. Needed to replay history. |
| `results/` | 833 GB | Per-checkpoint transaction results. Companion to `transactions/`. |
| `scp/` | 74 GB | Per-checkpoint SCP consensus state. Used by stellar-core for SCP replay. |
| `ledger/` | 16 GB | Per-checkpoint `LedgerHeaderHistoryEntry`. **Required by ADR-0017 contracts 3+4.** |
| `history/` | 6.2 GB | Per-checkpoint manifest. **Required by ADR-0017 contracts 3+4.** |

### Active touchpoints (verified by code grep + journalctl)

| Touchpoint | Reads | Writes | Cadence |
|---|---|---|---|
| `archive-completeness.service` (`ratesengine-ops archive-completeness verify`) | `ledger/` + `history/` only | `ledger/` (fix mode pulls missing checkpoints from SDF mirrors) | Nightly timer + `fix` on detection |
| `verify-archive-tier-a.service` (`-tier chain`) | **NOTHING from /srv/history-archive** — only LCM chain in MinIO | n/a | Nightly timer (scheduled) |
| `verify-archive -tier checkpoint` (Tier B) | `ledger/` + `history/` | n/a | Operator-invoked only (no scheduled cron) |
| `verify-archive -tier archivist` (Tier E) | Full archive (all subdirs) | n/a | **Operator-invoked only; never run in 30d journal** |
| `ratesengine-ops` 5x subcommands w/ `-archive-root` flag | `ledger/` paths | n/a | Operator-invoked |

### Non-touchpoints (verified)

- `ratesengine-indexer`, `ratesengine-aggregator`, `ratesengine-api`: **none read /srv/history-archive**.
- Galexie's captive-core: uses its own ephemeral state in `/var/lib/galexie/captive*`, NOT this archive.
- Caddy/nginx: no `/archive` routes. Not externally exposed.
- ratesengine.toml's `history_archive_url`: points at SDF upstream (`https://history.stellar.org/prd/core-live/core_live_001`), NOT at this local path. The local mirror is a **cache**, not the canonical source.

### Maintenance flow

- `archive-completeness fix` is the ONLY active writer to /srv/history-archive today.
- It writes only to `ledger/XX/YY/ZZ/ledger-*.xdr.gz` (and indirectly `history/` for manifest entries).
- `bucket/`, `transactions/`, `results/`, `scp/` are **frozen since 2026-04-23** (original one-shot mirror by `stellar-archivist mirror`).
- No active process writes those four subdirs. They are static.

### Latest completeness report (2026-05-20T02:20:54Z)

```json
{
  "range": {"from": 2, "to": 62647853},
  "cross_anchor": {
    "expected": 978872,
    "found":    978872,
    "missing_count": 0
  }
}
```

ADR-0017 contracts 3+4 currently SATISFIED. Daemon is keeping `ledger/` current.

---

## ADR cross-reference

| ADR | Touches storage how |
|---|---|
| [ADR-0002](../adr/0002-self-hosted-s3-compat-storage.md) | Galexie writes to S3-compat (MinIO on r1); not local FS |
| [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) | Closed-bucket-only API contract = per-region storage shapes are invisible to clients |
| [ADR-0016](../adr/0016-per-region-storage-strategy.md) | R1 = full mirror (integrity leader); R2 = AWS-hybrid; R3 = Vultr-hybrid. R2/R3 explicitly DON'T mirror /srv/history-archive — they trust R1's Tier B + E verdict (note: Tier E is dormant on R1 too) |
| [ADR-0017](../adr/0017-archive-completeness-invariants.md) | Dual-archive completeness invariants: primary (MinIO LCMs) + cross-anchor (/srv/history-archive). Contracts 3+4 bind to `ledger/` checkpoint files |
| [ADR-0027](../adr/0027-lcm-cache-tiering.md) | LCM hot/cold tier: galexie-archive hot (MinIO) + aws-public-blockchain cold. §2 has `trim-galexie-archive` tool. §3 has TOML enable. §4 has operator-triggered bulk trim |
| [ADR-0011](../adr/0011-supply-snapshot.md) | supply_snapshot.timer + asset_supply_history table — postgres growth contributor |

---

## Trim trade-off register

> Each row is a "considered move + what it would cost" so the
> trade-offs are explicit when a decision is eventually made.

### Move A: Drop /srv/history-archive `bucket/` + `transactions/` + `results/` + `scp/`

**Reclaim:** ~7.1 TB → pool drops 93% → ~43%.

**Touchpoints affected:**

- `archive-completeness verify`: unaffected (reads only `ledger/` + `history/`).
- `verify-archive -tier chain`: unaffected (doesn't read /srv/history-archive at all).
- `verify-archive -tier checkpoint`: unaffected (reads only `ledger/` + `history/`).
- `verify-archive -tier archivist` (Tier E): **WOULD FAIL with local file:// URL**. Mitigation: pass `-archivist-url https://history.stellar.org/...` to scan against SDF directly. ~10-100× slower per run but functional. Tier E has never been run in 30d of journal history.
- ADR-0016 (R2/R3 trust R1's Tier B+E): R2/R3 not yet provisioned. Tier E being dormant on R1 means there's nothing for them to actually delegate to today.
- Disaster recovery: "rebuild /srv/history-archive on demand" per ADR-0016 §line 168. Estimated 4-10 h via `stellar-archivist mirror` from SDF. Empirical r1-original-bringup time was 3-4 h for 5-5.5 TB; 4-10 h for current 7 TB is honest.

**ADR impact:** ADR-0016 says R1 has "full SDF mirror (~7 TB)" as part of its "integrity leader" role. Trimming the 7 TB partially supersedes ADR-0016 — requires a new ADR or amendment. The trim doesn't violate ADR-0017 (contracts 3+4 still satisfied via `ledger/`).

**Reversibility:** ZFS snapshot before trim → 7-day window → destroy snapshot to commit. During the 7-day window, trim is fully reversible at zero cost.

**Cost in DR scenarios:**

| Scenario | Probability | Cost |
|---|---|---|
| Never need Tier E | High (never run in 30d) | Free |
| Tier E needed once for audit | Moderate | 1 slow run against SDF (~hours not ~minutes) |
| LCM bucket corrupted → need ledger-state reconstruction from bucket/ | Low | 4-10h rebuild before recovery work |
| SDF deprecates `history.stellar.org` during a future DR | Very low | Fall back to peer mirrors (LOBSTR/SatoshiPay/Blockdaemon/etc.); slower |

**Decision status:** OPEN. Operator-gated. Pending in [Task #7].

### Move B: Drop /srv/history-archive entirely (incl. `ledger/` + `history/`)

**Reclaim:** ~6.95 TB.

**Touchpoints affected:** Same as Move A PLUS:
- ADR-0017 contracts 3+4 **VIOLATED**. Cross-anchor verification permanently disabled.
- `archive-completeness verify` would fail on next run (expected 978k files, found 0).
- Lose Tier B (LCM-vs-SDF checkpoint hash verification) — silent corruption in LCMs becomes undetectable via this path.

**Mitigation if pursued:** retain only `ledger/` + `history/` (~22 GB) under a separate trim policy. But this is essentially Move A.

**Decision status:** REJECTED for now — ADR-0017's hard contracts.

### Move C: raidz2 → raidz1 conversion

**Reclaim:** ~7 TB (drop one drive's worth of parity from 4-drive vdev).

**Touchpoints affected:** Pool destroy + recreate required (ZFS topology is immutable). Multi-day operator rebuild.

**Blocker:** OpenZFS 2.2.2 (current) doesn't support raidz expansion. Would need 2.3+ upgrade first. Even with that, the 12.5 TB data doesn't fit on a single drive (7.68 TB) for the 1-drive-transit pattern → requires 2-drive transit (zero-parity window during migration).

**Decision status:** DEFERRED. Move A is cleaner if it gets done.

### Move D: ADR-0027 §3 + §4 (cold-tier enable + bulk LCM trim)

**Reclaim:** TBD by `--older-than-ledger` choice. Pre-Soroban LCMs (~3.5 TB of MinIO galexie-archive) is plausible.

**Touchpoints affected:** galexie-archive bucket reads fall back through TieredDataStore to aws-public-blockchain. Hot reads (above the trim cutoff) unchanged.

**Status:** Tool exists (`ratesengine-ops trim-galexie-archive`); §3 was prematurely enabled once and rolled back due to wrong-region cold endpoint config (see `feedback_cold_tier_premature_enable.md`). Needs to be done with §3+§4 together.

**Trade-off against operator stance:** Introduces a STEADY-STATE dependency on `aws-public-blockchain` for every read in the trimmed range. Operator preference: minimize external dependencies. Move A's external dependency (SDF for DR rebuild) is *contingent*; Move D's is *operational*.

### Move E: TimescaleDB trades retention (drop oldest raw chunks)

**Reclaim:** ~50-100 GB est. (trades hypertable is 588 GB total; the long tail at >2 years has lower row density per chunk after compression so the reclaim-per-month is small.)

**Touchpoints affected:** `/v1/observations` raw-trades queries for windows >2 yr return empty (CAGGs still serve the same window for downsampled views). Aggregator's historical VWAP-from-raw paths lose pre-trim history.

**Mechanism:** TimescaleDB `add_retention_policy` on the trades hypertable. Rolls in chunk-by-chunk over weeks; small steady cost.

**Decision status:** Lever available; modest reclaim. Mostly useful as a "preventive growth lid" once #38 lands and trades resumes its 1-2 GB/day growth. Not standalone enough to address the 93% pool.

### Move F: Re-enable trades job 1000 + tighter compression policy

**Reclaim:** ~50-150 GB est. (trades is already 1.42x compressed; tighter compression marginally improves it.)

**Touchpoints affected:** Job 1000 was disabled to stop decompress-on-write storms during heavy backfills (`feedback_reenable_trades_compression`). Re-enabling it after #38 finishes is the documented next-step regardless of any other move.

**Risk:** Slow CPU burn during re-compression cycle; no data risk.

**Decision status:** Will happen anyway when #38 finishes. Independent of the strategic trim question.

### Move G: Decode-then-trim — ship classic-supply observers + run them + then Move D

**Reclaim:** ~4 TB net (Move D's ~3.5 TB galexie-archive trim + a thinner /srv/history-archive trim).

**What it solves that Move A/D don't:** Capturing pre-Soroban classic-asset issuance history (mint/burn/clawback events for USDC, EURC, AQUA, etc. from ledger 0 → Soroban activation) into our own observer tables BEFORE deleting the source LCMs / history-archive. The granular-coverage mission says we want this regardless.

**Effort:** Weeks. Three sub-tasks: (1) ship classic-supply observers (LedgerEntryChangeDecoder + sep41-supply); (2) backfill them across pre-Soroban range (50M ledgers); (3) only then do the trim.

**Trade-off against operator stance:** Self-sufficient (no external dependency added); mission-aligned (we own the decoded history forever); but slow to land.

**Sequencing note:** Move A doesn't block Move G. A is the immediate, self-sufficient win on /srv/history-archive (which is a *mirror*, not our decoded output). G is the long-term self-sufficiency play on galexie-archive (which is our LCMs). They compose — not compete.

---

## Combined views

| Combo | Reclaim | Sequence | External dep added? | Notes |
|---|---|---|---|---|
| **A alone** | ~7.1 TB | One operation, ~minutes (snapshot) + 7 days observation | No (only contingent SDF DR) | Pool 93% → ~43%. Multi-year runway. Best operator-stance fit. |
| **A + F** | ~7.2-7.3 TB | A first, F after #38 ends | No | F happens regardless once backfills end. |
| **A + D** | ~10-10.5 TB | A first, D over weeks | **Yes — aws-public-blockchain for cold reads** | Skip unless A+F isn't enough. |
| **G alone** | ~4 TB | Weeks of observer work then D | No | Self-sufficient + mission-aligned. Slow. |
| **A + G** | ~11 TB | A this week, G over months | No | Best long-term composition. |
| **C alone** | ~7 TB | Multi-day operator downtime | No | Equivalent reclaim to A without losing data, but higher operator risk during transit + OpenZFS 2.3 upgrade prereq. |

---

## Recommendation: Move A

**Move A is the dominant choice this week**, evaluated against the operator's stated constraints:

| Operator constraint | Move A | Move D | Move B | Move C |
|---|---|---|---|---|
| "Don't lose sync data — no decoder/backfill re-runs" | ✓ touches only the SDF mirror, never our decoded output | ✓ (LCMs only) | ✓ | ✓ |
| "Can't expand the server" | ✓ reclaims on existing disk | ✓ | ✓ | ✓ |
| "Don't like relying on anyone else" | ✓ external dep is *contingent* (SDF only if DR rebuild needed) | ✗ **steady-state** dep on aws-public-blockchain | n/a | ✓ |
| "Honesty is very important" | ✓ ADR-0016 explicitly documents the rebuild-from-SDF fallback | ✓ ADR-0027 documents it | ✗ violates ADR-0017 contracts 3+4 | ✓ |
| Reversibility window | ✓ 7 days (ZFS snapshot) | partial (re-mirror slow) | low | low (pool destroy) |
| Operational risk | low (rm + observe) | medium | high | medium-high |

**After Move A lands, the gated tasks (#5, #14, #30, #35) become safe to run.** Move F happens opportunistically when #38 finishes. Move G is the long-term self-sufficiency play that can proceed without urgency in parallel.

---

## Operational plan for Move A

```bash
# 1. Snapshot (instant, ~zero storage cost)
ssh root@r1 'zfs snapshot data/archive@pre-trim-2026-05-20'

# 2. Verify the daemon report is currently clean — sanity check before trim
ssh root@r1 'cat /var/lib/galexie/last-completeness-report.json'
# expected: missing_count: 0

# 3. Trim the four subdirs (sequential to keep load predictable)
ssh root@r1 'rm -rf /srv/history-archive/bucket'
ssh root@r1 'rm -rf /srv/history-archive/transactions'
ssh root@r1 'rm -rf /srv/history-archive/results'
ssh root@r1 'rm -rf /srv/history-archive/scp'

# 4. Verify space (note: snapshot still holds the bytes until destroyed,
#    so `zfs list` "REFER" drops but "USED" reflects snapshot retention)
ssh root@r1 'zpool list data; zfs list data/archive'

# 5. Wait for the nightly archive-completeness run (next ~04:19 UTC)
#    — must still report missing_count: 0
ssh root@r1 'journalctl -u archive-completeness.service --since "2 hours ago" | tail -50'
ssh root@r1 'cat /var/lib/galexie/last-completeness-report.json'

# 6. Observation window: 7 days. Monitor for any verify-archive failures,
#    archive-divergence alerts, or unexpected codepaths exercising
#    /srv/history-archive/{bucket,transactions,results,scp}.

# 7. Commit (free the space) after observation window passes:
ssh root@r1 'zfs destroy data/archive@pre-trim-2026-05-20'
ssh root@r1 'zpool list data'  # confirm pool % dropped
```

**Rollback (anytime during the 7-day window):**

```bash
ssh root@r1 'zfs rollback data/archive@pre-trim-2026-05-20'
```

This restores all four subdirs in seconds.

---

## Open questions / things still to verify

- [ ] Has Tier E ever been documented as a routine practice anywhere we haven't searched? (Searched 10 ops docs; only `archival-node-bringup.md` mentions it in the bring-up sequence, and even there Tier A+B are the success criteria.)
- [ ] What's the exact relationship between ADR-0016's "trust R1's Tier B + E verification" promise to R2/R3 and the operational reality that Tier E hasn't been run on R1 either? (Audit finding: R2/R3 are deferred and the "trust" relationship is theoretical.)
- [ ] Does `cmd/ratesengine-ops/trim_galexie_archive.go` cover `galexie-live` bucket too, or only `galexie-archive`? (Need to skim; relevant if we ever want to trim live bucket's older partitions.)
- [ ] Confirm MinIO du for `galexie-archive` vs `galexie-live` per-bucket breakdown (du is slow over 4.96 TB; still pending).

---

## Decisions made

| Date | Decision | ADR / commit | Rationale |
|---|---|---|---|
| 2026-05-20 | **Approved Move A** — trim /srv/history-archive/{bucket,transactions,results,scp} with ZFS-snapshot safety net + 7-day observation window | This doc + execution commit | Reclaims ~7.1 TB without losing decoded data, without adding steady-state external dependency, with full reversibility for 7 days. Best fit for operator's "no external dependency / no lost sync data / can't expand server" constraints. ADR amendment for ADR-0016 to follow once trim observation window passes. |

---

## Change log

| Date | Author | What |
|---|---|---|
| 2026-05-20 | Task #44 audit | Initial inventory + trade-off register |
| 2026-05-20 | Expanded options A-G + combined views + recommendation + operational plan | Added Moves E (TimescaleDB retention), F (compression re-enable), G (decode-then-trim) for completeness. Recorded Move A as the approved decision. |
