// 00-acceptance-contract-rate.js — THE acceptance-criteria evidence run.
//
// Drives exactly the contractual load from the RFPs (ctx-proposal
// §Milestones + Freighter SLAs): ≥1000 requests/min sustained, asserting
// p95 ≤ 200 ms, p99 ≤ 500 ms, error rate < 0.1 %. This is deliberately
// NOT a stress test — 06-mixed-realistic (300 req/s) probes the capacity
// cliff; this scenario proves the contract on the production deployment.
//
// Run it against a QUIESCENT host (no completeness audit / re-derives in
// flight) — the evidence should reflect serving capacity, not batch-job
// contention. 30 minutes at ~17 req/s ≈ 30,600 requests.
//
//   docker run --rm \
//     -e K6_TARGET=https://api.<domain>/v1 \
//     -e STELLARINDEX_LOAD_API_KEY=rek_... \
//     -v $PWD/test/load/scenarios:/scripts \
//     -v $PWD/test/load/reports/<date>:/reports \
//     grafana/k6:1.0.0 run \
//     --summary-export /reports/00-acceptance.json \
//     /scripts/00-acceptance-contract-rate.js

import http from 'k6/http';
import { check } from 'k6';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted, enc } from './lib/pairs.js';

export const options = {
  scenarios: {
    contract: {
      executor: 'constant-arrival-rate',
      rate: 17, // ≈1020 req/min — just over the contractual floor
      timeUnit: '1s',
      duration: '30m',
      preAllocatedVUs: 30,
      maxVUs: 60,
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<200', 'p(99)<500'],
    http_req_failed: ['rate<0.001'],
    checks: ['rate>0.999'],
  },
};

// The Freighter integration's realistic request mix: current price
// dominates, tip + chart + asset detail follow.
export default function () {
  const pair = pickWeighted();
  const roll = Math.random();
  let r;
  if (roll < 0.45) {
    r = http.get(
      `${baseUrl}/price?asset=${enc(pair.asset)}&quote=${enc(pair.quote)}`,
      { headers, tags: { endpoint: 'price' } },
    );
  } else if (roll < 0.7) {
    r = http.get(
      `${baseUrl}/price/tip?asset=${enc(pair.asset)}&quote=${enc(pair.quote)}`,
      { headers, tags: { endpoint: 'price-tip' } },
    );
  } else if (roll < 0.9) {
    r = http.get(
      `${baseUrl}/ohlc?base=${enc(pair.asset)}&quote=${enc(pair.quote)}&interval=1h&limit=168`,
      { headers, tags: { endpoint: 'ohlc-series' } },
    );
  } else {
    r = http.get(`${baseUrl}/assets/${enc(pair.asset)}`, {
      headers,
      tags: { endpoint: 'asset-detail' },
    });
  }
  check(r, { 'status 2xx': (res) => res.status >= 200 && res.status < 300 });
}
