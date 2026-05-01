// Scenario 04 — /v1/price/batch (bulk fan-out).
//
// What it stresses: the batch endpoint's per-asset Redis fan-out,
// the ADR-0018 cap (1000 assets max per batch), and the JSON
// serialisation overhead at high response sizes.
//
// Pass criteria: p95 < 500 ms @ batch-size 100, 50 rps for 5 min.
// The 500 ms ceiling is per design note §Scenarios broken out —
// batch is the one endpoint where p95 < 200 ms isn't asserted
// because legitimate large-batch responses dominate the network
// component of latency.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { baseUrl, headers } from './lib/env.js';
import { PAIRS, pickN } from './lib/pairs.js';
import { sla, rampingArrivalRate } from './lib/thresholds.js';
import { tlsWarmup } from './lib/warmup.js';

const BATCH_SIZE = 100;

export const options = {
  scenarios: {
    batch: rampingArrivalRate([
      { target: 25, duration: '30s' },
      { target: 50, duration: '2m'  },
      { target: 50, duration: '5m'  },
      { target: 0,  duration: '30s' },
    ], 25),
  },
  thresholds: sla.batch,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
  if (PAIRS.length === 0) {
    throw new Error('PAIRS fixture is empty');
  }
}

export default function () {
  // Re-pick with replacement to fill BATCH_SIZE; assets will repeat
  // across iterations but a single batch contains distinct assets.
  const picks = pickN(Math.min(BATCH_SIZE, PAIRS.length));
  const fill = [];
  while (fill.length + picks.length < BATCH_SIZE) {
    fill.push(picks[fill.length % picks.length]);
  }
  const all = [...picks, ...fill];

  const body = JSON.stringify({
    pairs: all.map((p) => ({ asset: p.asset, quote: p.quote })),
  });

  const r = http.post(`${baseUrl}/price/batch`, body, {
    headers: { ...headers, 'Content-Type': 'application/json' },
    tags: { endpoint: 'batch' },
  });
  check(r, { 'status 200': (r) => r.status === 200 });
  sleep(0.2);
}
