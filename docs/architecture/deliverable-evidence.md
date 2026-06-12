---
title: Deliverable evidence — acceptance criteria → proof
last_verified: 2026-06-13
status: living until sign-off; one section per acceptance criterion
---

# Deliverable evidence

One page per acceptance criterion (ctx-proposal §Milestones + Stellar
RFP §3/§5 + Freighter RFP SLAs) → the concrete, re-runnable proof.
Companion docs: `prod-verification-2026-06-12.md` (probe-level),
`coverage-matrix.md` (requirement-level), `freshness-definition.md`.

## AC1 — Real-time price staleness ≤ 30 s

- **Surface**: `/v1/price/tip` (+ SSE stream). Definition pinned in
  `freshness-definition.md` — pre-agree the one-liner with the customer.
- **Proof**: sla-probe verdict series (10-min timer, r1) — `pass` with
  per-endpoint freshness; spot probes show single-digit-second
  `observed_at` age for both `crypto:XLM` and (post-8fde6c84) `native`.
- **Caveat closed**: the native-spelling alias gap found 2026-06-12 is
  fixed + deployed.

## AC2 — p95 ≤ 200 ms (p99 ≤ 500 ms)

- **Proof**: k6 realistic-mix soak (300 req/s sustained 10 min — ~18×
  the contractual 1000 req/min) against the production deployment:
  `test/load/reports/2026-06-12/06-mixed-realistic.json`.
  Interim observed: p95 ≈ 86 ms, p99 well under target, from a
  cross-internet vantage. <!-- FINAL NUMBERS: fill from the post-auth-fix run -->
- Server-side p95 (Prometheus `stellarindex_api_*` histograms) agrees.

## AC3 — Historical retention ≥ 1 yr (ideally since inception)

- **Proof**: `/v1/history/since-inception` + `/v1/ohlc?interval=1d`
  serve daily bars back to 2015 (SDEX genesis); probe report §history
  shows daily series to 2021+ for XLM with full RFP timeframe ladder
  (1m/15m/1h/4h/1d × 1h/24h/1w/1mo/1yr/all-time). 1h+ granularities
  retained indefinitely (migration 0031 removed trades retention;
  caggs indefinite).

## AC4 — ≥ 1000 requests/min per client

- **Proof**: anon tier currently 6000/min (probe report); per-key tiers
  configurable (`mint-key -rate-limit-per-min`); the k6 soak sustains
  ~18,000/min on one key without rate-limit failures.

## AC5 — Completely open source; publicly accessible + reproducible

- **Status**: pre-flight CLEAN (secrets/license/VERSIONS —
  `public-flip-preflight-2026-06-12.md`); export rules recorded.
- **Remaining**: create org + public repo (operator), fresh-history
  export, v1.0.0 release. <!-- flip executed: link the public repo here -->

## AC6 — Production deployment within ~10 weeks

- **Proof**: r1 serving since 2026-05-03; current binaries (Stellar
  Index) deployed 2026-06-12; smoke 13/13; status page live.
- **Multi-region**: decision per readiness-plan §6 due Jun 18.

## AC7 — API reference docs + self-service onboarding

- **Proof**: OpenAPI-generated reference (docs/reference/api),
  getting-started ≤5-min path, /v1/signup self-service, examples/
  (curl + Postman). <!-- E5 onboarding E2E walkthrough: date+result -->

## Beyond-contract differentiators (the demo ceiling)

- **`/v1/coverage`** (live): per-source ADR-0033 verdicts — substrate
  continuity to genesis (proven at tip 63.0M, windowed audit),
  recognition, projection reconciliation. <!-- fill: N/15 complete at sign-off -->
- **`/v1/protocols` + `/v1/protocols/{name}`** (live): 15-protocol
  directory with verified factory trust-roots (ADR-0035), registered
  contracts, 24h activity.
- Per-protocol verification pages (`docs/protocols/`) cross-checked
  against team-published Dune data (53 DeFindex vaults, 178 Aquarius
  pools, Blend 2-factory enumeration).
