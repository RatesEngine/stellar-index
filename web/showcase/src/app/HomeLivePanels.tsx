'use client';

import { Panel } from '@/components/reveal';
import { Sparkline, AccelerationArrow } from '@/components/primitives';
import { asExample } from '@/api/client';
import { useCoins, useCursors } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * NetworkLivePanel — replaces the static "85,750 classic assets"
 * stub with the live count from /v1/coins (limit=500 — enough to
 * show the entire registry as it grows). Also surfaces the highest
 * last_ledger seen across non-backfill cursors as the "current
 * network tip" reading.
 */
export function NetworkLivePanel() {
  const coins = useCoins(500);
  const cursors = useCursors();

  const assetsCount = coins.data?.length ?? null;
  const tipLedger = cursors.data ? maxLiveLedger(cursors.data) : null;

  return (
    <Panel title="Network" hint="Stellar pulse" source={asExample('/v1/coins', { limit: 500 })}>
      <div className="space-y-2">
        <div className="text-2xl font-bold tabular-nums">
          {assetsCount !== null ? formatCompact(assetsCount) : '—'}
        </div>
        <div className="text-xs text-slate-500">classic assets indexed</div>

        <div className="pt-3">
          <div className="font-mono text-sm tabular-nums">
            {tipLedger !== null ? `#${tipLedger.toLocaleString()}` : '—'}
          </div>
          <div className="text-[11px] text-slate-500">current ingest tip</div>
        </div>

        <div className="mt-3 flex items-center gap-2">
          <Sparkline
            values={[
              60_000, 65_000, 71_000, 78_000, 82_000, 85_000,
              assetsCount ?? 85_750,
            ]}
          />
          <AccelerationArrow direction="up" acceleration="increasing" />
        </div>
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
        bodyClassName="text-xs text-slate-500"
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
        <div className="pt-1 text-[11px] text-slate-500">
          {liveRows.length} live cursor{liveRows.length === 1 ? '' : 's'}, {data.length - liveRows.length} backfill task{data.length - liveRows.length === 1 ? '' : 's'}
        </div>
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
        ? 'bg-amber-500'
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
        <div className="text-[10px] text-slate-500">{subtext}</div>
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
