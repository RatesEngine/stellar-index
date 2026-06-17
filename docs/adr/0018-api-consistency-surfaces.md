---
adr: 0018
title: API consistency surfaces — closed-bucket, tip, and observations
status: Accepted
date: 2026-04-28
supersedes: []
superseded_by: null
---

# ADR-0018: API consistency surfaces — closed-bucket, tip, and observations

## Context

[ADR-0015](0015-last-closed-bucket-rate-serving.md) commits the
`/v1/price` endpoint to closed-bucket-only semantics: the API never
returns an in-progress 1m bucket, only buckets whose end timestamp
has passed. That's what makes our cross-region rate consistency
property *provable* — any two regions that have ingested the same
ledgers will return byte-identical VWAP for the same `(pair,
window, from_ts)` triple.

That contract serves oracle clients (SEP-40, divergence monitors,
SLA-bound integrations) well. It does NOT serve clients who want
the most-current price they can get — wallet UIs, retail dashboards,
algorithmic clients tracking the tip. For those use cases the ~60–
120 s worst-case staleness on the closed-bucket surface is
unacceptable.

Two structural facts make a single endpoint with a query parameter
the wrong shape:

1. **Cross-region consistency is binary, not graded.** A response
   either carries the closed-bucket guarantee or it doesn't.
   Customers depending on the guarantee (oracle reads, divergence
   alarms) must not be able to consume a non-guaranteed response by
   accident — e.g. via a stray `?freshness=tip` left in some forgotten
   config.
2. **Wire shape differs.** A rolling-window tip price wants
   `window_seconds=5` and a small-window VWAP; a "last good price"
   fallback wants `price_type=last_trade` and a real per-trade
   timestamp; raw per-source observations want a *list* not a
   single record. One JSON shape can't cleanly cover all three.

So the surface needs URL-level separation, with each URL pinned to
one explicit consistency contract.

## Decision

**Three API surfaces, three URLs, three explicit consistency
contracts. Each surface's contract is the load-bearing wire promise;
mixing across surfaces requires deliberate URL choice.**

### Surface 1 — `/v1/price` (closed-bucket VWAP)

Existing endpoint. Contract per ADR-0015 holds unchanged:

- Aggregated VWAP from a CLOSED bucket only
- Default granularity: 1m (configurable via `?granularity=`)
- Cross-region consistency: **provable** — bytes match across all
  regions that have ingested the same ledgers
- Worst-case staleness: ~30–120 s (1m bucket); larger granularities
  proportionally more
- Use cases: oracle anchors, SLA reporting, divergence monitors,
  any consumer that depends on regional consistency
- `flags.stale = true` means: the closed-bucket VWAP wasn't
  available, the API degraded to a last-trade fallback
  (intentional ADR-0015 degradation envelope)

### Surface 2 — `/v1/price/tip` (rolling window + last-good-price)

New endpoint. Designed for "what's the price right now" use cases:

- VWAP over a rolling short window (default 5 s, tunable
  `?window_seconds=N` clamped 1–60 s)
- If no trades in the window: return the most-recent observed trade
  for the pair, with `price_type="last_trade"` and `window_seconds=0`
- No upper bound on the fallback's age — if the last trade was 6 weeks
  ago, return it. Customer derives freshness from `observed_at`.
- Cross-region consistency: **NOT provable** — rolling-window
  computation depends on each region's exact ingest timing
- Worst-case staleness when window has trades: ~5–10 s
- Use cases: ticker UIs, real-time dashboards, fast-poll clients
- `flags.stale = false` always — both branches (window-VWAP and
  last-good fallback) are *in-contract*, neither is degradation.
  Customer reads `price_type` + `observed_at` to know what they got.

### Surface 3 — `/v1/observations` (raw per-source)

New endpoint. The lowest-level, no-aggregation surface:

