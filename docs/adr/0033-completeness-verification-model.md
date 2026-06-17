---
adr: 0033
title: Completeness verification — substrate continuity, recognition, projection reconciliation
status: Accepted
date: 2026-06-02
supersedes: []
superseded_by: null
---

# ADR-0033: Completeness verification — substrate continuity, recognition, projection reconciliation

> **Reality note (2026-06-12, F-1354 / D2-04).** Where this ADR
> describes `hashdb` as a **feeder** of `ledger_ingest_log`, note that
> `internal/hashdb` is currently an **unwired library** — it has zero
> production callers and feeds nothing today. The substrate-continuity
> verdict is presently sourced from `archivecompleteness` +
> `verify-archive` + the ClickHouse substrate; the `hashdb` integration
> is aspirational. The decision below is preserved as the original
> record.

## Context

We want **100% confidence that we have 100% coverage** of every
protocol we index, with no shortcuts: a signal that is data-derived,
auditable, deterministic, and that localizes any gap to an exact
ledger / protocol / event rather than asserting a percentage.

ADR-0030 (per-source coverage invariant) and ADR-0031 (data-derived
coverage signal) moved us off cursor-derived coverage — a real
improvement, because an F-0020-class cascade can no longer hide
behind an advancing cursor. But the **confidence** the current
signal can offer is still bounded by three heuristics, each of
which can report 100% over a real gap:

1. **`density_pct` and `gap_free_pct` measure liveness, not
   completeness.** `density_pct = COUNT(DISTINCT ledger) / expected`
   answers "what fraction of ledgers have ≥1 row"; `gap_free_pct =
   1 - max_gap/expected` answers "is the largest hole smaller than a
   threshold." Neither can distinguish a ledger where we captured
   **all** of its events from one where we captured **one of eight**.
   A partial-decode of a busy ledger is invisible to both.

2. **`MinGapSizeOverride` silences gaps below a guessed sparsity
   envelope.** `internal/storage/timescale/per_source_gaps.go` raises
   the reportable-gap threshold per source to 50k–1,000,000 ledgers
   so natural quiet periods don't page. This exists *only* because
   the system has no oracle for "should there have been an event
   here," so it guesses. A decode halt that lands under the threshold
   reads as 100%. This is the single largest source of false
   confidence in the system today.

3. **There is no authoritative answer to "did we process every
   ledger?"** The substrate guarantee is fragmented across `hashdb`
   (drift only, not gaps), `archivecompleteness` (checkpoint *file*
   existence, not interior continuity), `verify-archive` (offline;
   verifies archive *files*, not that ingest consumed them), and the
   `ledgerstream` cursor — which advances **before** data is
   persisted (`cmd/stellarindex-indexer` `processAndPersistCursor`),
   so a panic mid-write loses a ledger with the cursor already past
   it.

The root epistemic problem is that **absence of data is being
interpreted as absence of events, with no independent oracle to back
the interpretation.** A quiet period and a missed capture look
identical to a row-count scan.

### A silent-loss bug this investigation surfaced

`soroban_events` (ADR-0029) is the natural independent oracle —
it is populated by `Dispatcher.dispatchOne` **before** any per-source
decoder runs (`internal/dispatcher/dispatcher.go:817-825`), so it
captures events regardless of decoder opinion. But the capture path
hardcodes `EventIndex: 0` (`internal/sources/sorobanevents/events.go:197`)
because `events.Event` carries no event index. The table PK is
`(ledger_close_time, ledger, tx_hash, op_index, event_index)`, and
the writer uses `ON CONFLICT DO NOTHING`
(`internal/storage/timescale/soroban_events.go:104`).

Therefore **any operation that emits ≥2 contract events collapses to
a single row in `soroban_events`; the rest are silently dropped.**
The worst case is Phoenix, which emits 8 events in one operation per
swap (`internal/sources/phoenix/decode.go:89-99` groups by
`{Ledger, TxHash, OpIndex}` — all 8 share one `op_index`). Today the
raw landing zone keeps 1 of those 8. The live decoder path is
unaffected (it sees all 8 via per-event `dispatchOne` calls), but the
*archive we would reconcile against* is lossy precisely where
multi-event reconstruction matters. No oracle can be built on a lossy
oracle, so fixing this is the precondition for everything below.

### What "100% confidence" actually requires

Coverage is not one number. It decomposes into three claims, each
provable by an oracle independent of the decoder being checked:

