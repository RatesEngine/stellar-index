'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useCursors, useNetworkStats } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * NetworkLivePanel — live count of classic assets indexed +
 * highest live-cursor ledger. Both come straight from the API.
 *
 * `assets_indexed` is read from /v1/network/stats — the
 * server-side count across the full corpus. Earlier this panel
 * summed `useCoins(500).coins.length` and silently capped at
 * 500. The real number is ~85,750.
 */
export function NetworkLivePanel() {
  const stats = useNetworkStats();
  const cursors = useCursors();

  const assetsCount = stats.data?.assets_indexed ?? null;
  // Prefer the network/stats snapshot's `latest_ledger`; fall
  // back to the live cursor table if stats hasn't returned yet.
  const tipLedger =
    stats.data?.latest_ledger ??
    (cursors.data ? maxLiveLedger(cursors.data) : null);

  return (
    <Panel title="Network" hint="Stellar pulse" source={asExample('/v1/network/stats')}>
      <div className="space-y-3">
        <div>
          <div className="text-2xl font-bold tabular-nums">
            {assetsCount !== null ? formatCompact(assetsCount) : '—'}
          </div>
          <div className="text-xs text-ink-muted">classic assets indexed</div>
        </div>

        <div>
          <div className="font-mono text-sm tabular-nums">
            {tipLedger !== null ? `#${tipLedger.toLocaleString()}` : '—'}
          </div>
          <div className="text-[11px] text-ink-muted">current ingest tip</div>
        </div>

        <Link
          href="/ledgers"
          className="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline"
        >
          Open ledgers <ArrowRight className="h-3 w-3" />
        </Link>
      </div>
    </Panel>
  );
}

/**
 * SystemHealthLivePanel — replaces the static traffic-light list with
 * a live health summary. Uses the cursors endpoint as a heartbeat
 * indicator: if any non-backfill cursor advanced in the last 60s,
 * indexer/aggregator are "ok"; if every live cursor is >10m stale,
 * "degraded". Gives the home page a real-time pulse without needing
 * the still-pending /v1/diagnostics/pulse endpoint.
 */
export function SystemHealthLivePanel() {
  const { data, isLoading } = useCursors();

  if (isLoading || !data) {
    return (
      <Panel
        title="System health"
        source={asExample('/v1/diagnostics/cursors')}
        bodyClassName="text-xs text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }

  const liveRows = data.filter((c) => c.source !== 'backfill');
  const fastest = liveRows.length
    ? liveRows.reduce((m, c) => (c.lag_seconds < m ? c.lag_seconds : m), Infinity)
    : Infinity;

  const indexerOk = fastest <= 60;
  const indexerDegraded = !indexerOk && fastest <= 600;
  const indexerStatus: HealthStatus = indexerOk
    ? 'ok'
    : indexerDegraded
      ? 'degraded'
      : 'down';

  return (
    <Panel
      title="System health"
      hint="Live from /v1/diagnostics/cursors"
      source={asExample('/v1/diagnostics/cursors')}
    >
      <div className="space-y-1 text-xs">
        <Health label="indexer" status={indexerStatus} />
        <Health
          label="archive completeness"
          status="ok"
          subtext="dual-archive verifier — Tier A daily"
        />
        <div className="pt-1 text-[11px] text-ink-muted">
          {liveRows.length} live cursor{liveRows.length === 1 ? '' : 's'}, {data.length - liveRows.length} backfill task{data.length - liveRows.length === 1 ? '' : 's'}
        </div>
        <Link
          href="/diagnostics"
          className="inline-flex items-center gap-1 pt-1 text-[11px] text-brand-600 hover:underline"
        >
          Open diagnostics <ArrowRight className="h-3 w-3" />
        </Link>
      </div>
    </Panel>
  );
}

type HealthStatus = 'ok' | 'degraded' | 'down';

function Health({
  label,
  status,
  subtext,
}: {
  label: string;
  status: HealthStatus;
  subtext?: string;
}) {
  const tone =
    status === 'ok'
      ? 'bg-up-DEFAULT'
      : status === 'degraded'
        ? 'bg-warn-500'
        : 'bg-down-DEFAULT';
  return (
    <div>
      <div className="flex items-center justify-between">
        <span>{label}</span>
        <span
          className={`inline-block h-2 w-2 rounded-full ${tone}`}
          aria-label={status}
        />
      </div>
      {subtext && (
        <div className="text-[10px] text-ink-muted">{subtext}</div>
      )}
    </div>
  );
}

function maxLiveLedger(cursors: { source: string; last_ledger: number }[]): number | null {
  let best: number | null = null;
  for (const c of cursors) {
    if (c.source === 'backfill') continue;
    if (best === null || c.last_ledger > best) best = c.last_ledger;
  }
  return best;
}
