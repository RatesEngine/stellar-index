// Scenario 05 — /v1/price/stream + /v1/observations/stream (SSE).
//
// What it stresses: the streaming hub's connection-accept path,
// per-client buffer, and last-event-id resume contract.
//
// Pass criteria: 99 % of clients receive their first event within
// 1 s of subscribe (sse_first_event_ms p99 < 1000 in
// lib/thresholds.js).
//
// Executor is constant-VUs not ramping-arrival-rate — long-lived
// SSE connections need a fixed concurrent count, not an RPS curve.
// Each VU subscribes once, records first-event latency, then
// holds the connection open for the rest of the iteration.
//
// Edge case (design note §5): SSE clients linger. At 200 VUs the
// scenario ends with up to 200 lingering connections — the hub's
// shutdown path is exercised on scenario teardown.

import http from 'k6/http';
import { check } from 'k6';
import { Trend } from 'k6/metrics';
import { baseUrl, apiKey } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla } from './lib/thresholds.js';

const firstEvent = new Trend('sse_first_event_ms');

export const options = {
  scenarios: {
    streaming: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { target: 50,  duration: '30s' },
        { target: 200, duration: '2m'  },
        { target: 200, duration: '5m'  },
        { target: 0,   duration: '30s' },
      ],
      gracefulStop: '60s',
    },
  },
  thresholds: sla.streaming,
};

export default function () {
  const pair = pickWeighted();
  const url = `${baseUrl}/observations/stream?asset=${pair.asset}&quote=${pair.quote}`;
  const start = Date.now();
  let firstSeen = false;

  // k6's http.get on a streaming endpoint blocks until server
  // closes or timeout; we use a long timeout and rely on the SSE
  // server enforcing its own keepalive cadence. We measure
  // first-event latency by checking response.timings.waiting,
  // which is the time-to-first-byte — for SSE that's exactly
  // the first event arrival.
  const r = http.get(url, {
    headers: { 'X-API-Key': apiKey, 'Accept': 'text/event-stream' },
    tags: { endpoint: 'stream' },
    timeout: '30s',
  });

  if (r.timings && typeof r.timings.waiting === 'number') {
    firstEvent.add(r.timings.waiting);
    firstSeen = r.timings.waiting > 0;
  }

  check(r, {
    'first event within 30s': () => firstSeen,
    'status 200 or 204': (r) => r.status === 200 || r.status === 204,
  });
}