| Claim | Question | Independent oracle |
|---|---|---|
| **Substrate** | Did we process every ledger? | The hash-linked ledger chain |
| **Recognition** | Do we understand every event the contracts emit? | Distinct topics present in `soroban_events` |
| **Projection** | Did every event we captured become a row? | `soroban_events` rows ⟷ projected rows (by provenance) |

Substrate completeness is cheap and cryptographically self-evident
(hash linkage from a trusted anchor). Decode completeness is **not**
self-evident on Stellar — a ledger header commits to *which* events
exist (via the tx-set and meta), but not to "we parsed every Phoenix
swap." The state of the art (The Graph's Proof-of-Indexing;
Dune's raw→decoded layering; CDC count-reconciliation; EIP-7792's
log accumulators) closes that gap with **deterministic recomputation
plus raw-vs-decoded reconciliation**. Stellar's SCP finality means
we carry none of the reorg machinery those EVM systems need — closed
ledgers are immutable — so we spend the entire complexity budget on
decode completeness, which is where indexers actually lose data
silently.

## Decision

Adopt a **three-claim completeness model**. A source is `COMPLETE
through ledger W` if and only if all three claims hold contiguously
from the source's genesis to `W`. `W` is the source's **completeness
watermark**; honest coverage is `(W - genesis) / (tip - genesis)`.
A single failing ledger pins `W` and surfaces exactly what is
missing. No threshold, no cursor trust, no asserted 100%.

### Claim 1 — Substrate continuity (`ledger_ingest_log`)

A single authoritative per-ledger record, written **after** event
persistence completes (not before, as the cursor is today):

```
ledger_ingest_log(
    ledger_seq               bigint PRIMARY KEY,
    ledger_close_time        timestamptz,
    ledger_hash              bytea,   -- 32-byte LCM header hash
    prev_ledger_hash         bytea,   -- header's previousLedgerHash
    soroban_event_count      int,     -- contract events in this LCM
    classic_trade_effect_count int,   -- trade-producing effects in this LCM
    persisted_at             timestamptz
)
```

Two checks make the substrate self-evident:

- **Contiguity** — the set of `ledger_seq` is unbroken from
  `genesis` to `W`. A cheap anti-join over this *narrow* table, never
  an unbounded `trades` scan.
- **Hash-chain** — `prev_ledger_hash[N] == ledger_hash[N-1]` for all
  `N`, anchored every 64 ledgers to the SDF-signed checkpoint
  (the Tier-B logic `verify-archive` already implements). This turns
  "we have ledger N" from an assertion into the same cryptographic
  property a full node enjoys.

The two `*_count` columns are computed **at ingest, directly from the
`LedgerCloseMeta`, without decoding bodies** (this is the EIP-7792
pattern: the LCM is the committed source, the count is the checksum).
They are the denominators for Claims 2–3, and they prove
`soroban_events` is itself complete:
`COUNT(soroban_events WHERE ledger=N) == soroban_event_count[N]`.
Once a ledger is provably processed *and* its on-chain event count is
recorded, "zero rows for contract C here" becomes a **proven** quiet
period — which is what lets us delete the sparsity-threshold
guessing from the confidence path.

`hashdb`, `archivecompleteness`, and `verify-archive` are retained as
**feeders and verifiers** of `ledger_ingest_log`, not as parallel
sources of truth.

### Claim 2a — Recognition (topic-completeness)

For every source, the set of event topics emitted on-chain by its
contracts must be a subset of the topics its decoder claims:

```
DISTINCT (contract_id, topic_0_sym) in soroban_events
   for the source's contracts
        ⊆  source.HandledTopics()
```

Any topic present in the raw data that the decoder does not claim is
a **recognition gap** — surfaced as both a CI test (against a
committed fixture of seen `(contract, topic)` pairs) and a live
metric. This is the automated, on-chain-truth version of a
hand-maintained event-coverage inventory,
and it is what makes the "EVERY event for EVERY protocol" principle
*enforceable* rather than
aspirational. It is also the check that catches in-place contract
upgrades that add a topic
(`docs/architecture/contract-schema-evolution.md`): the moment a new
topic appears on-chain, recognition fails loudly instead of the event
being silently skipped.

### Claim 2b — Projection reconciliation (Soroban)

