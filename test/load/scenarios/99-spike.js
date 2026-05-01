// Scenario 99 — 10× spike absorption.
//
// What it stresses: the API's recovery path after a brief 10×
// burst. Routes through the same endpoints as 06-mixed-realistic
// but with a much steeper arrival curve.
//
// Pass criteria:
//   - error rate stays under 0.5 % through the spike (latency is
//     not asserted mid-spike — that's the explicit hand-wave).
//   - recovery to baseline p95 within 2 min of spike end.
//
// AlertManager silence: the spike WILL legitimately trip
// `APIHighLatencyP95`. Without a silence, on-call gets paged for
// a planned spike (design note §6). setup() posts a silence
// covering the run window; teardown() removes it so a real
// post-run regression still pages.
//
// Pre-flight requirement: export ALERTMANAGER_URL pointing at the
// staging AlertManager. If unset, the silence is skipped — k6
// proceeds but operator must manually silence on-call before the
// spike to avoid waking somebody.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla } from './lib/thresholds.js';
import { silenceForRun, clearSilence } from './lib/alertmanager.js';
import { tlsWarmup, warmPriceCache } from './lib/warmup.js';

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 1000,
      stages: [
        { target: 100,  duration: '30s' },     // baseline establish
        { target: 1000, duration: '15s' },     // spike up to 10×
        { target: 1000, duration: '30s' },     // hold the spike
        { target: 100,  duration: '15s' },     // ramp down
        { target: 100,  duration: '2m' },      // observe recovery
        { target: 0,    duration: '15s' },
      ],
    },
  },
  thresholds: sla.spike,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
  warmPriceCache();
  // Window: 30s baseline + 1m spike + 2m recovery + buffer = 5m,
  // round up to 10m so a slow teardown still falls inside silence.
  const silenceID = silenceForRun(10, 'planned k6 99-spike scenario');
  return { silenceID };
}

export function teardown(data) {
  clearSilence(data.silenceID);
}

export default function () {
  const pair = pickWeighted();
  const r = http.get(
    `${baseUrl}/price?asset=${pair.asset}&quote=${pair.quote}`,
    { headers, tags: { endpoint: 'price' } },
  );
  check(r, {
    'no 5xx': (r) => r.status < 500,
  });
  sleep(0.01);
}
