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
  { ssr: false, loading: () => <div className="h-[260px]" /> },
);

interface Bucket {
  day: string;
  ledgers: number;
  txs: number;
  ops: number;
  events: number;
}
interface ThroughputResp {
  window_days: number;
  buckets: Bucket[];
}

type Metric = 'ops' | 'events' | 'txs' | 'ledgers';
const METRICS: { key: Metric; label: string }[] = [
  { key: 'ops', label: 'Operations' },
  { key: 'events', label: 'Contract events' },
  { key: 'txs', label: 'Transactions' },
  { key: 'ledgers', label: 'Ledgers' },
];

/**
 * IngestThroughputChart — the indexer's daily output (what the pipeline
 * decoded + persisted per day) over the last 30 days, as a
 * lightweight-charts line. A health signal: a sustained dip means the
 * pipeline fell behind. Backed by /v1/network/throughput.
 */
export function IngestThroughputChart() {
  const [metric, setMetric] = useState<Metric>('ops');
  const q = useQuery<ThroughputResp>({
    queryKey: ['/v1/network/throughput', 30, 'diagnostics'],
    queryFn: async () =>
      (await apiGet<Envelope<ThroughputResp>>('/v1/network/throughput', { window_days: 30 })).data,
    staleTime: 60_000,
  });

  const buckets = q.data?.buckets ?? [];
  const points = buckets.map((b) => ({
    time: Math.floor(Date.parse(`${b.day}T00:00:00Z`) / 1000),
    value: b[metric],
  }));
  const total = buckets.reduce((s, b) => s + b[metric], 0);

  return (
    <Panel
      title="Ingest throughput — last 30 days"
      hint="Daily decoded output from the certified lake; a sustained dip flags the pipeline falling behind."
      source={asExample('/v1/network/throughput', { window_days: 30 })}
      bodyClassName="space-y-3"
    >
      <div className="flex flex-wrap items-center gap-1 text-xs">
        {METRICS.map((m) => (
          <button
            key={m.key}
            type="button"
            onClick={() => setMetric(m.key)}
            aria-pressed={metric === m.key}
            className={`rounded-md px-2.5 py-1 ${
              metric === m.key
                ? 'bg-brand-600 text-white'
                : 'border border-line text-ink-body hover:border-brand-500'
            }`}
          >
            {m.label}
          </button>
        ))}
        {points.length > 0 && (
          <span className="ml-auto font-mono text-[11px] tabular-nums text-ink-muted">
            {formatCompact(total)} total
          </span>
        )}
      </div>
      {q.isLoading && <div className="h-[260px]" />}
      {q.isError && <p className="text-sm text-ink-muted">Throughput is unavailable right now.</p>}
      {q.data && points.length === 0 && (
        <p className="text-sm text-ink-muted">No throughput in this window yet.</p>
      )}
      {points.length > 0 && (
        <LineChart data={points} height={260} positive ariaLabel={`Daily ${metric} ingested over the last 30 days`} />
      )}
    </Panel>
  );
}
