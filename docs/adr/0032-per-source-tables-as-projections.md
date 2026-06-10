---
adr: 0032
title: Per-source tables are projections of soroban_events
status: Accepted
date: 2026-05-29
supersedes: []
superseded_by: null
---

# ADR-0032: Per-source tables are projections of `soroban_events`

## Context

Every Soroban-event ingest path today has **two parallel writers**
running from a single `dispatcher.ProcessLedger` call:

1. **Raw landing** — `dispatcher.dispatchOne`
   (`internal/dispatcher/dispatcher.go:811`) calls
   `d.rawEventSink.PushEvent(ev)` at line 824 (ADR-0029). This
   eventually flushes via `AsyncSink`
   (`internal/sources/sorobanevents/dispatcher_adapter.go:165`) →
   `store.InsertSorobanEventsBatch`
   (`internal/storage/timescale/soroban_events.go:33`) into the
   `soroban_events` hypertable.
2. **Per-source decoded** — the same `dispatchOne` then runs the
   decoder loop (lines 826-837). Each matching decoder's
   `Decode(ev)` produces a `consumer.Event` which flows through
   `internal/pipeline/sink.go::handleOneEvent` (line 113) and
   gets routed to a `persist*` function (`persistSEP41TransferEvent`
   line 741, `persistBlendPositionEvent` line 405, etc.) — each
   of which calls `store.Insert*` on the relevant per-source
   hypertable.

These two writers are **NOT in a transaction together**. The raw
sink batches asynchronously (1000-row batches, 1s flush); the
per-source sink writes one row per event synchronously through
the pipeline. Either can succeed while the other fails, and the
system relies on `ON CONFLICT DO NOTHING` idempotency + periodic
gap-detection + manual backfill to converge them.

The same per-source decoders are **also invoked** from seven
dedicated backfill subcommands
(`cmd/ratesengine-ops/{blend,phoenix,comet_liquidity,cctp,rozo,soroswap_skim,sep41_transfers}_backfill.go`)
plus the rc.87 orchestrator (`cmd/ratesengine-ops/drain_cascade_window.go`).
Each of those subcommands does the same thing: stream from
`soroban_events`, call `dec.Decode(ev)`, write to the per-source
table with `ON CONFLICT DO NOTHING`.

So we have **two paths that invoke the same decoder against the
same input**:

- Live ingest: dispatcher → decoder → per-source table (parallel
  with raw write)
- Backfill: SQL stream from `soroban_events` → decoder →
  per-source table

The duplication isn't theoretical — it's where every drift
incident this session originated:

- **F-0020 cascade** — back-pressure halted the raw writer; per-source
  writers kept going past the same window via live data; later raw
  recovery led to per-source / raw disagreement until the manual
  cascade-window drain.
- **2026-05-29 03:00** — `drain-cascade-window` ran the seven
  per-source backfills (data is whole) but didn't update the
  `backfill` cursor; cursor-derived density read 97-99% while
  data was at 100%. Tonight's commit `66efa65a` patched that by
  having the orchestrator upsert a cursor row on success — a
  workaround that adds code rather than removing the duplication.
- **rc.87 cascade-window dry-run flood** — every event from one
  non-SEP-41-compliant contract (CDNJAFZ4...) emitting `approve`
  events with a U32 in the spender slot tripped the per-source
  decoder, which is the per-source pipeline's only signal of
  "we wrote this row." `soroban_events` had the raw event;
  per-source table did not. Same input, two writers, different
  outcomes.
- **CAP-67 ScAddress variants (M/B/L)** — the scval decoder fix
  (rc.88) restored per-source coverage, but during the broken
  window `soroban_events` still landed everything raw. Two
  separately-decided "did this event ingest" answers.

ADR-0029 added `soroban_events` as a sidecar — a recovery
landing zone that didn't replace per-source writes. The promise
in §"Backfill path" (ADR-0029:120-128) was:

> Future per-source decoder backfills become SQL queries

and that promise has been **partially kept** (the seven
`*-backfill` subcommands deliver it operationally). But the
**live write path still bypasses the landing zone**: live ingest
writes per-source tables in parallel with the raw write rather
than as a derivative of it. The structural promise has not been
fully kept.

## Decision

**`soroban_events` is the sole authoritative store for Soroban-event
data. Per-source tables become CACHED PROJECTIONS, written by a
single projector component that reads from `soroban_events`.**

### Single write responsibility (Soroban side)

- **Live ingest writes raw only.** `dispatcher.dispatchOne`
  forwards every event to `d.rawEventSink.PushEvent(ev)` (already
  happens, unchanged). The decoder-loop section at
  `dispatcher.go:826-837` no longer routes `consumer.Event` to
  the per-source sink. Decoders are still invoked for **discovery
  / observability metrics** (events_seen counter, decoder_errors,
  asset discovery hook) but their structured-event outputs are
  discarded — the projector will redo that work.
