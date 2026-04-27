---
adr: 0009
title: API latency budget — per-component time slices summing to p95 ≤ 200ms / p99 ≤ 500ms
status: Accepted
date: 2026-04-27
supersedes: []
superseded_by: null
---

# ADR-0009: API latency budget

## Context

Stellar RFP §Performance and Freighter RFP §3 commit Rates Engine
to **p95 ≤ 200 ms, p99 ≤ 500 ms** end-to-end on the public REST
endpoints (coverage-matrix S9.2, F3.1, F3.2). Without an
explicit per-component budget those numbers are aspirational;
with one, every PR can be assessed against its share before it
lands rather than discovered as latency drift after.

The Redis cache schema decision (originally pencilled into this
ADR slot) is already covered by ADR-0007 ("Redis as hot-path
cache + rate-limit + ephemeral state"). This ADR repurposes the
0009 reservation for the load-bearing question 0007 doesn't
answer: **how much latency is each component on the hot path
allowed to consume.**

The constraint shape:

- p95 ≤ 200 ms — the cache-warm steady state. Most requests
  hit Redis and never touch Postgres.
- p99 ≤ 500 ms — the cache-cold + Postgres-fallback path. The
  worst-percentile request is allowed to take 2.5× the median
  but no more.
- "End-to-end" means edge-to-client: TLS terminate at HAProxy,
  through middleware, handler, store, network back. Anything we
  control counts.

Per ADR-0008 the architecture has three tiers: hot (Redis,
≤30s), warm (Timescale, ≤90d raw + indefinite aggregates), cold
(MinIO, archive). The latency budget allocates time across the
hot + warm path; cold is never on the request hot path by design.

## Decision

**Adopt the following per-component latency budget for the
`/v1/price` and `/v1/vwap` / `/v1/twap` / `/v1/ohlc` hot-path
endpoints. Other endpoints (history, batch, oracle) get
larger budgets per their slower-path nature; documented in
`docs/architecture/api-design.md` companion §Latency Budgets.**

### p95 budget — cache-warm path (200 ms total)

| component | budget | notes |
| --- | --- | --- |
| TLS terminate + HAProxy → API | **5 ms** | local LAN; SSL session resumption assumed |
| API ingress middleware (request-id, CORS, logger, ratelimit, recoverer) | **5 ms** | every middleware adds < 1 ms; total budgeted with headroom |
| Auth middleware (apikey lookup or SEP-10 token verify) | **10 ms** | apikey: Redis HGET; SEP-10: ed25519 verify (~1 ms compute) + cached subject lookup |
| Handler validation (asset parsing, range parsing, problem+json on error) | **5 ms** | pure Go; in-process |
| Redis cache lookup (`price:<asset_id>` per ADR-0007) | **20 ms** | one round-trip from API host to Redis cluster + `GET` + JSON deserialise |
| Response marshalling + envelope wrap | **5 ms** | json.Marshal of < 1 KB body |
| Network egress (API → HAProxy → client) | **15 ms** | symmetric with ingress |
| **TOTAL (steady-state hot path)** | **65 ms** | leaves 135 ms headroom against p95 ≤ 200 ms |

The 135 ms headroom absorbs:
- p95-tail Redis round-trip variance (cluster-side GC pauses)
- HTTP/2 connection-coalescing penalty on first request
- Occasional GC tail in the API process
- Post-deploy cache cold start (ADR-0007 promises sub-second
  warmup; this budget tolerates an order of magnitude over that)

### p99 budget — cache-cold / Postgres-fallback path (500 ms total)

| component | budget | notes |
| --- | --- | --- |
| Everything in the steady-state path above (excluding Redis result) | **45 ms** | Redis MISS still costs the GET round-trip |
| TimescaleDB primary `LatestClosedVWAP1mForPair` query | **60 ms** | indexed lookup on `(base_asset, quote_asset, bucket DESC)`; CAGG row scan; NUMERIC text marshal |
| Cache write-back (set `price:<asset_id>` with TTL) | **20 ms** | Redis SET; not blocking — fire and continue |
| **TOTAL (cache-cold path)** | **125 ms** | leaves 375 ms headroom against p99 ≤ 500 ms |

The 375 ms p99 headroom absorbs:
- Postgres query plan-cache miss (one-off after migration)
- Connection pool drain + reopen
- Single-replica failover (Patroni promotes a sync replica;
  budget allows for the short retry-on-failover window)
- WAN latency for cross-region read fallback (only relevant if
  ADR-0008's degradation contract activates an out-of-region
  read; nominally we don't traverse a WAN on a /v1/price hit)

### What's NOT on the hot path

By design these contribute zero to the budget because they're
asynchronous to a request:

- Aggregator policy chain (VWAP/TWAP/OHLC compute) — runs as a
  background daemon; the API reads the resulting row.
- Indexer ingest path (galexie → dispatcher → decoder → trades
  hypertable) — fully decoupled per ADR-0008's "ingest must
  never block serving" principle.
- Cross-region replication — async via PostgreSQL logical
  replication; the API reads from the local Patroni primary.
- Hubble cross-checks, WASM history audits, divergence
  monitoring — all out-of-band.

A handler that synchronously called any of these would violate
the budget *and* the architectural contract; review-gate.

### Per-endpoint exceptions

The 200/500 ms budget is for the hot serving endpoints. Slower
endpoints get their own budgets, documented inline in their
handler tests as `// budget: pXX = N ms`:

| endpoint family | p95 budget | p99 budget | rationale |
| --- | --- | --- | --- |
| `/v1/price`, `/v1/vwap`, `/v1/twap`, `/v1/ohlc` | 200 ms | 500 ms | hot path, cache-served |
| `/v1/price/batch` | 500 ms (≤100 assets) | 1000 ms | batched Redis pipeline; scales linearly with batch size up to the documented cap |
| `/v1/history` (recent ranges) | 500 ms | 1500 ms | Timescale time-bucket scan; bounded by `-limit` |
| `/v1/history/since-inception` | 5 s | 15 s | full-range CAGG scan; expected slow; client uses pagination |
| `/v1/oracle/lastprice` (SEP-40 wire format) | 200 ms | 500 ms | hot path; same as /v1/price |
| `/v1/healthz`, `/readyz`, `/version` | 5 ms | 20 ms | smoke endpoints; no DB |
| `/metrics` (operator-only, unmetered) | n/a | n/a | scraper-driven; cardinality controls applied at the registry |

### Enforcement

- **Histogram alerts** on `http_request_duration_seconds_bucket`
  per route + status (per `internal/obs/metrics.go`). Existing
  alerts in alerts-catalog:
  - `ratesengine_api_latency_p95_high` — > 500 ms p95 sustained
    > 2 min (2.5× the steady-state target — leaves room before
    paging on the P2)
  - `ratesengine_api_latency_p99_high` — > 2 s p99 sustained
    > 2 min (4× the cache-cold budget — paging threshold)
- **Load-test gate** — Week 9 SLA-validation deliverable
  exercises the budget under 2,000 rps on cache-served endpoints.
  Failure to meet p95 ≤ 200 ms / p99 ≤ 500 ms blocks the
  Phase-6b exit gate.
- **Per-handler test budgets** — handlers that touch novel paths
  (new store method, new external dependency) should add a unit-
  level latency assertion in their test (e.g.
  `if elapsed > 10*time.Millisecond { t.Errorf(...) }`) on a
  representative input. Catches regressions before integration.

## Consequences

- **Positive — every PR has a yardstick.** A reviewer asking
  "does this change keep us under 200 ms" can point at the
  per-component budget and assess on-the-spot, rather than
  arguing in the abstract.

- **Positive — degradation contract becomes quantitative.** ADR-
  0008 says "ingest must never block serving"; this ADR adds
  numerical teeth. The 135 ms headroom is the budget for the
  degradation tax (envelope flag work, source-list assembly,
  `as_of` resolution) — anything more and we've over-engineered
  the envelope.

- **Negative — the budget is conservative.** A 200 ms target with
  65 ms steady-state means most of the budget is headroom. We
  could promise lower (say p95 ≤ 100 ms) but the RFP only asks
  for 200 ms; over-promising costs us flexibility on tail
  events. Re-evaluate post-launch with real traffic data.

- **Negative — Auth middleware adds 10 ms unconditionally.** The
  apikey-tier and SEP-10-tier paths both hit Redis (or the
  in-memory token cache); 10 ms for that round-trip eats 5 % of
  the hot-path budget. Mitigation: a future optimisation can
  inline-cache JWT verification result in the request context
  via short-lived signed cookies. Out of scope for v1; the
  budget tolerates 10 ms.

- **Operational impact — every alerting threshold is set against
  this budget.** When the budget changes, the Prometheus rules
  + alerts-catalog must update in lockstep. Doc-lint already
  enforces alert-rule ↔ catalog symmetry.

- **Downstream design impact — the hot path's component list is
  effectively frozen.** Any new middleware or backend hop must
  fit within the steady-state 65 ms or document an explicit
  budget extension. Caches added below the existing Redis layer
  (e.g. an in-process LRU) are fine; new round-trips to remote
  systems are not.

## Alternatives considered

1. **No explicit budget — let load tests retroactively define
   it.** Rejected. The RFP commits to a number; we should know
   in advance how we're meeting it, not discover by failure.

2. **Stricter target (p95 ≤ 100 ms).** Considered; rejected for
   v1. The RFP commitment is 200 ms; over-engineering to 100 ms
   would push us into in-process cache territory that violates
   the ADR-0008 "Redis is the hot tier" decision.

3. **No allocation between components — global budget only.**
   Rejected. A 200 ms global budget with no allocation makes
   reviewers guess whether their PR's 5 ms add is acceptable.
   Allocation makes it cheap to assess.

4. **Tighten the alerts to fire AT the budget rather than 2.5×
   above.** Rejected for now — the budget is a pass/fail line for
   load tests; the alert is a "page someone in the middle of the
   night" line. Different thresholds for different consumers.
   Alert thresholds may tighten post-launch as steady-state
   variance becomes known.

## References

- [`docs/architecture/coverage-matrix.md`](../architecture/coverage-matrix.md)
  S9.2 / F3.1 / F3.2 — the requirements this ADR closes.
- [`docs/architecture/ha-plan.md`](../architecture/ha-plan.md)
  §10 launch-checklist — the load-test gate references this ADR.
- [`docs/operations/alerts-catalog.md`](../operations/alerts-catalog.md)
  §API plane — `ratesengine_api_latency_p95_high` and
  `_p99_high` thresholds derived from this budget.
- ADR-0007 — Redis as hot-path cache; defines the keyspace + TTL
  semantics this budget assumes.
- ADR-0008 — HA topology; defines the three-tier hot/warm/cold
  separation this budget allocates time across.
- ADR-0015 — Closed-bucket-only serving; defines what "fresh
  data" means and therefore what the cache key returns.
