// Pre-warmup helpers.
//
// The Redis hot-path scenario must not measure cold-cache requests
// (every miss becomes a Timescale full query — measures the wrong
// thing). Each scenario's setup() calls warmPriceCache once so the
// first measured request hits a populated cache.
//
// Also hits /v1/healthz once per VU on iteration 0 to absorb the
// TLS handshake into a non-measured request — keeps p95 honest.

import http from 'k6/http';
import { baseUrl, headers } from './env.js';
import { PAIRS } from './pairs.js';

export function warmPriceCache() {
  for (const p of PAIRS) {
    http.get(`${baseUrl}/price?asset=${p.asset}&quote=${p.quote}`, {
      headers,
      tags: { endpoint: 'warmup' },
    });
  }
}

export function tlsWarmup() {
  http.get(`${baseUrl}/healthz`, {
    headers,
    tags: { endpoint: 'warmup' },
  });
}
