---
adr: 0026
title: Stablecoin → fiat proxy is late-binding aggregator policy, not eager ingest normalisation
status: Accepted
date: 2026-05-10
supersedes: []
superseded_by: null
---

# ADR-0026: Stablecoin → fiat proxy is late-binding aggregator policy, not eager ingest normalisation

## Context

Most CEX and DEX trades quote against a USD stablecoin
(USDT / USDC / PYUSD) or a EUR stablecoin (EUROC / EUROB) or
a MXN stablecoin (MXNe), not against the underlying fiat
itself. Customers asking for `XLM/USD` overwhelmingly mean
"the price of XLM in dollars, however you can compute it" —
not strictly trades against the literal `fiat:USD` asset
identifier (which is a derived ECB-anchored reference, not a
tradeable instrument anywhere we ingest from).

We need to answer requests for `XLM/fiat:USD` even when the
overwhelming majority of underlying trades are `XLM/USDT`,
`XLM/USDC`, etc. — and we need to do it without losing the
ability to detect stablecoin depegs (which is exactly the
moment the equivalence breaks).

Two structural options:

1. **Eager normalisation at ingest.** Decoders rewrite
   `USDT` → `USD` (and friends) when storing trades. The
   trades hypertable contains only the rewritten pair; the
   aggregator sees nothing but `XLM/USD` even when the
   actual on-wire data was `XLM/USDT`.
2. **Late binding at aggregation.** Decoders store the real
   pair (`XLM/USDT`, `XLM/USDC`). The aggregator (and the
   API surfaces that fall through to it) maps the stablecoin
   to its fiat anchor at compute time, with the mapping
   pluggable per-deployment.

Option 1 is structurally cleaner for the aggregator (no
mapping logic in the hot path) but loses three things:

- **Depeg detection.** When `USDT` trades at $0.97, eager
  normalisation labels those trades as `XLM/USD = 0.97 *
  per-XLM-USDT` rather than `XLM/USDT = per-XLM-USDT`. The
  divergence vs `XLM/USDC` (still $1.00-anchored) is invisible
  in the rewritten pair; the customer sees a "USD" price
  that's actually a depegged-stablecoin price with the
  rewriting hiding the cause.
- **Per-stablecoin signal.** A customer asking
  `XLM/credit:USDC:GA5Z…` still wants the literal USDC
  pair. Eager rewriting at ingest would force a second
  (un-rewritten) storage path for the same underlying trades
  to preserve that surface — duplicate storage, duplicate
  ingest cost.
- **Reversibility.** A peg policy bug (wrong stablecoin
  added to the rewrite list, scaling error) becomes
  un-correctable historically without re-ingest. With late
  binding, fixing the policy fixes today's API responses
  immediately for ALL historical data.

Option 2 (late binding) was the implicit shape from the
start — CLAUDE.md's "Things that will surprise you" section
flagged it before any rewriting was wired — but the API
surfaces (`/v1/price`, `/v1/price/tip`, `/v1/vwap`,
`/v1/twap`, `/v1/ohlc`, `/v1/oracle/prices`, `/v1/chart`)
each had to slot the proxy fallback into their own resolution
chain. PRs #1217 / #1218 / #1224 / #1225 / #1226 (etc.) added
the fallback to one endpoint each over a couple of days.
This ADR records the policy that those PRs each instantiated.

## Decision

**Stablecoin-to-fiat mapping is aggregator-time policy, not
ingest-time rewriting.** Trades are stored at the real pair
the venue emitted; mapping happens in `tryStablecoinFiatProxy`
on the API path when a request for `X/fiat:USD` (or `X/fiat:EUR`,
`X/fiat:MXN`, etc.) misses the literal-pair cache layer.

Mapping shape (canonical asset → fiat target):

- USD anchor: `credit:USDT:…`, `credit:USDC:…`, `credit:PYUSD:…` → `fiat:USD`
- EUR anchor: `credit:EUROC:…`, `credit:EUROB:…` → `fiat:EUR`
- MXN anchor: `credit:MXNe:…` → `fiat:MXN`

The peg list lives in operator config (`api.peg_aliases`)
not hard-coded — depeg events trigger removing a peg from
the list (or an emergency hot-cache override), not redeploying
binaries. A deployment with an empty peg list still serves
the literal pair (`X/credit:USDC:…`) but returns 404 on
`X/fiat:USD` requests when no `X/credit:USD…` literal trades
exist (the "honest no-data" answer rather than a fabricated
fallback).

The fallback is **opt-in per-deployment** so a regional
deployment that doesn't trust a particular stablecoin's peg
can simply not list it. The default production peg list
covers USDT / USDC / PYUSD / EUROC / EUROB / MXNe.

## Consequences