`soroban_events` and the projected protocol table must reconcile.
Two tiers, because a naive count fails for correlation sources
(Phoenix 8 events → 1 trade; Soroswap swap+sync → 1 trade; Redstone
1 event → N feed rows):

- **Continuous tripwire (cheap, every cycle).** Per source, define a
  **driver topic** — the one event that maps 1:1 to a logical record
  (Soroswap `swap`, Phoenix `("swap","amount_in")`, etc.). Then
  `COUNT(driver events in soroban_events) == COUNT(projected rows)`
  per ledger, computed incrementally over `[W, tip]` from narrow
  per-ledger aggregates. A mismatch flags a suspect ledger.

- **Authoritative audit (exact, per-event).** **Provenance
  anti-join.** Every projected row carries
  `(ledger, tx_hash, op_index, event_index)` back to its
  `soroban_events` origin. Then:

  ```
  soroban_events (handled topics)  LEFT JOIN  projected rows ON provenance
    → any raw event with no projected descendant = a precise gap
  ```

  This needs no ratio arithmetic and localizes a miss to the exact
  event. Events that intentionally produce no row (e.g. Soroswap
  `sync`, which only updates reserve state) are declared as
  no-output in the source registration so they don't read as false
  gaps.

### Claim 2b (SDEX / classic) — different oracle, same shape

SDEX predates Soroban (it runs from ledger 2; the Soroban era starts
~50.4M), so `soroban_events` is the wrong oracle for it. The raw
oracle for SDEX is the **classic trade-producing effects in the
LCM** — `ManageBuy/SellOffer` crossings, path-payment trades, and
post-P23 unified trade effects — also countable decoder-independently
at ingest. That count is the `classic_trade_effect_count[ledger]`
column from Claim 1. Reconciliation:

```
classic_trade_effect_count[N] == COUNT(trades WHERE source='sdex' AND ledger=N)
```

As defense in depth, protocol totals are periodically cross-checked
against an external oracle (Hubble `history_trades`), windowed to
match. Full per-event
anti-join parity for SDEX would require materializing a classic-trade
raw census; given pre-2024 SDEX volume, count-reconciliation plus the
Hubble anchor is the chosen cost/confidence trade. We may add the
raw census later if a discrepancy demands it.

### External (CEX / FX) — a different completeness class

Off-chain sources have **no on-chain substrate**. There is no ledger
to reconcile against; their data is ephemeral vendor stream data.
Their honest signal is **freshness / liveness** (`last_event_ts` vs
now, connection uptime), and it must be reported as a **distinct
class**, never folded into the on-chain completeness number. Doing
otherwise is dishonest by construction.

### The headline signal

`/v1/diagnostics/ingestion` and the status page surface, per source,
the **completeness watermark `W`** and `coverage = (W-genesis)/(tip-genesis)`,
plus the localized reason `W` is where it is (substrate gap at ledger
X / recognition gap on topic T / projection gap at ledger Y). This is
deterministic and re-runnable — anyone can recompute it from
`soroban_events` + `ledger_ingest_log` and get the same answer (a
Proof-of-Indexing analogue). R1/R2/R3 can additionally compute a
per-checkpoint digest of each protocol table and compare cross-region
to catch region-local corruption.

### What the old heuristics become

- **`gap_free_pct` / `MinGapSizeOverride`** are demoted from the
  *confidence* path to the *alerting-cadence* path only. They may
  still drive "page me if a dense source goes quiet for longer than
  X" tripwires, but they no longer contribute to the completeness
  verdict. Because Claim 1 proves quiet-vs-gap, the thresholds are no
  longer load-bearing for correctness.
- **`density_pct`** is retained as an informational descriptive
  number (how active is this protocol), explicitly *not* a coverage
  claim.

## Consequences

### Positive

- A partial-decode of a busy ledger — invisible today — becomes a
  hard, localized failure (Claim 2b).
