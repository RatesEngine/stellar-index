---
adr: 0031
title: Coverage signal is data-derived from authoritative stores
status: Accepted
date: 2026-05-29
supersedes: []
superseded_by: null
---

# ADR-0031: Coverage signal is data-derived from authoritative stores

> **Note (ADR-0033, 2026-06-02).** ADR-0033 introduced
> `completeness_snapshots` (substrate continuity + recognition +
> projection reconciliation) as the **authoritative** coverage
> verdict. The data-derived gap detector described in this ADR is
> now a **supporting** signal that feeds alerting, not the headline
> completeness figure.

## Context

Stellar Index currently answers the question **"is range R covered
for source S"** in four independent places:

1. **Cursor inventory** — `ingestion_cursors` rows under
   `source='backfill'` carry `(sub_source, first_ledger, last_ledger)`
   tuples that document what an operator's backfill walked.
   `/v1/diagnostics/ingestion`'s `backfill_coverage[].density_pct`
   reads from these via
   `internal/api/v1/diagnostics_ingestion.go::computeSourceCoverage`
   (line 1072) + `cursorCoverageInterval` (line 1043).
2. **Data-derived gap detector** — `RunGapDetector`
   (`internal/storage/timescale/gap_detector.go:74`) scans every
   target in `DefaultGapDetectorTargets` every 30 min and emits
   `stellarindex_ingest_gap_*` Prometheus gauges. The alert
   `stellarindex_ingest_gap_detected` paginates on `max_size_ledgers > 1000`
   (per-target overridable per ADR-0030).
3. **Live cursor span** — `source='ledgerstream'` cursor's
   `first_ledger` to `last_ledger` is credited as covered for every
   on-chain source in `computeSourceCoverage`, on the premise that
   live ingest's walker covered the range gap-free.
4. **Decoder stats counters** — `decoder_stats_5m` hypertable
   (migration 0020) records per-source `events_seen` / `decode_errors`
   in 5-min windows; the dashboard renders these as
   "what the decoder claimed."

These four signals are computed by independent paths against
independent stores. They **diverge regularly under real-world
conditions**:

- **F-0020 cascade (2026-05-26)** — Postgres back-pressure halted
  the `soroban_events` writer across 103,396 contiguous ledgers.
  The cursor inventory's view of coverage was **unaffected** (the
  live cursor's `last_ledger` advanced past the halt window once
  back-pressure cleared), and `computeSourceCoverage` returned
  100% the whole time. Only a manual data probe surfaced the gap;
  ADR-0030 was the response.
- **2026-05-29 (last night)** — the `drain-cascade-window`
  orchestrator drained per-source classifier tables (data is
  whole) for [62642781, 62757524] but did **not** write a
  `backfill` cursor row for the range. Cursor-derived density
  read 97-99% per source while the gap detector reported 0
  gap-ledgers across every per-source target. The customer-visible
  "all decoders < 100%" signal was cursor vs data disagreement,
  not a coverage gap.

Each incident has been "fix one signal to agree with another"
which doesn't reduce the number of signals — it tightens the
coupling between them. The current architecture has too many
sources of truth for the same fact; every fix makes them more
entangled rather than fewer.

ADR-0030 (per-source coverage invariant) anticipated this
explicitly:

> **Headline density MUST be data-derived, not cursor-derived.**
> The `/v1/diagnostics/ingestion` handler's per-source `density_pct`
> already came from cursor coverage; rc.85 cleaned up the
> NULL→genesis fallback but the underlying source is still
> cursors. The next step (separate PR, scoped for rc.88+) is to
> surface the data-derived gap gauge values in the same response,
> so consumers (status page, dashboards) read the honest signal
> directly.

This ADR is that next step.

## Decision

**One coverage signal per data domain, derived from the
authoritative store.** Specifically:

### Soroban-event sources (every protocol matching by topic)

