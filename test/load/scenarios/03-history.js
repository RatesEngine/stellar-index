// Scenario 03 — /v1/history + /v1/history/since-inception.
//
// What it stresses: the windowed history endpoint (CAGG-served,
// expected p95 < 200 ms) and the heavier since-inception query
// (CAGG-served per PR #195, expected p95 < 1 s).
//
// 80 % of iterations hit the windowed path (real wallet usage);
// 20 % hit since-inception (analytics dashboards). Pass criteria
// are split by endpoint tag so a since-inception regression shows
// up independently of windowed regressions.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla, rampingArrivalRate } from './lib/thresholds.js';
import { tlsWarmup } from './lib/warmup.js';

const WINDOWS = ['1h', '6h', '24h', '7d', '30d'];
const RESOLUTIONS = ['1m', '5m', '1h', '1d'];

export const options = {
  scenarios: {
    history: rampingArrivalRate([
      { target: 30, duration: '30s' },
      { target: 60, duration: '2m'  },
      { target: 60, duration: '5m'  },
      { target: 0,  duration: '30s' },
    ], 30),
  },
  thresholds: sla.history,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
}

export default function () {
  const pair = pickWeighted();
  const sinceInception = Math.random() < 0.2;

  if (sinceInception) {
    const r = http.get(
      `${baseUrl}/history/since-inception?asset=${pair.asset}&quote=${pair.quote}`,
      { headers, tags: { endpoint: 'since-inception' } },
    );
    check(r, { 'status 200': (r) => r.status === 200 });
  } else {
    const window = WINDOWS[Math.floor(Math.random() * WINDOWS.length)];
    const resolution = RESOLUTIONS[Math.floor(Math.random() * RESOLUTIONS.length)];
    const r = http.get(
      `${baseUrl}/history?asset=${pair.asset}&quote=${pair.quote}&window=${window}&resolution=${resolution}`,
      { headers, tags: { endpoint: 'history' } },
    );
    check(r, { 'status 200': (r) => r.status === 200 });
  }
  sleep(0.15);
}
