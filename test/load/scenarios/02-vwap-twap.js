// Scenario 02 — /v1/vwap + /v1/twap (CAGG read path).
//
// What it stresses: Postgres continuous-aggregate path, the
// last-closed-bucket contract (ADR-0015), and the cache-front for
// CAGG queries.
//
// Pass criteria: p95 < 200 ms @ 100 rps for 5 min sustained per
// endpoint. Lower RPS than the price hot path because the CAGG
// path is heavier and customer traffic-share is ~9 %.
//
// Edge case (design note §4): the in-progress bucket flips at the
// refresh tick. Run for ≥5 min so the measurement window straddles
// at least one bucket boundary and catches refresh-tick latency.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla, rampingArrivalRate } from './lib/thresholds.js';
import { tlsWarmup } from './lib/warmup.js';

const WINDOWS = ['5m', '15m', '1h', '24h'];

export const options = {
  scenarios: {
    vwap_twap: rampingArrivalRate([
      { target: 50,  duration: '30s' },
      { target: 100, duration: '2m'  },
      { target: 100, duration: '5m'  },
      { target: 0,   duration: '30s' },
    ], 50),
  },
  thresholds: sla.vwapTwap,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
}

export default function () {
  const pair = pickWeighted();
  const window = WINDOWS[Math.floor(Math.random() * WINDOWS.length)];
  const useTwap = Math.random() < 0.4;
  const path = useTwap ? 'twap' : 'vwap';
  const r = http.get(
    `${baseUrl}/${path}?asset=${pair.asset}&quote=${pair.quote}&window=${window}`,
    { headers, tags: { endpoint: path } },
  );
  check(r, {
    'status 200': (r) => r.status === 200,
  });
  sleep(0.1);
}
