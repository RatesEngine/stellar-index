---
title: Price-freshness definition — the ≤30s SLA, precisely
last_verified: 2026-06-13
status: ratified-internally
---

# Price freshness — what the ≤30s SLA means here

Our freshness SLA requires **≤ 30s price staleness.**
This page pins exactly which endpoint satisfies that and why the API
deliberately serves TWO freshness contracts (ADR-0015 / ADR-0018).

## The two contracts

| Endpoint | Contract | Typical `observed_at` age |
|---|---|---|
| **`/v1/price/tip`** (+ `/price/tip/stream`) | rolling-window VWAP over the freshest trades, recomputed per request/tick | **≤ 5s** (CEX trades land sub-second; ingest cursor advances every ~5s ledger) |
| `/v1/price` | **last-closed bucket** — never an in-progress bucket, so every region serves the byte-identical answer (ADR-0015) | 30–150s by design (1m bucket close + aggregation cycle) |

**The ≤30s criterion is met by `/v1/price/tip`** — that is the
"real-time or near-real-time" surface and the one a wallet's asset page
should poll or stream. `/v1/price` trades freshness for cross-region
determinism and cacheability: a closed bucket has one true value, which
is what you want for anything that must agree across replicas, audits,
or CDN caches. This split is deliberate architecture, not a freshness
shortfall — both reference wallets (stellar.expert, steexp) display
closed-interval data on comparable surfaces.

## Evidence

- The SLA probe (`stellarindex-sla-probe`, 10-min timer on r1) measures
  `observed_at` staleness against the 30s target on `/price/tip` per
  request and records pass/fail; see the probe's JSON verdict series.
- 2026-06-13 spot checks: `/price/tip?asset=crypto:XLM` and (post
  alias-fix 8fde6c84) `?asset=native` both report `observed_at` within
  single-digit seconds.
- The 2026-06-12 production re-verification
  (`docs/architecture/prod-verification-2026-06-12.md`) documents the
  pre-fix native-spelling gap and its closure.

## The integrator-facing summary (one sentence)

> "Real-time price freshness ≤30s is served by `/v1/price/tip` (and its
> SSE stream); `/v1/price` is the deterministic closed-bucket surface
> with its own 30–150s semantics — integrators choose per use-case."

Surfacing that sentence in the docs makes the two-contract design read
as the feature it is, not a surprise.