- **The projector is the sole per-source writer.** A new component
  in the indexer binary (likely `internal/projector/`) tails
  `soroban_events` (read-from-cursor pattern, same shape as
  `ledgerstream`) and replays each row through
  `<source>.Decoder.Decode(events.Event)` — same Go code,
  identical decoders, identical per-source `Insert*` calls.
- **The projector has its own cursor:** `source='projector'`,
  `sub_source=<protocol>`, `last_ledger` = highest
  `soroban_events.ledger` projected. Operators can wipe a
  per-source table and rewind this cursor to re-project from any
  point. Per-source tables are caches.

### Per-source table semantics

```
soroban_events (truth)
   │  contains every event we ever saw, raw XDR preserved
   ▼
projector (single Go component)
   │  reads soroban_events.ledger > projector_cursor[source].last
   │  invokes <source>.Decoder.Decode(events.Event)
   │  writes per-source table with ON CONFLICT DO NOTHING (still
   │  idempotent — projector restarts safely)
   ▼
per_source_table  (cached projection — TRUNCATE-and-replay safe)
```

The decoder code (`internal/sources/<protocol>/decode.go`) is
**unchanged** — it's still the protocol-specific intelligence.
What changes is who invokes it (one component, one path) and
when (after raw landing, asynchronously).

### Operational surface

`ratesengine-ops projector --source X --replay --from N --to M`
replaces all seven `*-backfill` subcommands AND the
`drain-cascade-window` orchestrator:

- "Backfill the cascade window for blend": `projector --source blend --replay --from 62642781 --to 62757524`
- "Rebuild aquarius_swaps from scratch": `TRUNCATE aquarius_swaps; projector --source aquarius --replay --from <genesis>`
- "Catch up after a projector outage": `projector` (no flags — just runs the catch-up loop)

The drain-cascade-window orchestrator is **deleted** in favour of
calling `projector --source <each> --replay --from N --to M` for
each source — or simpler, `projector --all-sources --replay --from N --to M`.

### Indexer binary structure

```
cmd/ratesengine-indexer/
  main.go              — wires dispatcher + raw sink + projector
internal/indexer/
  pipeline/            — dispatcher → raw-event sink (existing, simplified)
  projector/           — NEW: soroban_events → per-source projection
    projector.go       — main loop, cursor management
    source_registry.go — maps source name → Decoder + persist functions
  observability/       — events_seen / decode_errors metrics (existing)
```

`internal/pipeline/sink.go` — its raison d'être was "route consumer.Event
to per-source tables." After ADR-0032 it's either deleted entirely
or reduced to **trades-table-only** (which is non-Soroban and
out of this ADR's scope — see §"Out of scope" below).

### Schema-evolution survival

Pre-ADR-0032: contract upgrades a field shape → live decoder
errors → per-source row drops → coverage loss.

Post-ADR-0032: raw event lands in `soroban_events` regardless of
decoder behaviour. Projector decoder fails → projection lags →
operator updates decoder + replay. **Raw data is never lost.**
The recovery path is: fix decoder, `projector --source <X>
--replay --from <decoder-failure-window-start>`.

This is the same promise ADR-0029 made for backfills, now
extended to live ingest.

### Out of scope

- **`trades` hypertable** stays as the truth store for non-Soroban
  data: SDEX classic-DEX writes, CEX/FX external streams. These
  don't flow through `soroban_events` (they're not Soroban
  events). Their write paths remain unchanged.
- **`oracle_updates` hypertable** is partially in scope: Reflector
  / RedStone / Band writes are derived from soroban_events
  contract events, so they become projector targets. CoinGecko /
  Chainlink / ECB writes are external pollers (not Soroban) and
  stay direct.
- **Aggregator** (VWAP / TWAP) reads from `trades` and
  per-source tables. Unchanged — projector keeps the per-source
  tables fresh.

### Compatibility window

The migration is staged so no per-source table loses coverage:

1. **Phase 1** — ship projector component running in **parallel**
   with the existing per-source sink. Both write to per-source
   tables; ON CONFLICT DO NOTHING absorbs the duplicate writes.
   Operator measures projector keeps up via the new
   `projector_lag_ledgers{source}` gauge.
2. **Phase 2** — flip the dispatcher to skip per-source sink
   routing (decoder still runs for metrics + discovery). Projector
   is now the sole writer. Watch per-source row rates remain
   constant.
3. **Phase 3** — delete `pipeline/sink.go` per-source `persist*`
   functions, delete the seven `*-backfill` subcommands, delete
   `drain-cascade-window`, delete the rc.89 cursor-credit fix
   (`66efa65a`). Net delete. *(The subcommand + `drain-cascade-window`
   deletions landed in **Phase 5, rc.97** — they no longer exist.)*

## Consequences

### Positive

- **Drift is structurally impossible.** Per-source tables are
  derived from `soroban_events` by one component. There's no
  "did the second writer also succeed" question.
