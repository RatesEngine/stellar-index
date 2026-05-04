---
adr: 0025
title: Rewritten-pair time-series storage (closing the fiat-target asymmetry)
status: Proposed
date: 2026-05-04
supersedes: []
superseded_by: null
---

# ADR-0025: Rewritten-pair time-series storage

## Context

Per CLAUDE.md and ADR-0014, **stablecoin → fiat substitution is
aggregator policy, not decoder policy**. The indexer stores trades
with their real on-chain quote (`native/USDC-GA5ZSEJYB37JRC…`,
`native/crypto:USDT`, …); the aggregator's
`internal/aggregate/orchestrator.fetchForTarget` rewrites those
trades onto a fiat-denominated target pair (`native/fiat:USD`) at
VWAP compute time. The rewriting is gated by
`[aggregate].enable_stablecoin_fiat_proxy` plus the operator's
`[trades].usd_pegged_classic_assets` allow-list.

That late-binding is correct and we want to keep it — it preserves
depeg signal in the raw trade feed (USDT trading at $0.968 during
a stress event IS news; folding USDT→USD at decode time would
hide it).

But the rewritten output today only lands in **one** place:

- **`vwap:<base>:<quote>:<window>`** Redis keys (current-bucket value,
  TTL = window).
- **`vwap:…:provenance`** marker for triangulation flag.
- A pub/sub event on `ratesengine:closed-bucket:v1` per write.

Every other time-series read path queries `prices_1m` (the
TimescaleDB continuous aggregate built from raw trades), which
**only knows literal trade pairs**. As a result:

