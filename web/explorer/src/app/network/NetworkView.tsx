'use client';

import { useState } from 'react';
import dynamic from 'next/dynamic';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { type Envelope } from '../explorer-shared';

const LineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-[320px]" /> },
);

interface NetworkStats {
  volume_24h_usd?: string | null;
  markets_count_24h: number;
  assets_indexed: number;
  latest_ledger: number;
  exchange_sources: number;
  total_sources: number;
}

interface ThroughputBucket {
  day: string;
  ledgers: number;
  txs: number;
  ops: number;
  events: number;
}

interface ThroughputResp {
  window_days: number;
  buckets: ThroughputBucket[];
}

type Metric = 'ops' | 'txs' | 'events' | 'ledgers';
const METRICS: { key: Metric; label: string }[] = [
  { key: 'ops', label: 'Operations' },
  { key: 'txs', label: 'Transactions' },
  { key: 'events', label: 'Contract events' },
  { key: 'ledgers', label: 'Ledgers' },
];
const WINDOWS = [30, 90, 365];

export function NetworkView() {
  const [metric, setMetric] = useState<Metric>('ops');
  const [windowDays, setWindowDays] = useState(30);

  const statsQ = useQuery<NetworkStats>({
    queryKey: ['/v1/network/stats'],
    queryFn: async () => (await apiGet<Envelope<NetworkStats>>('/v1/network/stats', {})).data,
    staleTime: 30_000,
  });

  const tpQ = useQuery<ThroughputResp>({
    queryKey: ['/v1/network/throughput', windowDays],
    queryFn: async () =>
      (await apiGet<Envelope<ThroughputResp>>('/v1/network/throughput', { window_days: windowDays })).data,
    staleTime: 60_000,
  });

  const buckets = tpQ.data?.buckets ?? [];
  const points = buckets.map((b) => ({
    time: Math.floor(Date.parse(`${b.day}T00:00:00Z`) / 1000),
    value: b[metric],
  }));
  const total = buckets.reduce((s, b) => s + b[metric], 0);

  const s = statsQ.data;

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-1">
        <p className="text-xs uppercase tracking-wider text-ink-muted">Explorer</p>
        <h1 className="text-2xl font-semibold tracking-tight text-ink">Network</h1>
        <p className="max-w-2xl text-sm text-ink-muted">
          Stellar network throughput over time and the current at-a-glance
          snapshot, straight from the certified lake.
        </p>
      </header>

      <Panel title="Now" source={asExample('/v1/network/stats', {})}>
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
          <Stat label="Latest ledger" value={s ? `#${s.latest_ledger.toLocaleString()}` : '—'} />
          <Stat label="24h volume" value={s?.volume_24h_usd ? `$${formatCompact(Number(s.volume_24h_usd))}` : '—'} />
          <Stat label="Markets (24h)" value={s ? formatCompact(s.markets_count_24h) : '—'} />
          <Stat label="Assets indexed" value={s ? formatCompact(s.assets_indexed) : '—'} />
          <Stat label="Exchange sources" value={s ? String(s.exchange_sources) : '—'} />
          <Stat label="Total sources" value={s ? String(s.total_sources) : '—'} />
        </div>
      </Panel>

      <Panel
        title="Throughput"
        source={asExample('/v1/network/throughput', { window_days: windowDays })}
        bodyClassName="space-y-4"
      >
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex gap-1">
            {METRICS.map((m) => (
              <button
                key={m.key}
                onClick={() => setMetric(m.key)}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  metric === m.key
                    ? 'bg-brand-600 text-white'
                    : 'border border-line text-ink-body hover:border-brand-500'
                }`}
              >
                {m.label}
              </button>
            ))}
          </div>
          <div className="ml-auto flex gap-1">
            {WINDOWS.map((d) => (
              <button
                key={d}
                onClick={() => setWindowDays(d)}
                className={`rounded-md px-2.5 py-1 text-xs ${
                  windowDays === d
                    ? 'bg-surface-strong text-ink'
                    : 'border border-line text-ink-body hover:border-brand-500'
                }`}
              >
                {d}d
              </button>
            ))}
          </div>
        </div>

        {tpQ.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
        {tpQ.isError && <p className="text-sm text-ink-muted">Throughput is unavailable right now.</p>}
        {tpQ.data && points.length === 0 && (
          <p className="text-sm text-ink-muted">No ledgers in this window yet.</p>
        )}
        {points.length > 0 && (
          <>
            <p className="text-sm text-ink-body">
              <span className="font-mono tabular-nums">{formatCompact(total)}</span>{' '}
              {METRICS.find((m) => m.key === metric)?.label.toLowerCase()} over the last {windowDays} days
            </p>
            <LineChart
              data={points}
              height={320}
              positive
              ariaLabel={`Daily ${metric} on the Stellar network over the last ${windowDays} days`}
            />
          </>
        )}
      </Panel>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-[11px] uppercase tracking-wider text-ink-muted">{label}</div>
      <div className="mt-0.5 font-mono text-sm tabular-nums text-ink">{value}</div>
    </div>
  );
}
