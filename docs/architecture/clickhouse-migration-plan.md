---
title: Tiered data architecture — galexie → ClickHouse → decoders → Postgres
last_verified: 2026-06-05
status: proposed
---

# Migration plan: galexie → ClickHouse → protocol decoders → Postgres

**Status: proposed.** A comprehensive, phased migration to the storage
architecture that supports BOTH the pricing proposal AND a full,
searchable Stellar/Soroban explorer — without the bulk-reprocessing wall
we hit with `soroban_events` and `trades` in Postgres.

## 1. Context — why we're changing storage class

Grounded in the real numbers on r1 (2026-06-05):

| table | size | rows |
|---|---|---|
| `trades` | 324 GB | ~2.89 B |
| `soroban_events` | 210 GB | ~3.25 B |
| everything else | MB–GB | thousands–tens of millions |
| ZFS pool | 9.6 TB used / **18 TB free** | (disk is not the constraint) |

We are storing an OLAP-scale append-only blockchain firehose (billions of
rows) in a row-oriented OLTP store (Postgres/TimescaleDB) with per-row
unique PK indexes and `ON CONFLICT` dedup. That is the wrong storage
*class* for this tier: at 3 B rows the unique index dwarfs RAM, so every
insert is random IO. Bulk reprocessing (recovering the `event_index`
collision loss, fixing the `op_index` collision, adding a new decoder)
runs at ~0.24 ledgers/s on an *idle* box — i.e. months — and is the wall
that has blocked the recovery. This is not a bug or a tuning problem; it
is physics of a giant B-tree.

The firehose belongs in **columnar, append-only** storage. The small,
served pricing/entity data belongs in Postgres, where it already works.

## 2. Decision + why I'm confident (alternatives stress-tested)

**Decision:** a four-tier architecture, data routed by access pattern:

```
Tier 0  ARCHIVE      galexie → MinIO/S3 (LCM/XDR, immutable)        ✅ have
Tier 1  RAW LAKE     ClickHouse (columnar; all ledgers/tx/ops/      ➕ build
                     events/state, all history) + Parquet on MinIO
Tier 2  SEARCH       ClickHouse (exact-id) + OpenSearch (fuzzy)      ➕ build
Tier 3  SEMANTIC     Postgres/Timescale (decoded protocol entities, ✅ have
                     pricing CAGGs — small, served, hot)
Tier 4  SERVE        Go API + Next.js explorer                       ✅ have/expand
```

Alternatives I considered and rejected:

- **Keep scaling Postgres (Citus / partition harder / drop indexes).**
  Row-store is the wrong class for a billions-of-rows analytical firehose;
  Citus adds distributed-Postgres ops burden; dropping unique indexes
  loses dedup. The entire blockchain-indexing industry moved off Postgres
  for this tier — strong signal. **Rejected.**
- **Pure Parquet-on-MinIO + DuckDB/Trino.** Great for batch analytics,
  weak for a *serving* explorer (DuckDB is single-process; Trino is a
  cluster to operate; Parquet-over-S3 latency is high for entity-page
  lookups). **Rejected as the primary engine** — but kept: ClickHouse
  reads/writes Parquet on S3, so Parquet is our cold/interchange format.
- **Managed cloud warehouse (BigQuery/Snowflake/Databricks).** What Dune/
  Allium use, but conflicts with the self-hosted/colo ethos (ADR-0002,
  validators), adds cost + egress + lock-in. ClickHouse is self-hostable
  OSS — same capability, our posture. **Rejected.**
- **Serve the explorer on-demand from galexie (decode-on-read).** Fine
  for single-ledger point lookups; impossible for search/aggregation
  (would scan TB of LCM per query). You need a materialized columnar
  index. **Rejected.**
- **Put pricing in ClickHouse too (one store).** ClickHouse can aggregate,
  but the pricing semantics (ADR-0015 closed-bucket serving, i128-as-
  NUMERIC, the outlier/triangulation/class-weight policy chain, the CAGG
  ladder, the Go aggregator) already work in Postgres/Timescale and are
  small. Rewriting them in ClickHouse buys nothing and risks the proposal.
  **Rejected** — keep pricing in Postgres.

**ClickHouse wins** because it is columnar (cheap bulk scan + load),
append-only with **merge-on-read dedup** (no `ON CONFLICT` random-IO, and
no silent-drop class of bug like the `event_index` collision), self-
hostable OSS (fits our posture), serving-grade latency for an explorer,
and proven at exactly this workload (Goldsky, Allium, modern Dune). I am
confident this is the best approach; the residual risks (§7) are
operational, not architectural.