- **Positive:**
  - Depeg detection works. `XLM/USDT` and `XLM/USDC` remain
    independently observable at all times. A depeg shows up
    as a divergence between the two literal pairs;
    `XLM/fiat:USD` continues to serve via the `tryStablecoinFiatProxy`
    chain, which can be operator-overridden during a depeg
    incident.
  - Per-stablecoin transparency. Customers asking for
    `XLM/credit:USDC:GA5Z…` get exactly that — the
    literal-pair price, no proxy.
  - Reversible policy. Adding / removing a peg is a config
    edit. No re-ingest required.
  - Hot path stays cheap. The proxy fallback only fires on a
    cache miss for the literal pair; the common case hits
    the literal `XLM/USDC` cache path.
- **Negative:**
  - Every API surface that resolves a `fiat:*` quote must
    explicitly call `tryStablecoinFiatProxy` after its
    primary lookup misses. We accepted this duplication
    because the alternative (one universal "expand to peg
    proxy" middleware) would couple every endpoint's response
    shape (`flags.triangulated`, `flags.stale`, etc.) to the
    middleware's view of "did we proxy?" — a worse
    abstraction.
  - Customers seeing `XLM/fiat:USD` get a pseudo-pair that
    doesn't directly correspond to any single set of trades.
    OpenAPI documentation calls this out per surface (the
    `flags.triangulated` advisory for routes that triangulate;
    the proxy chain for the rest).
  - Operator burden: keep `api.peg_aliases` in `/etc/stellarindex.toml`
    in sync with the de-facto peg state. Documented in the
    config reference and the `price-divergence` runbook.
- **Operational impact:** During a depeg event, the operator
  removes the affected peg from `api.peg_aliases` (one config
  line + reload) so `/v1/price?asset=X&quote=fiat:USD`
  returns the literal-pair price (or 404 if no literal-USD
  trades exist) rather than a USDT-via-stablecoin number.
  Runbook: `docs/operations/runbooks/price-divergence.md`.
- **Downstream design impact:**
  - Triangulation (ADR-0019 follow-up) builds on the proxy
    layer — when no direct-or-proxy-pegged path resolves a
    pair, the triangulator stitches via an intermediate
    asset. The proxy layer is the first try; triangulation
    is the second.
  - Cross-region byte-identical contract (ADR-0015) requires
    every region to ship the SAME `api.peg_aliases`. Drift
    breaks the contract; the cross-region monitor checker
    (`stellarindex-ops cross-region-check`) verifies the
    config hash alongside per-pair price equality.

## Alternatives considered

1. **Eager ingest-time rewrite.** Rejected because it would
   hide depeg events (the reason "stablecoin ≈ fiat" is *not*
   a safe identity in the first place) and force duplicate
   storage paths to preserve per-stablecoin signal. See
   Context for the full breakdown.
2. **Per-stablecoin trades view.** Have the aggregator
   maintain a derived `XLM/fiat:USD` row that's the volume-
   weighted average of `XLM/USDT` + `XLM/USDC` + `XLM/PYUSD`
   etc., persisted to a second cagg. Rejected because the
   weighting policy belongs at request-time (so an operator
   can adjust during an incident) and persisting it commits
   the historical record to a particular weighting that may
   later be wrong. The proxy fallback achieves the same
   customer-visible answer without the historical commitment.
3. **404 on `X/fiat:USD` if no literal-USD trades.** Rejected
   because the overwhelming majority of customers asking for
   `XLM/USD` mean "in dollars, however you can compute it".
   Returning 404 to that question is technically honest but
   practically useless — every customer would build a
   client-side "try X/USDC if X/USD 404s" loop, which is the
   proxy fallback we now ship server-side. Centralising the
   policy in the server is the right place to express it.

## References

- Related ADRs:
  - [ADR-0010](0010-off-chain-fiat-representation.md) —
    why `fiat:USD` is a first-class canonical asset to begin
    with.
  - [ADR-0015](0015-last-closed-bucket-rate-serving.md) —
    cross-region byte-identical contract that this peg-alias
    config has to honour.
  - [ADR-0018](0018-api-consistency-surfaces.md) — the
    per-surface `flags.stale` semantics that this fallback
    has to compose with.
  - [ADR-0019](0019-anomaly-response-and-confidence-scoring.md)
    — the triangulation layer this proxy fallback sits below.
- Implementation surface (this is not exhaustive — every PR
  adding the proxy to a new endpoint instantiates this ADR):
  - PR #1217 — `/v1/price` proxy fallback
  - PR #1218 — `/v1/price/tip` proxy fallback
  - PR #1224 — `/v1/vwap` + `/v1/twap` proxy fallback
  - PR #1225 — SEP-40 oracle endpoints proxy fallback
  - PR #1226 — `/v1/ohlc` proxy fallback
  - PR #1219 — `/v1/chart` proxy fallback
- Runbook: `docs/operations/runbooks/price-divergence.md`
  documents the operator response when a peg breaks.
- The late-binding policy was an implicit decision from the start
  but lacked a binding ADR until this one.
- CLAUDE.md "Things that will surprise you" carries the
  short version for AI agents reading the repo cold.