- Returns the most-recent trade per source for the pair, as an array
- `?source=X` filter narrows to one source
- `?aggregate=latest` collapses to the single most-recent trade across all sources
- No VWAP, no chaining, no smoothing — purely "what each venue last published"
- Cross-region consistency: **NOT provable** — different regions
  observe trades at slightly different moments
- Use cases: divergence detection, source comparison, build-your-own
  aggregation, debugging
- `flags.stale = false` always — no aggregation contract to fall
  short of

### `flags.stale` semantic, pinned across all surfaces

The flag now carries one consistent meaning: **"this response is
below the surface's documented baseline contract"**.

| Surface | Baseline contract | Below-baseline cases | Flag fires? |
|---|---|---|---|
| `/v1/price` | Closed-bucket VWAP | Closed bucket missing → last-trade fallback | **Yes** |
| `/v1/price/tip` | "VWAP if window has trades, else last good price" | Both branches in-contract | **No** |
| `/v1/observations` | Raw per-source trades | No aggregation to fall short of | **No** |

Customers gating on `flags.stale = true` for "should I worry / retry"
behaviour get the right signal automatically across all surfaces.

### Forex factor handling for chained rates

For rates that chain via USD pivot (`XLM/EUR = XLM/USD × USD/EUR`,
where `USD/EUR` comes from a forex source per ADR-0010):

