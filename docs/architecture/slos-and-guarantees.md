---
title: Performance SLOs & guarantees — targets → proof
last_verified: 2026-06-17
status: living; one section per SLO
---

# Performance SLOs & guarantees

The operational service-level objectives Stellar Index holds itself to,
each with concrete, re-runnable proof. Companion docs:
`prod-verification-2026-06-12.md` (probe-level), `coverage-matrix.md`
(requirement-level), `freshness-definition.md`.

## Freshness — real-time price staleness ≤ 30 s

- **Surface**: `/v1/price/tip` (+ SSE stream). Definition pinned in
  `freshness-definition.md`.
- **Proof**: sla-probe verdict series (10-min timer, r1) — `pass` with
  per-endpoint freshness; spot probes show single-digit-second
  `observed_at` age for both `crypto:XLM` and `native`.

## Latency — p95 ≤ 200 ms (p99 ≤ 500 ms)

This is a **server-latency** SLO. The authoritative evidence is two
independent measurements that agree:

- **Server-side histogram (Prometheus, measured live under sustained k6
  load, 2026-06-13):** `http_request_success_duration_seconds`
  **p95 = 68 ms, p99 = 98 ms** — PASS by ~3× / ~5×. `all`-request p95
  equals success p95 (68 ms), confirming a ~0 % error rate at the served
  load.
- **k6 client-side, origin-direct** (`00-acceptance-rate.js`, 30 min @
  17 req/s = the 1000 req/min target rate, against `http://localhost:3000`
  to bypass the edge): **30,600 requests, p95 = 54.4 ms, p90 = 48.0 ms,
  max = 901 ms, error rate = 0.00 % (0 / 30,600), checks 100 %.** All
  three k6 thresholds (`p(95)<200`, `p(99)<500`, `rate<0.001`) green.
  **PASS.**

**Why origin-direct, not through Cloudflare:** the production API sits
behind Cloudflare. A single-IP synthetic k6 burst trips Cloudflare's
anti-abuse layer (designed to block exactly that shape) — yielding 60 s
timeouts that are an artifact of the *test source*, not server latency (a
through-edge run showed a 13 % "error" rate whose `expected_response:true`
p95 was still 191 ms). Real traffic is distributed across many client IPs
and does not trip this. The origin-direct run + the server-side histogram
both isolate the quantity this SLO actually constrains.

## Historical retention ≥ 1 yr (since inception where possible)

Verified live 2026-06-13:

- **XLM/USD (headline pair)**: `/v1/history/since-inception?asset=native&quote=fiat:USD`
  serves daily VWAP back to **2021-02-01** (5.4 yr — when reliable
  on-chain USD anchoring began on Stellar) → ≫ the ≥1yr floor.
- **SDEX since-inception**: `/v1/ohlc?interval=1d` against classic
  anchor pairs serves daily bars back to 2016-2018; the `prices_1d`
  continuous aggregate holds native pairs back to **2015-11-18** (6.3 M
  rows). 1h+ granularities retained indefinitely (migration 0031 removed
  trades retention; caggs indefinite). Raw `trades` is the ~3-month
  served working set (ADR-0034); full history lives in the ClickHouse
  lake + the indefinite caggs.
- **OHLC candlesticks, deep**: `/v1/ohlc?quote=fiat:USD&interval=…` —
  the series handler COMBINES the USD-pegged constituent pairs per bucket
  (the same source set the live aggregator's VWAP uses:
  `aggregate.ExpandTargetPairWithClassicPegs`). Verified live: XLM/USD
  candles to **2021-02-01**, and it **generalises to every asset**
  (AQUA/USD from 2021-08, its USD inception). `triangulated: true` flags
  the proxy combine. Non-USD pairs (XLM/anchors) still serve to 2015 per
  the cagg.

## Throughput — ≥ 1000 requests/min per client

- **Proof**: the origin-direct run sustained **1031 req/min on a single
  key** (Prometheus `rate(http_request_duration_seconds_count)`, measured
  live) for 30 min with **zero rate-limit (429) failures**.
- **Headroom**: anon tier is provisioned at 6000/min and authenticated
  keys default to 1000/min (`key_rate_limit_per_min`, per-key
  configurable via `mint-key -rate-limit-per-min`); a saturation probe
  drove ~18,000/min on one key before any limiter pushback.

## Open source — publicly accessible + reproducible

- Apache-2.0; the full system builds reproducibly (`go build ./...`,
  `make build`) from a clean checkout with no proprietary dependencies.
- The public site, status page, and API docs are served from the same
  codebase; the public API tier is a perpetual commitment.

## Documentation & onboarding

- **Reference**: OpenAPI-generated reference (`docs/reference/api`).
- **Self-service onboarding**: `docs/getting-started.md` leads with the
  ≤1-min path — `POST /v1/signup` (email → usable `rek_…` key, no Stellar
  wallet needed), then the SEP-10 account-bound path as the advanced
  option.
- **E2E walkthrough (verified 2026-06-13 on r1):** `POST /v1/signup`
  (`{email,label}`) → `200` with a fresh `rek_…` key (`tier: apikey`,
  `rate_limit_per_min: 1000`); the key immediately authenticated
  `GET /v1/account/me` → `200`, on both `X-API-Key` and
  `Authorization: Bearer`.
- **Examples**: `examples/` curl scripts + auto-generated Postman
  collection.

## Beyond the baseline

- **`/v1/coverage`** (live): per-source ADR-0033 verdicts — substrate
  continuity to genesis, recognition, projection reconciliation.
  **15/15 sources `complete=true`** (run-4, 2026-06-13).
- **`/v1/protocols` + `/v1/protocols/{name}`** (live): 15-protocol
  directory with verified factory trust-roots (ADR-0035), registered
  contracts, 24h activity.
- Per-protocol verification pages (`docs/protocols/`) cross-checked
  against team-published Dune data (53 DeFindex vaults, 178 Aquarius
  pools, Blend 2-factory enumeration).