## 3. The dataflow (the load-bearing insight)

```
              structural decode (ONE galexie walk, historic + live)
  galexie ───────────────────────────────────────────────► ClickHouse (Tier 1)
 (LCM/XDR)   ledgers, txs, ops, op_results, contract_events,    raw lake
             ledger_entry_changes  + retained topic/body/arg XDR
                                                                    │
                                          protocol/semantic decoders│ (read CH rows)
                                                                    ▼
                                                            Postgres (Tier 3)
                                                     trades/CAGGs, pools, oracle_updates…
```

- The galexie walk is **structural only** (extract shape + keep raw XDR
  blobs); it does NOT interpret protocols, so it's cheap and decoder-
  agnostic.
- **Protocol decoders read ClickHouse, not galexie.** Their *logic* is
  unchanged (the hard-won Soroswap/Phoenix/Blend/SDEX/oracle/bridge
  decoders); only their input *source* moves to ClickHouse rows.
- **One historic galexie walk** populates ClickHouse. Postgres is then
  derived *from ClickHouse* (a columnar scan + decode), never a second
  galexie walk.

**The payoff that fixes us permanently:** re-deriving Postgres becomes a
ClickHouse scan, not a galexie walk. A decoder bug fix or a new protocol
decoder = re-run against ClickHouse (hours) → repopulate Postgres. The
`event_index`/`op_index` collision recovery — infeasible today — becomes a
routine re-projection. And ClickHouse's append-only model means the
collision class can't silently drop rows in the first place.

## 4. Reused vs. new

**Reused (the expensive intellectual core):** galexie ingestion + Tier-0
archive (ADR-0001/0002/0016); **every protocol decoder** + the WASM
audits + event-shape discoveries; the pricing engine, CAGGs, canonical
types, i128 discipline; the API, client SDK, frontend; the ADR-0033
verification model (census = decoder-independent oracle).

**New:** ClickHouse (Tier 1) + the galexie→columnar structural decoder;
the decoder input-adapter that reads ClickHouse; the search layer;
explorer UI expansion + per-protocol pages.

## 5. Tier-1 schema sketch (the first concrete artifact)

ClickHouse, `MergeTree` family, `PARTITION BY intDiv(ledger_seq,
1000000)`, `ORDER BY` the query-natural key, storage policy = local NVMe
hot + S3(MinIO)/Parquet cold. Dedup via `ReplacingMergeTree(ingest_ts)`
or idempotent partition-replace on backfill.

- `ledgers(ledger_seq, close_time, hash, prev_hash, protocol_version,
  tx_count, op_count, soroban_event_count, classic_trade_effect_count …)`
  — also serves the ADR-0033 substrate/census role.
- `transactions(ledger_seq, close_time, tx_hash, tx_index, source_account,
  fee_charged, success, op_count, memo, memo_type, result_code)`
- `operations(ledger_seq, close_time, tx_hash, op_index, op_type,
  source_account, body_xdr)` — body_xdr lets any OpDecoder run from CH.
- `operation_results(ledger_seq, tx_hash, op_index, result_code,
  result_xdr)` — SDEX claim atoms, path-payment results.
- `contract_events(ledger_seq, close_time, tx_hash, op_index, event_index,
  contract_id, type, topics_xdr Array(String), data_xdr, op_args_xdr,
  in_successful_call)` — the `soroban_events` replacement, append-only.
- `ledger_entry_changes(ledger_seq, close_time, tx_hash, op_index,
  change_type, entry_type, key_xdr, entry_xdr)` — supply/state observers.
- `contract_code` / `contract_data` (WASM + state) — explorer/contract pages.

Retaining the raw XDR blobs (topics/body/args/entries) is what lets every
decoder class (event / op / contract-call / ledger-entry-change) run from
ClickHouse without ever re-touching galexie.

## 6. Phased migration (additive — nothing stops)

**Phase 0 — ADR + provisioning.** Ratify the tiered model (ADR-0034,
supersedes ADR-0029 soroban_events-in-Postgres; amends ADR-0032 projector
source + ADR-0033 Claim 3 oracle). Decide ClickHouse placement (r1 has 18
TB free but Postgres shares the box — evaluate a dedicated/sibling host vs
co-tenant with resource limits) + storage policy (NVMe hot + MinIO Parquet
cold).

**Phase 1 — Stand up ClickHouse + Tier-1 schema.** Deploy CH, configure
S3/MinIO disk + retention/TTL, create the structural tables (§5).

**Phase 2 — Structural decoder (galexie → CH).** New ingest path that
walks LCM and writes structural rows to CH. Reuse the existing
LCM-walking / census / soroban_events-extraction logic; retarget the sink
to CH (native protocol or Parquet batch). Two modes: historic backfill +
live fan-out.