- **Recovery is routine.** Wiping a per-source table + replaying
  is a normal operation. No incident-grade orchestration.
- **Decoder code reuse is exact.** The Go decoder packages
  (`internal/sources/<protocol>/`) are invoked once, by one
  driver. No "live invocation" vs "backfill invocation"
  divergence.
- **Schema evolution survives.** Live decoder failure no longer
  loses ledger coverage; raw is durable.
- **Operator UX collapses.** Seven subcommands + one
  orchestrator + one cursor-credit hack become **one** `projector`
  command with `--replay` semantics.
- **LoC delete estimate:** -1500 to -2000 LoC across:
  - `cmd/ratesengine-ops/*_backfill.go` (7 files, ~150 LoC each = -1050)
  - `cmd/ratesengine-ops/drain_cascade_window.go` (-280)
  - `cmd/ratesengine-ops/drain_cascade_window_test.go` (-200)
  - `internal/pipeline/sink.go` per-source `persist*` (-600)
  - Plus delete the cursor-credit fix from commit `66efa65a`
    (-78 LoC including the writeDrainBackfillCursor function)
  - Add `internal/projector/` (~600 LoC) + tests (~400 LoC)
  - **Net: roughly -1700 LoC** before factoring in ADR-0031's deletes.

### Negative

- **Projector is a new always-on component.** Risk: the projector
  wedging would cause per-source tables to lag the raw store.
  Mitigation: projector lag is a first-class observed metric
  (`projector_lag_ledgers{source}`) with paging alert at sustained
  > 1000 ledgers (matches the gap-detector threshold).
- **Read-after-write latency increases** for per-source tables.
  Today: live decoder writes per-source within the same dispatcher
  call (~50ms after the ledger lands). Post-ADR-0032: projector
  loop polls + catches up. Worst case = projector cycle interval
  + scan time = ~5-30 seconds. **Probably acceptable** — none
  of the per-source tables back a sub-second-SLA query path. (The
  trades table, which DOES back the price endpoints, is out of
  scope.)
- **Big-bang migration risk.** Mitigated by the three-phase
  rollout above — parallel writes during Phase 1 absorb any
  projector bugs.

### Neutral

- `ingestion_cursors` adds a `source='projector'` namespace.
  Different from `ledgerstream` (which still drives live indexer
  resume) and `backfill` (operational journal). Three cursor
  sources, each with a clear lane.
- The 5-file source convention + EVERY-event policy + ADR-0030
  lint guard all compose unchanged. Decoders are still in
  `internal/sources/<protocol>/`; their `Decode()` function is
  just called from a different driver.
- Existing per-source table indexes are unchanged. Projector
  uses the same `Insert*` paths.

## Alternatives considered

- **Keep parallel writes; rely on idempotency.** Today's
  architecture. Drift bugs already cited. Rejected.
- **Materialised views.** Postgres MATERIALIZED VIEW with
  REFRESH ON COMMIT or periodic refresh. Doesn't work — the
  decoder is Go code with full XDR parsing, not SQL. Would
  require either rewriting decoders in PL/pgSQL (lunacy) or
  using a foreign-function bridge (operational nightmare).
- **Postgres LISTEN/NOTIFY for projector trigger.** Replace the
  poll-cursor pattern with NOTIFY on every raw insert. Tighter
  read-after-write but adds Postgres backpressure surface +
  complicates the projector's batching. Rejected — 5-30s
  acceptable latency is not worth that complexity.
- **Per-source table = view over soroban_events with XDR parse
  in SQL.** TimescaleDB doesn't have an XDR parser; we'd ship a
  custom C extension. Operational cost outweighs simplification.
  Rejected.
- **Project only on demand (lazy).** Read-time decode + cache.
  Latency is bad for the aggregator's VWAP path which scans
  thousands of trades per tick. Rejected.

## Reference

- **soroban_events landing zone:** ADR-0029
- **Coverage signal:** ADR-0031 (depends on this ADR's projector
  cursor for projection-lag metric)
- **Per-source coverage invariant:** ADR-0030 (still binding;
  projector targets ARE the gap-detector targets)
- **Current parallel write paths:**
  - `internal/dispatcher/dispatcher.go:811` `dispatchOne`
  - `internal/sources/sorobanevents/dispatcher_adapter.go:165` `AsyncSink.PushEvent`
  - `internal/pipeline/sink.go:113` `handleOneEvent` (to be deleted)
- **Former backfill subcommands** (deleted in Phase 5, rc.97):
  - `cmd/ratesengine-ops/{blend,cctp,comet_liquidity,phoenix,rozo,sep41_transfers,soroswap_skim}_backfill.go`
  - `cmd/ratesengine-ops/drain_cascade_window.go`
- **EVERY-event policy:** project memory `project_every_event_principle`
- **Cascade-detection pattern:** project memory `project_cascade_detection_pattern`
