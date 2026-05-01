// Shared SLA thresholds. Every scenario imports from here so the
// pass/fail bar is defined once and matches:
//   - Freighter RFP §SLA targets: p95 ≤ 200 ms
//   - ADR-0009 multi-window SLO: 99.9 % availability
//
// Endpoint tags (`endpoint:price` etc.) are set by each scenario
// via `tags: { endpoint: '...' }` on the http call so per-endpoint
// p95 can be asserted independently.

export const sla = {
  priceHotPath: {
    'http_req_duration{endpoint:price}': ['p(95)<200', 'p(99)<500'],
    'http_req_failed':                   ['rate<0.001'],
  },
  vwapTwap: {
    'http_req_duration{endpoint:vwap}':  ['p(95)<200', 'p(99)<500'],
    'http_req_duration{endpoint:twap}':  ['p(95)<200', 'p(99)<500'],
    'http_req_failed':                   ['rate<0.001'],
  },
  history: {
    'http_req_duration{endpoint:history}':           ['p(95)<200', 'p(99)<500'],
    'http_req_duration{endpoint:since-inception}':   ['p(95)<1000', 'p(99)<2000'],
    'http_req_failed':                               ['rate<0.001'],
  },
  batch: {
    'http_req_duration{endpoint:batch}':  ['p(95)<500', 'p(99)<1000'],
    'http_req_failed':                    ['rate<0.001'],
  },
  streaming: {
    // SSE clients are long-lived; we measure first-event latency
    // via a custom Trend, not http_req_duration.
    'sse_first_event_ms': ['p(99)<1000'],
    'http_req_failed':    ['rate<0.001'],
  },
  mixed: {
    // The canonical proof: weighted mix p95 ≤ 200 ms, 99.9 % success.
    'http_req_duration': ['p(95)<200', 'p(99)<500'],
    'http_req_failed':   ['rate<0.001'],
  },
  spike: {
    // The 10× burst should not error; latency excused mid-spike,
    // recovery asserted out-of-band by the runbook.
    'http_req_failed': ['rate<0.005'],
  },
};

// Common executor shape — RPS-controlled per the design note Q4.
// Scenarios override stages but inherit the executor type.
export function rampingArrivalRate(stages, preAllocatedVUs = 100) {
  return {
    executor: 'ramping-arrival-rate',
    startRate: 50,
    timeUnit: '1s',
    preAllocatedVUs,
    maxVUs: preAllocatedVUs * 4,
    stages,
  };
}