| Endpoint | Literal pair (`?quote=USDC-GA5Z…`) | Rewritten target (`?quote=fiat:USD`) |
|---|---|---|
| `/v1/price` | works | **works** (PRs #631 / #632 — Redis fallback) |
| `/v1/price/tip` | works | **works** (PR #634 — Redis fallback) |
| `/v1/price/batch` | works | **works** (PR #634 — Redis fallback) |
| `/v1/vwap` | works | empty 404 |
| `/v1/twap` | works | empty 404 |
| `/v1/observations` | works | empty array |
| `/v1/history/since-inception` | works | empty points |
| `/v1/chart` | works | empty points |
| `/v1/changes/coin/<id>` | rewritten pair never lands → 404 | 404 |

The launch-day customer narrative ("Stellar → USD pricing as a
service") implies the fiat-target surfaces work end-to-end. The
asymmetry between `/v1/price`-family (which we fixed via Redis
fallback) and the longer-tail surfaces is a UX bug.

## Decision

**Mirror the aggregator's rewritten output to a new
TimescaleDB hypertable, `prices_1m_proxy`** (or similar), populated
incrementally by the orchestrator on every closed-bucket write.
API readers query both `prices_1m` (literal trade pairs, source of
truth for direct queries) and `prices_1m_proxy` (rewritten targets,
source of truth for stablecoin-fiat-proxy queries).

Rejected alternatives below.

## Schema sketch

```sql
CREATE TABLE prices_1m_proxy (
    bucket       timestamptz NOT NULL,
    base_asset   text        NOT NULL,
    quote_asset  text        NOT NULL,  -- fiat-form, e.g. "fiat:USD"
    vwap         numeric     NOT NULL,
    twap         numeric,
    volume       numeric     NOT NULL,
    volume_usd   numeric,
    trade_count  integer     NOT NULL,
    sources      text[]      NOT NULL,
    -- closed-bucket only; no first/last/high/low because the
    -- rewriter aggregates across multiple source pairs and the
    -- candle-shaped fields are ambiguous under aggregation
    PRIMARY KEY (bucket, base_asset, quote_asset)
);
SELECT create_hypertable('prices_1m_proxy', 'bucket',
    chunk_time_interval => INTERVAL '1 day');
SELECT add_retention_policy('prices_1m_proxy', INTERVAL '30 days');
```

Same retention as `prices_1m`. No CAGG dependency — the aggregator
writes rows directly at bucket close.

## Wiring

Two changes to `internal/aggregate/orchestrator/orchestrator.go`:

1. Extend `cacheClosedBucket` (the Redis writer) to ALSO upsert into
   `prices_1m_proxy` when `EnableStablecoinFiatProxy` is on AND the
   target's quote is fiat. Failure is logged + counted, not
   propagated — Redis remains the live-cache source of truth, the
   hypertable is the historical mirror.
2. Add a `prevVWAP` row read on tick start that consults
   `prices_1m_proxy` for the rewritten case (today the orchestrator
   reads `prices_1m` for the prev-bucket comparator; it would miss
   for rewritten pairs).

API-side, two changes:

1. New storage method
   `LatestClosedVWAP1mForPairProxy(ctx, pair) (Vwap1mRow, error)`
   that queries `prices_1m_proxy`. Existing
   `LatestClosedVWAP1mForPair` stays untouched — it remains the
   literal-pair source.
2. Add a fallback in `storePriceReader.LatestPrice`:
   prices_1m → prices_1m_proxy → Redis → trade. The Redis path
   stays as the freshness-bounded fallback for between-tick reads;
   the hypertable now anchors the historical reads.

Downstream surfaces (`/v1/vwap`, `/v1/twap`, `/v1/observations`,
`/v1/history/since-inception`, `/v1/chart`, change-summary worker)
get a similar two-table check via the same storage primitives.
Most are simple route-through additions.

## Why a separate table, not a CAGG

A continuous aggregate over `trades` could not produce rewritten
rows — CAGGs `GROUP BY` the literal columns; they don't know about
app-layer rewriting. We'd have to store the rewritten target in
the trades table itself (rejected — see "indexer-side rewriting"
below).

A second CAGG over a future `rewritten_trades` table would mean
materialising rewritten trade rows somewhere; same storage cost
either way, and a hypertable + direct insert is simpler than the
CAGG-over-CAGG-style topology.

## Why not indexer-side trade rewriting (option B)

Three reasons:

1. **Hides depegs.** The whole reason CLAUDE.md mandates
   aggregator-level rewriting is that USDT trading at $0.968 during
   a stress event is news. Indexer-level rewriting would store
   `XLM/fiat:USD = 0.968 × XLM/USDT` and lose the discrepancy.
2. **Doubles storage at the trade layer.** Every on-chain trade
   would now have a literal row AND a rewritten row in `trades`.
   Storage cost on r1 today would jump from 217 GB to 400+ GB for
   no reader-side benefit beyond what Option A delivers.
3. **Conflicts with the existing `[trades].usd_pegged_classic_assets`
   semantic.** That config tells the indexer "this classic credit
   counts as USD for `usd_volume` purposes" — a metadata signal,
   not a rewrite trigger. Conflating the two would make the config
   surface confusing.

## Why not service-level abstraction (option D)

Push the rewriting logic into each affected endpoint (the API would
union queries for the literal proxy pairs and aggregate them at
read time):

- Multiplies the rewriting logic across N handlers — each must know
  the operator's `usd_pegged_classic_assets` list and reproduce the
  aggregator's volume-weighted merge.
- Read latency would grow with the number of source pairs (one
  query per pair per request).
- The aggregator already does this work once per tick; doing it
  again at every read is wasteful.

## Why not status-quo (option C)

Defer the fix and document. Customers querying
`/v1/vwap?base=native&quote=fiat:USD` would continue to get 404; the
launch-day narrative would carry an asterisk ("/v1/price serves
USD; for VWAP/TWAP/history of XLM-USD, query the literal `quote=
USDC-GA5Z…` pair"). Two reasons against:

- The asymmetry is a teaching debt — every customer has to learn
  the literal-vs-fiat distinction before their first useful chart.
- The change-summary worker (`/v1/changes/coin/<id>` data) silently
  produces zero rows for the configured `(crypto:XLM/fiat:USD,
  native/fiat:USD)` entities, so the showcase's delta-strip surface
  also stays empty until this is fixed.

## Backfill

The hypertable starts empty after migration. The aggregator
populates it from now forward — closed buckets only. Historical
rewritten data (anything before the deploy) is unrecoverable
without re-running the rewriter; that's acceptable since:

- The `prices_1m` literal data is intact, so any future re-rollup
  has the source material.
- A one-shot backfill via `ratesengine-ops` is straightforward if
  a customer needs historical rewritten VWAPs.
- The 30-day retention boundary makes the unrecovered window
  small.

## Schema migration risk

Adding a new hypertable is low-risk: empty on creation, no foreign
keys against existing tables, no impact on writers until the
aggregator is deployed with the new code. Migration 0027 (TBD) ships
the table; aggregator deploy starts populating; API deploy starts
reading. Rollback at any stage is independent.

## Implementation phases

1. Migration `0027_create_prices_1m_proxy.up.sql` + `.down.sql`.
   Storage methods (`UpsertProxyVWAP`,
   `LatestClosedVWAP1mForPairProxy`, `TimedVWAPs1mForChangeSummaryProxy`).
   Tests at the storage boundary. **No behavior change yet** —
   table exists, nothing reads or writes it.
2. Aggregator wires the upsert in `cacheClosedBucket`. Deploy.
   Confirm rows accumulate.
3. API-side reader fallback (`storePriceReader`,
   change-summary worker, `/v1/vwap`, `/v1/twap`,
   `/v1/observations`, `/v1/history/since-inception`,
   `/v1/chart`). Deploy. Confirm fiat-target endpoints serve.

Three PRs, each independently mergeable. Phase 1 is the operator's
review point — it ratifies the schema; phases 2-3 are mechanical.

## Costs

- **Storage**: ~3 windows × N pairs × 1 row per 5 min = trivially
  small (< 1 GB / month at the launch pair set).
- **Write IO**: one extra UPSERT per (pair, window) per
  aggregator tick. The orchestrator already writes one Redis key +
  one provenance marker; one postgres UPSERT alongside is a low
  multiplier.
- **Read IO**: API readers union two tables; the rewritten table
  is small enough that the join is constant-time.

## Open questions for the operator

1. **Bucketed vs snapshot data model — important; flagged after
   PR #638 was opened, drafting #639 surfaced the gap.** The
   migration sketched in "Schema sketch" assumes a
   `prices_1m_proxy` storing **minute-aligned bucket VWAPs**
   keyed on `(bucket, base, quote)`, mirroring what `prices_1m`
   does for literal pairs. The aggregator orchestrator does NOT
   today produce minute-aligned bucket VWAPs for ANY pair — it
   produces rolling-window VWAPs at every tick (default
   `Interval=30s`), parameterised by lookback window
   (`Windows=[5m, 1h, 24h]`). Each tick writes one Redis key per
   (pair, window). Two ways to bridge:

   **Option A — minute-bucket computer (matches the schema in
   #639).** Add a new orchestrator path that runs at every minute
   boundary, fetches trades from `[T-1m, T)` for the rewritten
   pair (via the same `fetchForTarget` expansion), computes the
   minute-bucket VWAP, and UPSERTs into `prices_1m_proxy` with
   `bucket=T-1m`. Read shape exactly matches `prices_1m`'s.
   Cost: a new code path in the aggregator's tight loop;
   ~half-day of careful work to avoid drift between the rolling
   path and the bucketed path. Schema in #639 stands.

   **Option B — rolling-window snapshot table.** Replace
   `prices_1m_proxy` with `rewritten_vwap_snapshots`:

   ```sql
   CREATE TABLE rewritten_vwap_snapshots (
       observed_at    timestamptz NOT NULL,
       base_asset     text NOT NULL,
       quote_asset    text NOT NULL,
       window_seconds integer NOT NULL,  -- 300 / 3600 / 86400
       vwap           numeric NOT NULL,
       trade_count    integer NOT NULL,
       sources        text[] NOT NULL,
       PRIMARY KEY (base_asset, quote_asset, window_seconds, observed_at)
   );
   ```

   Mirror Redis: every aggregator tick that publishes a
   `vwap:base:quote:N` key ALSO inserts a row here. Append-only,
   bounded by retention. Phase 2 wiring is genuinely 1-line per
   write (alongside the existing Redis SET). API readers answer
   "current price" by reading the latest snapshot for the
   requested window; "history" by walking the time series for the
   chosen window; "/v1/changes" deltas by comparing snapshots at
   chosen lookback offsets.
   Cost: ~77 k rows/day at launch scale (9 pairs × 3 windows ×
   1 tick/30s × 86400 s/day); 30-day retention ≈ 2.3 M rows
   total, trivial.

   **Recommendation if asked:** Option B. The aggregator's actual
   output is rolling-window VWAPs; storing what it produces (vs
   forcing it into a bucket model) is simpler, cheaper, and
   lower-risk for launch. The bucketed model is strictly more
   "TimescaleDB-idiomatic" (CAGG-shaped) but requires a new
   computation path that doesn't exist today and risks drift
   against the existing rolling path.

   Pick A or B before merging #639. If B, the migration in #639
   needs the rewrite above; the storage methods change shape too
   (insert by `observed_at`, read by `(pair, window, observed_at
   DESC LIMIT 1)`).

2. **Naming.** Within Option A: `prices_1m_proxy` vs `_synth` vs
   `_rewritten`. Within Option B: `rewritten_vwap_snapshots` vs
   `proxy_vwap_snapshots` etc. No semantic difference within each
   model; pick one.
3. **Multi-window mirror.** Same pattern at 5m/1h/24h granularities,
   or just one window with downstream callers re-aggregating? The
   Redis side publishes all three; mirroring matches that. Mostly
   relevant for Option B (Option A would reuse the per-minute
   bucket and let read-side aggregation handle the windowing).
4. **Phase ordering vs the launch.** The phases are independently
   safe. The operator can defer phases 2-3 to post-launch if launch
   is gated on the SLA-proof artefact (L77) and the headline
   `/v1/price` already works (it does, via the Redis fallback). The
   "launch with caveat" path is viable; this ADR documents the
   cleanup target either way.

## Status

**Proposed** — awaiting operator decision on the four open questions
above. Implementation: PR #639 ships a phase-1 migration + storage
methods consistent with **Option A** (the original "Schema sketch"
section). If the operator picks **Option B** instead, #639 needs
the migration rewrite shown above; the storage method bodies change
shape but the call-site interfaces stay similar.
