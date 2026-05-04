// Scenario 07 — catalogue browse (showcase hot path).
//
// Models a user browsing the showcase explorer at ratesengine.net:
// they hit /coins → click into /coins/{slug} (which fans out to
// /v1/issuers/{g} + /v1/changes/{type}/{id}) → tab over to
// /markets and /issuers, with /diagnostics polling in the
// background.
//
// Pass criteria:
//   - p95 < 200 ms across {coins, issuers, issuer-detail, cursors}
//   - p95 < 300 ms on /v1/markets (GROUP BY across 14-day window)
//   - p99 < 500 ms on the lookups, < 1000 ms on /v1/markets
//   - error rate < 0.1 %
//
// This scenario is NOT a release gate — it's a regression check
// for the showcase's hot path. Run before any release that
// touches storage queries on classic_assets / issuers / trades.
//
// Traffic shape modelled on real showcase telemetry (estimated
// pre-launch; tune post-launch as real traffic teaches us):
//   30% /v1/coins                    — index page, cached
//   25% /v1/issuers/{g}              — coin detail issuer card
//   20% /v1/markets                  — markets page
//   15% /v1/issuers                  — issuers index
//   10% /v1/diagnostics/cursors      — operator polling

import http from 'k6/http';
import { baseUrl, headers } from './lib/env.js';
import { pickWeighted } from './lib/pairs.js';
import { sla, rampingArrivalRate } from './lib/thresholds.js';
import { tlsWarmup } from './lib/warmup.js';

// USDC issuer — top-of-list on r1, deterministic for the
// load-test environment.
const SAMPLE_ISSUER = 'GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN';

export const options = {
  scenarios: {
    catalogue: rampingArrivalRate(
      [
        { target: 50, duration: '30s' },
        { target: 150, duration: '1m' },
        { target: 150, duration: '5m' },
        { target: 0, duration: '30s' },
      ],
      50,
    ),
  },
  thresholds: sla.catalogue,
  discardResponseBodies: true,
};

export function setup() {
  tlsWarmup();
}

function pickEndpoint() {
  const r = Math.random() * 100;
  if (r < 30) return 'coins';
  if (r < 55) return 'issuer-detail';
  if (r < 75) return 'markets';
  if (r < 90) return 'issuers';
  return 'cursors';
}

export default function () {
  const ep = pickEndpoint();
  let r;

  switch (ep) {
    case 'coins':
      r = http.get(`${baseUrl}/coins?limit=100`, {
        headers,
        tags: { endpoint: 'coins' },
      });
      break;

    case 'issuers':
      r = http.get(`${baseUrl}/issuers?limit=100`, {
        headers,
        tags: { endpoint: 'issuers' },
      });
      break;

    case 'issuer-detail':
      r = http.get(`${baseUrl}/issuers/${SAMPLE_ISSUER}`, {
        headers,
        tags: { endpoint: 'issuer-detail' },
      });
      break;

    case 'markets': {
      // Use the canonical pair set's quote so this exercises a
      // realistic market lookup path; un-tagged because the
      // /v1/markets endpoint is itself the surface under test.
      const _pair = pickWeighted();
      r = http.get(`${baseUrl}/markets?limit=100`, {
        headers,
        tags: { endpoint: 'markets' },
      });
      break;
    }

    case 'cursors':
      r = http.get(`${baseUrl}/diagnostics/cursors`, {
        headers,
        tags: { endpoint: 'cursors' },
      });
      break;
  }

  if (r.status >= 500) {
    console.warn(`5xx from ${ep}: ${r.status}`);
  }
}