```
covered(source)  = COUNT(DISTINCT ledger)
                   FROM soroban_events
                   WHERE topic_0_sym IN (source's claimed topic-0 symbols)
                     AND ledger BETWEEN sourceGenesisLedger(source) AND tip

expected(source) = tip - sourceGenesisLedger(source) + 1
density(source)  = covered / expected   (capped to [0, 1])
```

The source's "claimed topic-0 symbols" come from a new
`sources.Source.TopicSymbols()` interface method that every
decoder package implements (already implicit in their `classify()`
functions; ADR-0030's EVERY-event policy means the symbol set is
exhaustive). For sources that emit no events (Band — see CLAUDE.md
surprise list), coverage signal is instead derived from the
`oracle_updates` table filtered by `source = '<name>'`.

### Classic-DEX (SDEX)

```
covered("sdex") = COUNT(DISTINCT ledger)
                  FROM trades
                  WHERE source = 'sdex'
                    AND ledger BETWEEN 2 AND tip
```

This is the existing SDEX gap-detector target's query
(`per_source_gaps.go:122` — `WhereFilter: "source = 'sdex'"`).

### External (CEX, FX, oracle pollers)

These don't have Soroban-ledger genesis — they're off-chain
streams. Their "coverage" is **freshness**, not ledger density.
Surfaced as `last_event_ts` against `time.Now()` (already in
`/v1/diagnostics/ingestion::sources[]`); ADR-0031 does not change
the external-source signal.

### Two complementary numbers per source

The diagnostic + status page surfaces **two** numbers, not one:

- **`density_pct`** — `covered / expected`. Honest raw coverage.
  For dense sources (SDEX, Soroswap, Aquarius) this approaches
  1.0 when fully ingested. For sparse-by-design sources (Blend
  auctions, CCTP, Rozo) it's naturally low — the contract doesn't
  emit on every ledger and never will. Customers seeing 0.07% for
  Blend auctions are seeing the truth.
- **`gap_free_pct`** — `1 - max_gap_size / expected`. Captures
  "is there a stretch where we'd expect emission but see none."
  Tuned by the per-target sparsity threshold (ADR-0030 +
  `MinGapSizeOverride`). 1.0 means "no unexplained gap larger
  than the cadence threshold."

A dense source running cleanly: both ≈ 1.0. A sparse source
running cleanly: density low, gap_free_pct = 1.0. A cascade:
both drop. **Two numbers, both honest, both data-derived.**

### What the cursor inventory becomes

`ingestion_cursors` is preserved as the **operational journal**:
"what did the operator ask the system to do, and how far did it
get." It's no longer interpreted as a coverage signal. Specifically:

- `source='ledgerstream'` cursor — still drives the live indexer's
  resume point. **Operational state, not a coverage claim.**
- `source='backfill'` rows — still tracks per-chunk progress for
  `stellarindex-ops backfill`. **Operational journal.**
- The /v1/cursors handler still surfaces them (different purpose:
  "what's the system doing right now") but `/v1/diagnostics/ingestion`'s
  `backfill_coverage` becomes data-derived.

### Caching + cost

The data-derived query is `COUNT(DISTINCT ledger)` against
`soroban_events` (or `trades` for SDEX). r1 measured the LAG-over-DISTINCT
scan at ~5 min for 50M+ rows (`gap_detector.go:18`). The simpler
COUNT-DISTINCT is similar order. We **already pay this cost** —
the gap detector runs it every 30 min. ADR-0031's diagnostic
handler reads the **cached gauge values** the gap detector
already emits + a `count_distinct_ledger` gauge we add alongside
(`obs.IngestSourceDistinctLedgers{source, table}`). No new
queries per HTTP request.

### Single SQL helper, one read path

A new `internal/storage/timescale.SourceCoverage` struct:

