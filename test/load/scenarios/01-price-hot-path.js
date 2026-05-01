// Scenario 01 — /v1/price + /v1/price/tip hot path.
//
// What it stresses: Redis cache + handler routing.
// Pass criteria: p95 < 200 ms @ 500 rps for 5 min sustained.
//
// Per design note §Sample scenario shape. RPS-controlled via
// ramping-arrival-rate; SLA thresholds imported from lib/.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend } from 'k6/metrics';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla, rampingArrivalRate } from './lib/thresholds.js';
import { warmPriceCache, tlsWarmup } from './lib/warmup.js';

const priceLatency = new Trend('price_latency_ms');

export const options = {
  scenarios: {
    price_hot_path: rampingArrivalRate([
      { target: 100, duration: '30s' },
      { target: 500, duration: '2m'  },
      { target: 500, duration: '5m'  },
      { target: 0,   duration: '30s' },
    ]),
  },
  thresholds: sla.priceHotPath,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
  warmPriceCache();
}

export default function () {
  const pair = pickWeighted();
  const useTip = Math.random() < 0.15;
  const path = useTip ? 'price/tip' : 'price';
  const r = http.get(
    `${baseUrl}/${path}?asset=${pair.asset}&quote=${pair.quote}`,
    { headers, tags: { endpoint: 'price' } },
  );
  priceLatency.add(r.timings.duration);
  check(r, {
    'status 200': (r) => r.status === 200,
  });
  sleep(0.05);
}
