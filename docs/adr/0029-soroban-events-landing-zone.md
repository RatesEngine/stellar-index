---
adr: 0029
title: soroban_events raw-event landing zone
status: Accepted
date: 2026-05-25
supersedes: []
superseded_by: [0034]
---

# ADR-0029: `soroban_events` raw-event landing zone

> ⚠️ **Superseded by ADR-0034 (2026-06-05).** This describes the Postgres `soroban_events` landing zone. The raw landing zone is now ClickHouse (the Tier-1 lake); the parallel live+backfill writer model is replaced by the single-writer projector (ADR-0032). Retained for historical context.

## Context

The Rates Engine is committed to **granular coverage** — every bit
of data for every major Stellar protocol (see project memory
`project_full_indexing_future_scope`). Each protocol's ingest sits
behind a per-source decoder under `internal/sources/<venue>/` that
matches by topic, decodes the SCVal body into a domain type, and
writes to a per-protocol hypertable (`trades`, `blend_auctions`,
`cctp_events`, `rozo_events`, `sep41_supply_events`, ...).

The economic model of that pipeline is great for steady-state
live ingest. It is **bad** for new-decoder backfills:

Every time a new protocol's decoder ships AFTER its launch on
mainnet, we have to re-walk the same 50M-ledger Soroban-era range
out of MinIO galexie storage to materialise its rows. The cost
list as of 2026-05-25:

