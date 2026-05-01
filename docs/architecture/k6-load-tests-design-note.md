---
title: k6 load test suite (test/load/) — design note (DRAFT — not pushed)
last_verified: 2026-04-30
status: design draft (Task #74 — unblocks Task #77 p95 proof)
related:
  - docs/freighter-rfp.md §SLA targets
  - docs/architecture/coverage-matrix.md S9.1 / S9.2
  - docs/operations/runbooks/api-latency.md (the alert this proves we don't trip)
  - deploy/monitoring/rules/slo.yml (multi-window SLO rules)
---

# k6 load test suite — design note

**Working draft on local-only branch
`design/k6-load-tests-design-note`. Bootstraps Task #74
implementation. Directly unblocks Task #77 (p95 ≤ 200 ms proof
report), which is a Freighter RFP contract requirement.**

## What we're proving

Two specific commitments under load:

1. **Freighter SLA p95 ≤ 200 ms** (coverage matrix S9.2). The
   public API serves p95 ≤ 200 ms across the realistic mix of
   `/v1/price`, `/v1/vwap`, `/v1/twap`, `/v1/history`, batch +
   stream surfaces under representative concurrency.
2. **The 99.9 % availability SLO holds under sustained load**
   (ADR-0009). Error rate stays < 0.1 % across endpoints during
   the soak phase.

Out of scope (deliberately):
- Chaos / failure-mode behaviour — that's Task #75's chaos suite.
- Long soak (24 h+) regression — pre-launch dryrun, lower priority.
- Cross-region consistency tests — these need multi-region
  staging deployed; not assumed here.

## Why k6

Considered the alternatives:

| Tool | Pro | Con |
|---|---|---|
| **k6** | Go-friendly script syntax (JS); native Prometheus output; easy to pin in CI; LoadImpact has a free-tier cloud-runner if we ever need scale-out | Yet another runtime |
| Vegeta | Single binary; Go-native | Less ergonomic for scenario-shaped tests |
| Gatling | Mature; Scala | JVM cold-start |
| wrk2 | Smallest footprint | No script abstraction |
| Custom Go | Full control | Reinvents the wheel |

k6's Prometheus exporter wires directly into the existing
Grafana stack so the report can include per-endpoint p95 charts
generated from the same dashboards on-call uses. That's the
deciding factor — the load test artefact and the production
artefact are graphed the same way.

## Layout

```
test/load/
├── README.md                       (operator-facing: how to run)
├── doc.go                           (package-level doc + nothing else,
│                                     so test/load/ shows up in `go doc`)
├── scenarios/
│   ├── lib/
│   │   ├── env.js                   target URL + auth helpers
│   │   ├── pairs.js                 representative pair fixtures
│   │   └── thresholds.js            shared SLA thresholds
│   ├── 01-price-hot-path.js         /v1/price + /v1/price/tip (Redis hot path)
│   ├── 02-vwap-twap.js              /v1/vwap + /v1/twap (CAGG path)
│   ├── 03-history.js                /v1/history + /v1/history/since-inception
│   ├── 04-batch.js                  /v1/price/batch (bulk fan-out)
│   ├── 05-streaming.js              /v1/price/stream + /v1/observations/stream (SSE)
│   ├── 06-mixed-realistic.js        weighted mix matching production traffic shape
│   └── 99-spike.js                  10× spike over 30s, recovery within 2m
├── reports/                          (gitignored; generated outputs)
│   └── README.md                    explains the gitignore + how the reports surface
├── docker-compose.k6.yaml            staging-target compose for local runs
└── Makefile.fragment                 included by top-level Makefile
```

Each `.js` is one scenario. The `lib/` subdirectory holds shared
helpers so scenarios stay readable and the SLA thresholds are
defined once.

## Sample scenario shape

```javascript
// scenarios/01-price-hot-path.js
import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';
import { baseUrl, apiKey } from './lib/env.js';
import { PAIRS } from './lib/pairs.js';
import { sla } from './lib/thresholds.js';

const priceLatency = new Trend('price_latency_ms');

export const options = {
  scenarios: {
    price_hot_path: {
      executor: 'ramping-arrival-rate',
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      stages: [
        { target: 100, duration: '30s' },   // ramp
        { target: 500, duration: '2m' },    // sustained baseline
        { target: 500, duration: '5m' },    // soak
      ],
    },
  },
  thresholds: sla.priceHotPath,             // see lib/thresholds.js
};

export default function () {
  const pair = PAIRS[Math.floor(Math.random() * PAIRS.length)];
  const r = http.get(`${baseUrl}/v1/price?asset=${pair.asset}`, {
    headers: { 'X-API-Key': apiKey },
    tags: { endpoint: 'price' },
  });
  priceLatency.add(r.timings.duration);
  check(r, {
    'status 200': (r) => r.status === 200,
    'has body': (r) => r.body.length > 0,
  });
  sleep(0.1);
}
```

`lib/thresholds.js` centralises the SLA targets so every
scenario asserts the same:

```javascript
export const sla = {
  priceHotPath: {
    'http_req_duration{endpoint:price}': ['p(95)<200', 'p(99)<500'],
    'http_req_failed':                   ['rate<0.001'],   // 99.9 %
  },
  vwap: {
    'http_req_duration{endpoint:vwap}':  ['p(95)<200', 'p(99)<500'],
    // ... etc
  },
};
```

## Traffic shape (`06-mixed-realistic.js`)

The mixed scenario is what the Freighter SLA argument actually
hangs on. Per-endpoint share informed by RFP traffic
expectations + audit telemetry:

| Endpoint | Share | Rationale |
|---|---|---|
| `/v1/price` (single) | 60 % | Wallet hot path |
| `/v1/price/batch` | 15 % | Wallet portfolio refresh |
| `/v1/price/tip` | 10 % | Trading-side latency-sensitive |
| `/v1/vwap` | 6 % | Analytics / charts |
| `/v1/twap` | 3 % | Analytics |
| `/v1/history` | 4 % | Charts (paged) |
| `/v1/observations/stream` (SSE) | 1 % | Long-lived clients |
| `/v1/oracle/lastprice` (SEP-40) | 1 % | Other oracles consuming us |

These match neither *exactly* the launch traffic (we don't have
that yet) nor any one customer's pattern; they're a defensible
blend. Document the assumption explicitly in the scenario file
so future tunings know what to update.

## Scenarios broken out

| File | What it stresses | Pass criteria |
|---|---|---|
| 01-price-hot-path | Redis cache + handler routing | p95 < 200 ms @ 500 rps for 5 min |
| 02-vwap-twap | CAGG read path | p95 < 200 ms @ 100 rps for 5 min |
| 03-history | Since-inception query (heavy) | p95 < 1000 ms for `since-inception`; p95 < 200 ms for windowed |
| 04-batch | bulk fan-out cap (per ADR-0018: 1000 assets max) | p95 < 500 ms @ batch-size 100, 50 rps |
| 05-streaming | SSE connection ramp + sustain | 99 % of clients receive their first event < 1 s after subscribe |
| 06-mixed-realistic | the canonical proof scenario | p95 < 200 ms across the weighted mix; error rate < 0.1 %; sustained 10 min |
| 99-spike | brief 10× burst absorption | recovery to baseline p95 within 2 min of spike end |

## How the proof report (#77) is generated

After running `06-mixed-realistic.js`, the artefact is:

1. k6's Prometheus output for the run (already wired by
   `--out=experimental-prometheus-rw`).
