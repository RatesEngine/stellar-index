import type { Metadata } from 'next';

import { StatusDashboard } from './StatusDashboard';

export const metadata: Metadata = {
  title: 'Status — Rates Engine',
  description:
    'Live system health for the Rates Engine. Per-region heartbeats, p95/p99 latency, freshness, alert state.',
};

/**
 * /status — public system-health page. Live (client-rendered)
 * dashboard pulling from /v1/healthz, /v1/version, /v1/sources,
 * /v1/diagnostics/cursors, /v1/price for the canonical XLM/USD
 * pair. Refreshes every 10s.
 *
 * Today's surface: per-service liveness (API, indexer, aggregator,
 * storage), per-source ingest cursor lag, headline-pair freshness,
 * version + region. Future: Prometheus-fed p99 latency strips,
 * Alertmanager-fed active-incident feed (both via a new
 * /v1/status aggregator endpoint that proxies the metrics
 * stack — see docs/operations/status-page-plan.md once the
 * plan lands).
 *
 * Distinct from /diagnostics, which is operator-facing and
 * lower-level. /status is customer-facing: green panels, plain-
 * English summaries, the answer to "is the API working RIGHT NOW?"
 */
export default function StatusPage() {
  return (
    <div className="mx-auto max-w-6xl px-4 py-10 sm:px-6 sm:py-12">
      <StatusDashboard />
    </div>
  );
}