- verify-archive (16h walk, 62.6M ledgers)
- RedStone backfill (3.9M ledgers ≈ 2.5h)
- CCTP backfill (12M ledgers ≈ 6h)
- Rozo backfill (12M ledgers ≈ 6h)
- Blend money-market decoder backfill (#25, ~12M ledgers ≈ 6h)
- Comet / Phoenix / Soroswap gap backfills (#26/27/28, more hours)
- every future protocol — re-read again.

The MinIO read is the bottleneck, and the only thing we're
extracting per re-walk is "what events happened in this range".
Once we have that, the decoder is a pure function: contract id →
match → SCVal parse → domain row.

A raw-event landing zone solves this. Materialise EVERY Soroban
contract event the dispatcher routes into one hypertable
(`soroban_events`); future per-source decoder backfills become

```sql
INSERT INTO <protocol_table> SELECT ... FROM soroban_events
 WHERE contract_id IN (...) AND topic_0_sym IN (...);
```

— milliseconds-to-minutes instead of hours-per-source.

## Decision

Land migration 0041 introducing the `soroban_events` hypertable
plus a catch-all dispatcher hook that writes every Soroban contract
event the dispatcher routes. **Additive** — existing per-source
decoders continue to write their domain-specific tables unchanged.
The two paths run in parallel under one `dispatcher.ProcessLedger`
call.

### Schema (summary; full DDL in `migrations/0041_create_soroban_events.up.sql`)

```sql
CREATE TABLE soroban_events (
  ledger              INT          NOT NULL,
  ledger_close_time   TIMESTAMPTZ  NOT NULL,
  tx_hash             BYTEA        NOT NULL,   -- 32-byte raw
  op_index            SMALLINT     NOT NULL,
  event_index         SMALLINT     NOT NULL,
  contract_id         TEXT         NOT NULL,   -- C-strkey
  contract_id_hex     BYTEA        NOT NULL,   -- 32-byte raw
  topic_count         SMALLINT     NOT NULL,
  topic_0_sym         TEXT,                    -- decoded Sym/Str
  topic_0_xdr         BYTEA        NOT NULL,
  topic_1_xdr         BYTEA,
  topic_2_xdr         BYTEA,
  topic_3_xdr         BYTEA,
  body_xdr            BYTEA        NOT NULL,
  op_args_xdr         BYTEA,                   -- ScVec XDR of InvokeContract args
  PRIMARY KEY (ledger, tx_hash, op_index, event_index)
);
```

Hypertable on `ledger_close_time` with daily chunks. Three indexes
covering the common backfill shapes: per-contract walk, per-symbol
cross-contract scan, and (contract_id, topic_0_sym) point lookup.
Compression after 7 days with `segmentby = contract_id`.

### Ingest wiring

The dispatcher (`internal/dispatcher`) gains a third side-effect
hook `RawEventSink` alongside the existing `DiscoverySink`:

```go
type RawEventSink interface {
    PushEvent(ev events.Event)
}
func (*Dispatcher) SetRawEventSink(sink RawEventSink)
```

`dispatchOne` fires both hooks BEFORE the per-source decoder pass,
mirroring the discovery pattern. Per-source decoders are
unchanged.

The sink implementation lives in
`internal/sources/sorobanevents/`:

- `events.go::Capture(events.Event) (Row, error)` — projection
  from event to row, with raw XDR preservation.
- `dispatcher_adapter.go::AsyncSink` — buffered drain + batched
  insert into `soroban_events` via
  `timescale.Store.InsertSorobanEventsBatch`.

Both the indexer (`cmd/ratesengine-indexer`) and the backfill
subcommand (`cmd/ratesengine-ops backfill`) wire one `AsyncSink`
per dispatcher.

### Backfill pseudo-source

`ratesengine-ops backfill -source soroban-events -from N -to M
-parallel K` populates `soroban_events` for historical ranges
without per-source decoding. The pseudo-source name is exclusive —
running it alongside other sources is refused, because the
catch-all sees every event regardless of per-source decoder
routing and so co-running would double the MinIO reads with no
extra rows captured.

The pseudo-source bypasses the `BackfillSafe` gate — the raw XDR
is stored as-is and the "Soroban DeFi contracts upgrade in place"
concern is downstream of any per-source decoder that later
interprets these rows.

### Encoding rules

- **Contract ID** is stored in both C-strkey form (text, what
  humans paste into SQL) and raw 32 bytes (efficient indexed
  joins).
- **Topics 0-3** are stored as raw XDR bytes (base64-decoded from
  the wire). `topic_0_sym` is the convenience-decoded Symbol or
  String value of topic[0] when it's of one of those types — NULL
  otherwise; downstream correctness uses `topic_0_xdr` not
  `topic_0_sym`.
- **Body** is the raw XDR bytes of the event body SCVal. Per
  ADR-0003 the body can carry i128/u128 amounts; storing the raw
  XDR preserves full precision.
- **op_args_xdr** is the XDR-marshalled `ScVec` of the
  originating InvokeContract op's arguments, NULL when the event
  didn't come from an InvokeContract op (system events, CAP-67
  classic-op transfer events, etc.). Powers decoders that need
  tx-args alongside the event body (RedStone's `feed_ids`, Band's
  `relay()` args, future event-less protocols).

## Consequences

### Positive

- **Future decoder backfills are SQL queries**. Once a per-source
  decoder ships and we want to backfill its rows for the
  Soroban-era history, we run an `INSERT ... SELECT FROM
  soroban_events WHERE ...` — milliseconds-to-minutes regardless
  of the per-source decoding complexity. No MinIO walk needed.
- **Decouples decoder readiness from history reach**. A protocol
  whose decoder is mid-audit, or whose schemas are still moving
  between Soroban contract upgrades, still gets its events
  captured raw — operators can manually inspect those rows while
  the decoder gets ready, and the day the decoder lands the
  backfill is a one-shot SQL transaction.
- **Idempotent and incremental**. `ON CONFLICT (ledger, tx_hash,
  op_index, event_index) DO NOTHING` makes the live capture +
  parallel backfill chunks + replay-on-restart all collapse to
  the same final state with no de-dup work needed.
- **Operator visibility**. The new sink exposes `WrittenCount /
  DroppedCount / SkippedCount` counters. Steady-state `DroppedCount`
  is zero — the sink applies back-pressure on a full buffer
  (PushEvent blocks until storage drains). Non-zero values only
  appear in the shutdown-race window and are logged as
  `WARN soroban-events: rows dropped at shutdown race`; investigate
  any drops outside an intentional kill.

### Negative

- **Storage cost**. 50M Soroban-era ledgers × ~5-20 events/ledger
  ≈ 250M-1B rows. At ~400 bytes compressed = 100-400 GB. The R1
  postgres pool is 13.85 TB, currently ~610 GB used; this is
  comfortable but the next-largest single table.
- **Ingest CPU adds ~5-20 inserts/ledger**. The batch writer
  amortises the per-row cost across 1000-row batches, but each
  ledger walk now does work proportional to the number of
  contract events in the ledger. Measured CPU impact is small (a
  few percent of dispatcher's total time) per local profiling;
  production confirmation comes after the rollout.
- **Back-pressure can stall the dispatcher**. When the sink's
  buffer is full PushEvent blocks (intentionally — see the
  cursor-coherence note below); under sustained Postgres write
  pressure the dispatcher slows down to match drain rate, which
  manifests as a slower ledger-walk throughput rather than as
  dropped rows. Operators should alert on ledger-walk velocity
  falling well below archive-tip drift, not just on drop counters.
- **Original buffer-full-drop design (superseded 2026-05-26).**
  The first implementation dropped rows on full buffer to keep the
  hot path non-blocking, and the backfill driver's per-ledger
  cursor advance was independent of sink throughput. The
  combination was unsafe: the 2026-05-26 fill walk dropped ~0.43%
  of rows across 8 chunks (~13M rows) and the cursor advanced past
  them, so `-resume` short-circuited and the dropped rows had no
  recovery path. The fix made PushEvent block on full buffer so
  the cursor cannot outrun durable writes.

### Alternatives considered

**A. Per-source-only (status quo)** — keep doing MinIO re-walks
per decoder. Rejected: doesn't scale, every protocol added pays
hours of re-walk wall time, and the granular-coverage mission
multiplies the cost monotonically.

**B. Block-level raw storage** — keep galexie's LedgerCloseMeta
output and re-parse on demand for backfills. Rejected: that's
exactly what galexie already does and we already do MinIO walks
against it; the bottleneck is parsing per-ledger XDR + filtering
to relevant events. A pre-parsed, indexed event landing zone is
the missing layer.

**C. Per-protocol "raw events" tables** — one
`<protocol>_raw_events` per known protocol. Rejected: the
catch-all is the whole point. We don't know which contract IDs
will matter in the future (e.g. CCTP launched on Stellar 2026-04;
Blend's WASM has been upgraded several times). A catch-all
captures the data we don't yet know we'll want.

## References

- Memo: `~/.claude/projects/-Users-ash-code-ratesengine/memory/project_soroban_events_landing_zone.md`
- Memo: `project_full_indexing_future_scope` — the granular-coverage mission.
- Migration: `migrations/0041_create_soroban_events.up.sql`
- Package: `internal/sources/sorobanevents/`
- Dispatcher hook: `internal/dispatcher/dispatcher.go::RawEventSink`
- Storage writer: `internal/storage/timescale/soroban_events.go`
- CLAUDE.md "Soroban DeFi contracts upgrade in place" — the
  per-WASM-version decoder concern future per-source backfills
  will need to satisfy when interpreting these raw rows.
- ADR-0003 — i128/u128 no-truncation. The raw XDR storage preserves
  i128 amounts; downstream decoders that read soroban_events
  observe the same invariant.