- The sparsity-threshold guessing is removed from the confidence
  signal; "quiet" is proven, not assumed (Claim 1's LCM census).
- Recognition is verified against on-chain reality continuously
  (CI + metric), so a contract upgrade that adds an event can no
  longer be silently dropped (Claim 2a).
- The substrate guarantee is unified, durable, post-persist, and
  hash-verified — closing the cursor-advances-before-write hole.
- The verdict is deterministic and auditable; cross-region digest
  comparison adds an independent confirmation.
- Fixes the `soroban_events` multi-event silent-loss bug as a
  prerequisite (Phase 1), which also makes every future
  decoder-backfill-from-`soroban_events` complete.

### Negative

- `ledger_ingest_log` is one narrow row per ledger (~62M rows,
  ~5–6 GB). Acceptable against the 13.85 TB pool; narrow and
  indexed, and reconciliation runs incrementally over `[W, tip]`,
  never re-scanning history.
- Projected tables need `(ledger, tx_hash, op_index, event_index)`
  provenance columns where they lack them; a migration + projector
  change per source.
- Historical `soroban_events` rows captured before Phase 1 are
  missing the collided multi-event rows. The Claim-2b reconciliation
  will *surface* these (LCM census > soroban_events count for
  multi-event-op ledgers), and the affected ranges must be
  re-backfilled with the fixed capture. This is correct behavior —
  the system telling us where it lost data — but it is real backfill
  work.

### Neutral

- ADR-0029 (`soroban_events`), ADR-0030 (per-source invariant),
  ADR-0031 (data-derived signal), ADR-0032 (tables as projections)
  all compose with this; 0033 is the verification layer on top of
  the projection architecture 0032 established. It does not formally
  supersede them — it demotes the threshold-as-confidence reading of
  0030/0031 to alerting-only.

## Alternatives considered

- **Keep tuning `MinGapSizeOverride` per source.** This is the
  current path and the source of the false confidence. Every tune is
  a guess about a sparsity envelope; it cannot distinguish a halt
  during a quiet period from genuine quiet. Rejected as the
  confidence mechanism (retained for alert cadence).
- **Trust the live decoder + a stronger cursor.** The decoder path
  is correct today, but it is unauditable after the fact and the
  cursor cannot prove decode completeness. Rejected: provides no
  independent oracle.
- **External reconciliation only (Hubble / Stellar Expert).** Useful
  as defense in depth (and used for SDEX), but external oracles have
  window-mismatch traps, rate limits, and their own completeness
  questions. Rejected as the primary signal; retained as a
  cross-check.
- **Cryptographic per-event accumulator (EIP-7792 style) committed
  on-chain.** Not available on Stellar; we approximate the benefit
  with the LCM-derived per-ledger event census + deterministic
  recomputation, which is checkable offline.

## Implementation phases

Each phase is independently shippable and strictly increases
confidence; merge-as-you-go.

1. **`event_index`** — thread a real per-op event index through
   `events.Event` → dispatcher → `Capture` / `Reconstruct`; add it to
   `StreamSorobanEvents` ordering. Fixes the multi-event silent-loss
   bug. *(this PR's follow-on)*
2. **`ledger_ingest_log`** — post-persist per-ledger record with LCM
   census counts + hash-chain columns; contiguity + checkpoint-anchor
   verification. Fold `hashdb` / `verify-archive` into feeding it.
3. **Recognition check** — `HandledTopics()` per source; CI test +
   live metric comparing against `DISTINCT (contract, topic)` in
   `soroban_events`; generate the coverage inventory.
4. **Projection reconciliation** — driver-topic count tripwire +
   provenance anti-join; add provenance columns where missing.
5. **SDEX / classic reconciliation** — `classic_trade_effect_count`
   vs `trades(source='sdex')`; Hubble anchor.
6. **Watermark verdict** — replace the `density_pct`/`gap_free_pct`
   headline with the completeness watermark; demote
   `MinGapSizeOverride` to alerting-only.

## Reference

- Silent-loss bug:
  - `internal/sources/sorobanevents/events.go:186-197` (`EventIndex: 0`)
  - `internal/dispatcher/dispatcher.go:517-533` (op-event enumeration, index discarded)
  - `internal/storage/timescale/soroban_events.go:104` (`ON CONFLICT DO NOTHING`)
  - `internal/sources/phoenix/decode.go:89-99` (8 events share one op_index)
- Current heuristics being demoted:
  - `internal/storage/timescale/per_source_gaps.go` (`MinGapSizeOverride`)
  - `internal/storage/timescale/source_coverage.go` (`density_pct` / `gap_free_pct`)
  - `migrations/0048_source_coverage_snapshots.up.sql`
- Substrate fragments to unify:
  - `internal/hashdb/`, `internal/archivecompleteness/`,
    `cmd/stellarindex-ops/verify_archive_*.go`
- Builds on: ADR-0029, ADR-0030, ADR-0031, ADR-0032.