2. A Grafana snapshot at `https://grafana.staging/d/load-proof/...`
   capturing the per-endpoint p95 chart for the run window.
3. A markdown summary at
   `docs/operations/sla-proof-<YYYY-MM-DD>.md` with:
   - Run window (start/end UTC).
   - Aggregate p95 / p99 / error-rate per endpoint.
   - PASS / FAIL against each SLA threshold.
   - Link to the Grafana snapshot.
   - Concurrent ingest activity during the run (proof we
     weren't load-testing an idle stack).

Task #77 is then "ship the proof report from the latest passing
mixed-realistic run." Cadence: monthly, plus pre-launch.

## Where this runs

- **Local dev:** `make test-load` — operator-driven against
  whatever target is in `K6_TARGET` env var. Refuses to run if
  `K6_TARGET` mentions production.
- **Staging:** scheduled via GitHub Actions (or self-hosted
  runner) on a weekly cadence — a regression alarm if p95
  drifts.
- **Pre-launch (manual):** the canonical mixed-realistic run
  against staging that produces the SLA proof report.

## Tooling decisions

### Where the auth comes from

API key: minted once, stored in vault (`RATESENGINE_LOAD_API_KEY`).
Operator exports it before running k6. Never committed.

### How we model wallet-shaped clients

The default `http_req_duration` distribution k6 produces is
*from the load generator's perspective*. For wallet-shaped
clients on real internet, add a network-RTT bias. Either:

- Run k6 from a geographically-realistic location (e.g.
  separate Hetzner box in a different region).
- Or accept "synthetic latency in ideal conditions" with the
  caveat documented in the proof report.

Recommendation: separate-region runner. Not a launch blocker
either way.

### When to run from k6 cloud vs self-hosted

Self-hosted is the default — single binary, runs anywhere.
k6 Cloud is a paid escalation if the proof needs >5,000 concurrent
VUs (unlikely; Freighter's expected load profile fits in
self-hosted territory).

## Edge cases / gotchas

1. **k6 connection pool defaults are fine but worth pinning.**
   k6 reuses HTTP connections per VU; default pool is generous.
   Set `discardResponseBodies: true` for scenarios where we
   don't actually parse the response (just measure latency) —
   reduces VU memory.

2. **TLS handshake noise in p95.** First request from each VU
   includes the TLS handshake. With ramping VU counts the
   handshake-tail can pollute p95 readings. Solution:
   `http.batch` + warmup pre-stage that hits `/v1/healthz`
   before the measured stage starts.

3. **Redis hot-path scenario must not run cold.** If the cache
   hasn't warmed, every request becomes a Timescale full query
   and the test measures the wrong thing. Pre-warmup helper:
   `scenarios/lib/warmup.js` — hits each pair's price endpoint
   once before the measured scenario starts.

4. **CAGG-path scenario is bucket-aligned.** `/v1/vwap` for the
   *current* in-progress bucket returns last-closed-bucket data
   per ADR-0015. So the latency profile flips at the bucket
   boundary (refresh tick fires). Aim measurement windows to
   straddle a bucket boundary to catch that pathology.

5. **SSE scenario clients linger.** With 1 % of traffic on
   `/v1/observations/stream`, even at 500 rps that's 5 new
   long-lived connections per second. After 10 min the test
   has 3000 lingering connections — exercises the streaming
   hub's hub-buffer + last-event-id. Document the ceiling:
   k6's `executor: 'constant-vus'` for streaming so the count
   is fixed, not unbounded.

6. **Spike scenario can wedge alerting.** A 10× spike will
   trip `ratesengine_api_latency_p95_high` legitimately. The
   load test must announce itself to alerting (silence + label)
   so on-call doesn't get paged for the planned spike. Pre-run
   step: post a `silence` to AlertManager via API for the run
   window; remove on completion.

## Effort breakdown

| Step | Estimate |
|---|---|
| `test/load/README.md` (operator-facing) | 2 h |
| `lib/env.js`, `lib/pairs.js`, `lib/thresholds.js` (shared helpers) | 3 h |
| `01-price-hot-path.js` (template scenario) | 2 h |
| `02-vwap-twap.js`, `03-history.js`, `04-batch.js` (similar shape) | 4 h (1.5h each) |
| `05-streaming.js` (different shape — long-lived) | 3 h |
| `06-mixed-realistic.js` (the canonical proof scenario) | 4 h |
| `99-spike.js` + alertmanager-silence integration | 3 h |
| `Makefile` target (`test-load`) + production-target guard | 1 h |
| `docker-compose.k6.yaml` (staging-target compose) | 2 h |
| Grafana dashboard skeleton for k6's prom output | 3 h |
| First end-to-end run + flake-fix iteration | 4 h |
| `docs/operations/sla-proof-<YYYY-MM-DD>.md` template + first proof run | 2 h |
| CHANGELOG + coverage matrix #74/#77 close | 1 h |
| **Total** | **~34 h, ~4 days** |

The matrix's "~1 week" estimate matches if you round up for
flake-tail. **Wave-able**:

- Wave 1 (~2 days): scaffold + scenarios 01/02 + Makefile +
  README → unblocks ad-hoc operator runs.
- Wave 2 (~1.5 days): scenarios 03/04/05/06 → covers the
  mixed-realistic proof.
- Wave 3 (~half-day): spike + alertmanager-silence integration.
- Wave 4 (~half-day): GitHub Actions weekly schedule.

Wave 2 lands the SLA proof scenario; #77 closes after the first
green mixed-realistic run.

## Implementation PR shape (suggested)

Multi-PR, since waves can land independently:

**PR 1 (Wave 1)** — scaffold + 2 scenarios:
1. `feat(test-load): scaffold + lib helpers + README`
2. `feat(test-load): 01-price-hot-path scenario`
3. `feat(test-load): 02-vwap-twap scenario`
4. `feat(make): test-load target + production-target guard`

**PR 2 (Wave 2)** — proof scenarios:
5. `feat(test-load): 03-history + 04-batch + 05-streaming`
6. `feat(test-load): 06-mixed-realistic + threshold report shape`
7. `docs: sla-proof-<date>.md template`

**PR 3 (Wave 3)** — spike + AlertManager:
8. `feat(test-load): 99-spike with alertmanager-silence pre/post hooks`

**PR 4 (Wave 4)** — schedule:
9. `ops(ci): weekly k6 schedule on GitHub Actions self-hosted runner`

Each wave-PR is small enough for clean review + a single CI run.

## Open questions for the implementer

1. **GitHub-hosted runner vs self-hosted for the weekly?**
   GitHub-hosted has 7-min step cap on free tier — tight for a
   10-min mixed-realistic scenario. Self-hosted (one of our
   ops boxes) sidesteps the cap and gets us closer-to-real RTT.
   Recommend: self-hosted. Document the exact box +
   maintenance burden.

2. **Should the spike scenario be on the weekly schedule?** No
   — too noisy for routine. Schedule mixed-realistic weekly;
   spike + 24h-soak only on operator-triggered runs.

3. **Do we expose the k6 scenarios to customers as "you can
   reproduce our SLA claims"?** Open-source-friendly move; would
   ship to `pkg/loadtest` + a `scripts/run-sla-proof` wrapper.
   Defer until the public-flip (Task #78).

4. **VU count vs RPS as the rate-control knob?** k6 supports
   both — `ramping-arrival-rate` (RPS-controlled, recommended)
   vs `ramping-vus` (VU-count-controlled). RPS-controlled is
   more like real traffic. Standardise on `ramping-arrival-rate`
   in `lib/thresholds.js` so every scenario is the same.

5. **Do we need a separate "sustained" scenario at lower load
   over a longer window for the 99.9 % SLO?** Mixed-realistic
   at 10 min is fine for p95; 99.9 % availability is harder to
   prove in 10 min (need ~10,000 requests for one error to be a
   single-digit percentage of the budget). Lower-RPS, longer-
   window scenario at 50 rps × 1 h gets to 180,000 requests —
   ~180 errors at 0.1 % budget. Consider scenario `07-soak.js`
   in Wave 2.5 if the mixed-realistic numbers are noisy.