**Phase 3 — Historic backfill of ClickHouse.** Walk galexie [genesis,
tip] → CH (columnar bulk load — fast, no unique-index). Verify with the
census (every ledger present; per-ledger counts match LCM). This is the
big load that was infeasible in Postgres and is feasible here.

**Phase 4 — Rebuild the Postgres tier from ClickHouse (clean slate, not a
repair).** Build the ClickHouse→decoder input-adapter and **re-derive the
entire Postgres semantic/pricing tier fresh from CH into new tables, then
cut over and drop the old ones.** We do NOT repair the existing lossy
Postgres tables in place — we discard them (every row is re-derivable from
CH ← galexie) and rebuild correct ones. This eliminates the
`event_index`/`op_index` collision damage in one move instead of chasing
it. **Right-size while rebuilding:** the API serves CAGGs + a bounded
recent raw window, NOT 2.89 B raw `trades` — the full raw history lives in
CH; Postgres holds only the served slice + retention. Live path = dual-
sink (decode each ledger once → CH structural + Postgres semantic) to keep
pricing latency as-is. See §10 for the full drop/rebuild list.

**Phase 5 — Completeness on the new model.** ADR-0033 reconciliation
(substrate continuity ✅ already proven via census; recognition; per-source
count-reconciliation of Postgres vs CH vs census) → per-source watermarks.
Now cheap (CH scans). Replaces the headline `gap_free%`.

**Phase 6 — Explorer + search.** Search (exact-id via CH; fuzzy labels via
OpenSearch/Meilisearch). Explorer API endpoints + Next.js entity pages
(ledger/tx/op/event/account/contract/asset) served from CH.

**Phase 7 — Per-protocol deep-dives.** Expand semantic views (pools, TVL,
volume, rates, positions, emissions) + protocol UI pages, decoded from CH.

**Phase 8 — Decommission (no orphans).** Once CH is the source and the
rebuilt Postgres tier has passed cutover validation, execute the full
removal in §10: drop the old/oversized tables (`soroban_events`,
old `trades`, `ledger_ingest_log`, superseded protocol tables), purge
orphan cursors, delete the superseded code paths + systemd units, revert
the oversize tuning, and mark the affected ADRs superseded/amended +
update CLAUDE.md. Reclaim disk; end the scale pain. Nothing dead left
behind.

## 7. Proposal vs. explorer — sequencing (don't block the proposal)

The **pricing proposal is a small-data problem and is largely met on the
current Postgres stack already**: forward ingest is fixed (no new loss),
CAGGs are maintained, and the historical multi-event gaps are concentrated
in AMM swaps that VWAP/OHLC over liquid pairs is robust to. **The cheap
materiality check** (do the historical gaps move any served pricing
number, vs the census?) likely confirms the proposal is met *now*, with
the watermark to prove it — independent of this migration.

So: **the proposal ships on the existing stack; the ClickHouse migration
is the explorer + evolvability foundation, built additively beside it.**
Phases 4–5 also retroactively fix pricing correctness, but the proposal is
not *blocked* on the full migration.

## 8. Risks & mitigations

- **New stateful service (CH) to run/back up/monitor.** Mitigate: single-
  node to start (viable at our scale), standard ops, Parquet-on-MinIO as
  durable cold copy + galexie as ultimate backstop.
- **Live latency.** Mitigate: dual-sink (Phase 4) keeps pricing as fast as
  today; CH is the re-derivation source, not in the hot pricing path.
- **Dedup semantics** (ReplacingMergeTree dedups on merge). Mitigate:
  query with `FINAL`/dedup-aware views, or idempotent partition-replace on
  backfill.
- **Decoder input-adapter** is real plumbing. Mitigate: logic is reused;
  only the source changes; cover with the existing golden fixtures.
- **Resource contention if co-located on r1.** Mitigate: resource limits
  or a sibling host; disk is ample (18 TB free).
- **Scope creep.** Mitigate: the proposal is decoupled (§7) so the
  multi-month explorer build never holds the proposal hostage.

## 9. ADR impact

- **Supersedes ADR-0029** (`soroban_events` raw landing zone in Postgres).
- **Amends ADR-0032** (projector reads from ClickHouse, not a Postgres
  landing zone; protocol tables are re-derivable from CH).
- **Amends ADR-0033 Claim 3** (reconcile Postgres vs ClickHouse + the LCM
  census; per-event provenance lives in CH).
- ADR-0001/0002 (no Horizon; self-hosted S3) unchanged and reinforced.