- **`/v1/price`**: forex factor MUST be the most recent FX-source
  quote published *at-or-before the bucket's end timestamp*. Same
  algorithm in every region → same factor → preserves the closed-
  bucket consistency property. Implemented in
  `internal/aggregate/orchestrator/triangulate.go` (Task #71): for
  every fiat-vs-fiat leg in a configured triangulation chain, the
  orchestrator queries `timescale.Store.FXQuoteAtOrBefore(pair,
  bucketEnd, FXSources())` instead of reading the leg's cached VWAP.
  Misses fall back to cached VWAP and increment
  `stellarindex_aggregator_fx_snap_fallback_total{leg=…}`; the alert
  in `deploy/monitoring/rules/aggregator.yml` fires at 30 m sustained
  fallback dominance.
- **`/v1/price/tip`**: forex factor is the freshest FX quote
  available at request time. Lives within the tip's
  no-cross-region-consistency contract.
- **`/v1/observations`**: per-source observations don't get FX
  conversion — surface returns the raw observed trade. Customer
  applies their own conversion if they want EUR-denominated.

### URL discipline as the contract enforcer

The URL is what makes the three contracts distinguishable. To
prevent silent contract changes:

- **Query parameters MUST NOT change a surface's consistency
  contract.** `?granularity=1h` on `/v1/price` is fine — still
  closed-bucket. `?freshness=tip` on `/v1/price` is **prohibited**
  by this ADR — tip semantics require the `/v1/price/tip` URL.
- **A request whose intent doesn't match the URL's contract MUST
  return a 400.** E.g. asking `/v1/price/tip?granularity=1m`
  returns 400 — granularity is a closed-bucket concept; tip
  doesn't have granularities.
- **Wire-shape divergence is part of the contract.** `/v1/price`
  returns a single `data` object; `/v1/observations` returns a
  `data` array. A grep'd codebase can audit which endpoints get
  which consistency guarantee just by looking at URL strings.

## Consequences

- **Positive — customers self-select consistency tier by URL.**
  Code-review-grep'able. An oracle integration that should never
  consume tip data can be flagged in CI by checking that `/v1/price/tip`
  doesn't appear in oracle-client code paths.

- **Positive — the closed-bucket contract is load-bearing for
  trust.** ADR-0015 stays the API's strongest correctness statement;
  `/v1/price` keeps that property unconditionally.

- **Positive — `flags.stale` becomes a meaningful single-bit signal
  again.** Pre-ADR, the flag's per-endpoint meaning drifted; after,
  it's "below baseline" everywhere.

- **Positive — wire shapes are honest about what each surface is.**
  `price_type="vwap"` vs `"last_trade"` is observable; the customer
  knows which they got. `window_seconds=0` is a clear "fallback"
  signal.

- **Negative — three endpoints to maintain instead of one.** ~3×
  the OpenAPI surface to keep accurate, ~3× the integration tests,
  ~3× the docs/reference output. Acceptable cost for the
  consistency-tier separation.

- **Negative — chained-fiat consistency on `/v1/price` requires the
  FX-factor-snap rule.** Without the "FX quote at bucket end" rule,
  closed-bucket rates for non-USD pairs could subtly differ across
  regions due to FX poll timing. This ADR pins the rule; the
  aggregator's chaining code must implement it.

- **Operational impact — three runbooks for "rate looks wrong".**
  Per surface: closed-bucket diagnostics (CAGG refresh lag, ingest
  gap), tip diagnostics (window threshold, fallback frequency),
  observations diagnostics (per-source freshness, missing source).
  All build off existing runbooks but add per-surface entry points.

- **Downstream design impact — SSE stream (item #5) wires onto the
  tip surface.** `/v1/price/stream` pushes the same shape as
  `/v1/price/tip` updates. Same consistency contract (no
  cross-region guarantee), same wire shape, request/response vs
  streaming as the only difference.

## Alternatives considered

1. **Single endpoint with `?freshness=closed|tip|raw` query
   parameter.** Rejected: silently changes the consistency contract
   based on a parameter, which is exactly what the URL discipline
   above is designed to prevent. A stray query string in some
   forgotten config could turn an oracle integration into a
   non-consistent reader without the customer noticing.

2. **Supersede ADR-0015 to allow in-progress on `/v1/price`.**
   Rejected: that contract is load-bearing for cross-region
   consistency and for SEP-40 oracle integrations. Loosening it
   would break every downstream consumer that depends on regional
   determinism.

3. **Stream-only realtime (no `/v1/price/tip`, just SSE).**
   Rejected: request/response semantics are still useful for clients
   that want a single quick lookup without subscribing to a
   long-lived stream. Most ticker UIs poll once per second; SSE is
   overkill for that pattern.

4. **No realtime surface at all — closed-bucket only forever.**
   Rejected: real customer demand for sub-minute freshness on
   ticker UIs. The SLA doesn't require it but the product does.

5. **Cap the `/v1/price/tip` last-good-price fallback at some max
   age (e.g. 1 hour) and 404 above that.** Rejected: introduces a
   sharp threshold customers must reason about. The wire already
   carries `observed_at`; let the customer decide what's "too
   stale" for their use case rather than us imposing a one-size
   cutoff.

## References

- [ADR-0015](0015-last-closed-bucket-rate-serving.md) — the
  closed-bucket-only contract this ADR explicitly preserves on
  `/v1/price`.
- [ADR-0010](0010-off-chain-fiat-representation.md) — fiat
  representation; this ADR pins the FX-factor-snap rule for
  chained rates.
- [ADR-0016](0016-per-region-storage-strategy.md) — the per-region
  trust model that makes the cross-region consistency property
  load-bearing.
- [ADR-0017](0017-archive-completeness-invariants.md) — archive
  invariants underlying the closed-bucket contract.
- [`scripts/ci/lint-openapi-urls/`](../../scripts/ci/lint-openapi-urls/) —
  CI lint that enforces the URL-discipline rule from this ADR; rejects
  query parameters whose name or enum implies selecting between
  consistency surfaces.
- [`docs/operations/api-design.md`](../reference/api-design.md) §5 —
  per-endpoint surface descriptions; this ADR drives the per-surface
  rows.
- Item #5 in the Phase 5 work list — SSE `/v1/price/stream`, lands
  on top of the tip-surface contract.
- Item #27 — `/v1/price/tip` implementation PR.
- Item #28 — `/v1/observations` implementation PR (deferrable).