```go
type SourceCoverage struct {
    Source           string
    DistinctLedgers  int64
    ExpectedLedgers  int64
    MaxGapLedgers    int64   // largest contiguous gap >= threshold
    GapCount         int64
    DensityPct       float64 // distinct / expected
    GapFreePct       float64 // 1 - max_gap / expected
    LastUpdated      time.Time
}

func (s *Store) GetSourceCoverage(ctx context.Context, source string) (SourceCoverage, error)
```

`/v1/diagnostics/ingestion` calls `GetSourceCoverage(source)` per
source instead of running `computeSourceCoverage`. Alerts can
PromQL on the same gauges. There is **one query, one struct, one
truth.**

## Consequences

### Positive

- A future F-0020-class cascade is **impossible to hide**. The
  coverage signal IS data state; there's no second-store-says-fine
  failure mode.
- Cursor-derived false positives like 2026-05-29's "decoders <100%
  because drain didn't write a cursor" stop happening. The drain
  writes data, the data-derived signal reflects it.
- The /v1/diagnostics/ingestion response gets simpler: one
  source-of-truth per number, fewer ways for the response to
  contradict itself.
- The cursor-credit fix shipped tonight (commit `66efa65a`,
  drain-cascade-window upserts a `backfill` cursor on success)
  becomes redundant and can be reverted. **Net code delete.**
- Two-numbers framing (density + gap_free) lets us be honest
  about sparse sources without misleading customers ("Blend
  auctions density 0.07%" is correct — the contract barely emits
  — but `gap_free_pct = 1.0` says "we have everything that was
  emitted").

### Negative

- Adding `count_distinct_ledger` to the gap detector's per-target
  scan adds one query per cycle per target. ~16 targets × 1 query
  × 30 min cycle = trivial.
- Sources whose Soroban-event topic claims are narrower than the
  reality (e.g. a decoder that ignores `set_admin` events but the
  contract emits them) will show artificially low density. The
  EVERY-event policy
  mitigates this — decoders MUST classify every topic the contract
  emits, even ones they route to a "noop" sink.
- One migration to seed historical `distinct_ledger` counts so
  the diagnostic doesn't show 0% during the first 30-min cycle
  after deploy. Manageable.

### Neutral

- Cursor inventory stays as-is structurally — just demoted in the
  coverage projection. No data is deleted.
- The 5-file source convention + EVERY-event policy + ADR-0030
  lint guard all compose with this cleanly.

## Alternatives considered

- **Patch the cursor inventory to always agree with data.** Wires
  the drain orchestrator + every backfill subcommand + live ingest
  to write coherent cursor rows. We did one step of this tonight
  (drain-cascade-window cursor-credit). Reduces drift but doesn't
  remove the duplication. Future bugs in this surface will keep
  surfacing the same class of incident.
- **Keep cursor-derived density, add gap-free as a second metric.**
  Surface both. Reduces customer confusion but keeps the two
  computation paths alive. Doesn't enable the LoC delete.
  Rejected: the goal is fewer sources of truth, not more
  cohabiting.
- **Real-time COUNT(DISTINCT) on every HTTP request.** Too slow
  on r1 (~5 min scan). Rejected.

## Reference

- Current cursor-derived implementation:
  - `internal/api/v1/diagnostics_ingestion.go:1043` `cursorCoverageInterval`
  - `internal/api/v1/diagnostics_ingestion.go:1072` `computeSourceCoverage`
  - `internal/api/v1/diagnostics_ingestion.go:743` `buildBackfillCoverage`
- Current data-derived computation (target list):
  - `internal/storage/timescale/per_source_gaps.go:59` `DefaultGapDetectorTargets`
- Migration to add: `migrations/0048_distinct_ledger_metrics.up.sql`
  (creates supporting indexes if any are missing; the
  `(contract_id, ledger DESC)` and `(topic_0_sym, ledger DESC)`
  indexes from ADR-0029 already cover most cases).
- ADRs touched: supersedes (in spirit, not formally) cursor-derived
  density established implicitly in pre-rc.85 code; complements
  ADR-0030 (per-source coverage invariant); referenced by ADR-0032
  (per-source tables as projections).