## 10. Decommissioning & historical-debt removal (no orphans)

**Principle:** every component this migration supersedes is removed in the
same phase that replaces it — no orphan code, data, schema, or infra left
behind. And because the Postgres tier is *rebuilt from ClickHouse* (§Phase
4), **all current Postgres protocol/event data is discarded and re-derived
clean** — we carry forward zero historical damage. Everything dropped is
re-derivable from ClickHouse ← galexie, so this is safe.

### 10a. Postgres data + schema to DROP (new down-migrations, Phase 4/8)
- `soroban_events` hypertable (210 GB / 3.25 B) → **DROP** — replaced by CH
  `contract_events`.
- `trades` (324 GB / 2.89 B) → **DROP + rebuild** as a right-sized served
  table (CAGGs + bounded recent window + retention); full raw history stays
  in CH. The old lossy/collided rows are discarded, not migrated.
- Every protocol table (`blend_*`, `soroswap_router_swaps`,
  `sep41_transfers`, `oracle_updates`, `*_skim`, cctp/rozo, supply tables,
  `price_source_contributions`, `divergence_observations`, …) → **DROP +
  rebuild fresh from CH** (clean, no collision damage).
- `ledger_ingest_log` → **DROP** — the CH `ledgers` table absorbs the
  substrate/census role (per-ledger counts + header hash chain).
- CAGGs (`prices_1m/15m/1h/4h/1d/1w/1mo`) → **rebuild** from the rebuilt
  `trades`/source data.
- `ingestion_cursors` → **purge** the orphan cursor rows (`census-backfill`,
  `soroban-events`, the deleted `*-backfill` subsources, `backfill-router`).
- `completeness_snapshots`, `source_coverage_snapshots`,
  `decoder_stats_5m` → re-point to CH/census or drop if redundant.
- Revert oversize tuning once `trades` is right-sized:
  `max_locks_per_transaction` (grew 64→4096 for trades chunks) → default.

### 10b. Code to REMOVE or REFACTOR (tracked, no orphans)
- **Remove:** `internal/sources/sorobanevents/*` (the Postgres landing-zone
  writer/reconstruct), `internal/storage/timescale/soroban_events.go` +
  `topic_samples.go`, the `backfill -source soroban-events` pseudo-source
  in `cmd/ratesengine-ops/backfill.go`.
- **Remove (this session's now-superseded recovery tooling):** the Postgres
  `ledger_ingest_log.go` + `census_backfill` (the census *logic* moves to
  the CH `ledgers` populate path), and the soroban_events-specific arms of
  the completeness/reconcile code. Keep the *concepts* (census, recognition,
  watermark) — re-implement them against CH.
- **Refactor:** `internal/projector/*` → read ClickHouse instead of a
  Postgres landing zone (or fold into the CH→decoder adapter). The
  gap-detector (`per_source_gaps.go`, `RunGapDetector` over soroban_events +
  per-source hypertables) → CH/census-based.
- **Audit for orphans:** the deleted `*-backfill` family references; retired
  web scaffolds (CLAUDE.md wave-57); the gap-detector `excludedFrom*`
  tables (`freeze_events`, `mev_events`, `api_usage_events` — confirm live
  vs orphan); any `soroban_events`-specific config/docs.

### 10c. Infra/config to REMOVE
- Failed transient systemd units (`backfill-router.service`,
  `soroban-events-fill.service`) → cleaned.
- Any `soroban_events`/trades-chunk-specific Postgres tuning no longer
  needed after the firehose leaves Postgres.

### 10d. Docs/ADRs to mark superseded (so dead decisions don't mislead)
- ADR-0029 → Superseded. ADR-0030/0031/0032 → Amended (coverage signal is
  CH/census-based; projector reads CH). ADR-0033 Claim 3 → Amended (oracle).
  Update `CLAUDE.md` invariants (#6 ingest path, #7 one-writer/landing-zone)
  + the "Things that will surprise you" entries that reference
  `soroban_events`-in-Postgres. Retire/repoint the recovery runbook
  (`docs/operations/adr-0033-data-recovery.md`) which assumed the Postgres
  re-backfill.

### 10e. The clean-cutover guarantee
Sequencing guarantees no dual-source confusion: CH is fully backfilled +
census-verified (Phase 3) **before** the Postgres rebuild (Phase 4); the
new Postgres tables are built + validated **beside** the old ones, then a
single cutover switches the API/serving over; only **after** the cutover
passes do the old tables/code/units get dropped (Phase 8). At every moment
there is exactly one authoritative path, and galexie remains the ultimate
backstop throughout.
