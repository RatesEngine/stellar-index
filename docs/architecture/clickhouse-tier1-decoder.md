---
title: Phase 2 design — structural decoder (galexie → ClickHouse Tier-1)
last_verified: 2026-06-05
status: design
---

# Phase 2 — structural decoder (galexie → ClickHouse Tier-1)

**Status: design (gated — no full backfill until the two gates in §6 pass on
a sample).** Implements ADR-0034 Tier-1 ingest: decode every LCM into the
six `stellar.*` ClickHouse tables, *structurally* (no protocol semantics),
retaining raw XDR, completely + idempotently, for backfill + live.

## 1. Principle: reuse proven extraction, don't re-derive XDR

The codebase already walks an LCM into exactly the pieces we need. Phase 2
**retargets that extraction to a ClickHouse sink** — it does not re-derive
XDR parsing (that's where bugs hide). Sources of truth reused:

- **LCM walk:** `ingest.NewLedgerTransactionReaderFromLedgerCloseMeta(
  passphrase, lcm)` — the same reader `dispatcher.CensusLedger`
  (census.go) uses. Bounded historical walk via `internal/ledgerstream`
  (the census-backfill path); live via the indexer.
- **Per-ledger header + counts:** `dispatcher.CensusLedger` already yields
  the LCM header fields + the decoder-independent event /
  classic-trade-effect counts → the `ledgers` row (and the gate oracle).
- **contract_events:** `sorobanevents.Capture(events.Event)` already
  produces ContractID(+hex), TopicCount, Topic0Sym, Topic0-3XDR, BodyXDR,
  OpArgsXDR, EventIndex — a 1:1 map to the `contract_events` columns.

## 2. Field mapping (LCM → ClickHouse), per table

Each column maps to a concrete, existing accessor — checkable, not guessed:

| CH table | source (per LCM/tx/op/event/change) |
|---|---|
| `ledgers` | LCM header (`ledger_seq, close_time, ledger_hash, prev_hash, protocol_version, bucket_list_hash, total_coins, fee_pool, base_fee, base_reserve`) + `CensusLedger` (`tx_count, op_count, soroban_event_count, classic_trade_effect_count`) |
| `transactions` | `ingest.LedgerTransaction`: `Hash`, `Envelope.SourceAccount()`, `Result.Result.FeeCharged`, `Envelope` max-fee, op-count, `Result.Successful()`, result code, `Envelope.Memo()` (type+value) |
| `operations` | each `Envelope.Operations()[i]`: `op_type` = `Body.Type`, `source_account` = op source ?? tx source, `body_xdr` = base64(`op.MarshalBinary()`) |
| `operation_results` | each `Result.Result.Results()[i]`: `result_code`, `result_xdr` = base64(marshal) |
| `contract_events` | `tx.GetTransactionEvents()` → `sorobanevents.Capture` fields (+ `op_index`, `in_successful_call`) |
| `ledger_entry_changes` | `tx.GetChanges()` (ingest) → `change_type`, `entry_type` (Data discriminant), `key_xdr`, `entry_xdr`; fee-meta changes carry `op_index = -1` |

Open check before coding: confirm the exact SDK accessor names against the
pinned `go-stellar-sdk` (the `ingest.LedgerTransaction` API) — done in the
implementation PR, not assumed here.

## 3. ClickHouse write path

- New `internal/storage/clickhouse` package using the official
  `github.com/ClickHouse/clickhouse-go/v2` driver (native protocol,
  `127.0.0.1:9300`). Add to go.mod.
- **Batched inserts** per table (native columnar batch; flush per N ledgers
  or M rows). One connection pool; async-insert optional later.
- Writes are append; ReplacingMergeTree handles dedup on merge.

## 4. Idempotency

ReplacingMergeTree(ingested_at) dedups on each table's ORDER BY key (the
row's true identity — verified in `deploy/clickhouse/tier1_schema.sql`). So
re-running any ledger range is safe: re-inserts collapse on merge; reads use
`FINAL` (or `GROUP BY`) until merges settle. No `ON CONFLICT` random-IO, no
silent drop.

## 5. Backfill + live

- **Backfill (build history):** `ratesengine-ops ch-backfill -from -to
  -bucket galexie-archive [-parallel N]` — bounded ledgerstream walk →
  structural extract → CH batch. Parallel by ledger range (CH ingests
  concurrent writers well; this is the step Postgres couldn't do).
- **Live (keep current):** the indexer fans out the same structural rows to
  CH alongside its existing work (dual-sink, ADR-0034) — wired AFTER
  backfill is proven. Phase 2 delivers the backfill path first.

## 6. Gates — both must pass on a 100k-ledger SAMPLE before any full walk

1. **Throughput + footprint gate.** Run `ch-backfill` over a 100k-ledger
   sample (a recent, event-dense range — worst case). Measure: ledgers/s and
   on-disk bytes/ledger (compressed). Project full-history time + size.
   PASS = full walk is hours-not-days and size fits the 12 TB pool with
   margin. (This is the empirical proof that columnar removes the wall — the
   premise the whole migration rests on. We do NOT commit to the full walk
   until this passes.)
2. **Completeness gate.** For every ledger in the sample:
   `count(contract_events) == CensusLedger.soroban_event_count`, and
   tx/op counts match the LCM. PASS = nothing dropped (the proof the
   Postgres collision class is gone). Use `FINAL`/dedup-aware counts.

Only after BOTH pass do we run Phase 3 (full historic backfill) and re-run
both gates over all history.

## 7. Risks + mitigations

- **SDK accessor drift** → confirm names against the pinned SDK in the impl
  PR; cover the extractor with golden-fixture tests (reuse existing LCM
  fixtures).
- **Throughput worse than hoped** → caught by gate 1 on the sample, before
  the big commit; if so, tune batch size / parallelism / async-insert, or
  re-evaluate — but cheaply, on 100k ledgers.
- **clickhouse-go version** → pin a stable v2; smoke-tested by the sample run.
- **Live dual-sink latency** → deferred to after backfill proof; the live
  pricing path is untouched until then.

## 8. Done definition for Phase 2 (#19)

CH structural sink + `ch-backfill` implemented, verify.sh green, and **both
§6 gates passed on the 100k sample with the numbers recorded here.** Full
historic backfill is Phase 3 (#20), gated on this.
