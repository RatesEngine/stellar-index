'use client';

import { useState } from 'react';

import { Panel } from '@/components/reveal';
import { CandleChart } from '@/components/charts/CandleChart';
import { asExample } from '@/api/client';
import { chartSeedFor } from '@/lib/chart-seed';

type Timeframe = '24h' | '7d' | '30d' | '1y';
type Granularity = '1m' | '15m' | '1h' | '4h' | '1d';

const TIMEFRAMES: { key: Timeframe; label: string; bucket: number; count: number }[] = [
  { key: '24h', label: '24h', bucket: 60 * 10, count: 144 },
  { key: '7d', label: '7d', bucket: 60 * 60, count: 168 },
  { key: '30d', label: '30d', bucket: 60 * 60 * 4, count: 180 },
  { key: '1y', label: '1y', bucket: 60 * 60 * 24, count: 365 },
];

const GRANULARITIES: { key: Granularity; label: string }[] = [
  { key: '1m', label: '1m' },
  { key: '15m', label: '15m' },
  { key: '1h', label: '1h' },
  { key: '4h', label: '4h' },
  { key: '1d', label: '1d' },
];

/**
 * Chart tab content for /coins/[slug]?tab=chart.
 *
 * v0 renders deterministic seed OHLC bars derived from the slug.
 * API hookup lands once /v1/chart returns OHLC bar shape (today
 * it returns VWAP points). Replace `chartSeedFor(...)` with
 * `useQuery({ url: \`/v1/chart\` })` when the endpoint reshapes.
 */
export function ChartPanel({
  slug,
  startPrice,
}: {
  slug: string;
  startPrice: number;
}) {
  const [timeframe, setTimeframe] = useState<Timeframe>('24h');
  const [granularity, setGranularity] = useState<Granularity>('1h');

  const tf = TIMEFRAMES.find((t) => t.key === timeframe)!;
  const data = chartSeedFor(`${slug}:${timeframe}`, startPrice, tf.count, tf.bucket);

  return (
    <div className="space-y-4">
      <Panel
        title="Price chart"
        hint={`${timeframe} · ${granularity}`}
        source={asExample('/v1/chart', {
          asset: slug,
          quote: 'fiat:USD',
          timeframe,
          granularity,
          price_type: 'vwap',
        })}
      >
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <Picker
            label="Timeframe"
            options={TIMEFRAMES.map((t) => ({ key: t.key, label: t.label }))}
            value={timeframe}
            onChange={(v) => setTimeframe(v as Timeframe)}
          />
          <Picker
            label="Granularity"
            options={GRANULARITIES}
            value={granularity}
            onChange={(v) => setGranularity(v as Granularity)}
          />
        </div>
        <CandleChart data={data} height={420} />
      </Panel>
      <p className="text-xs text-slate-500">
        v0 of the chart renders deterministic seed OHLC bars derived
        from the slug. API hookup lands when{' '}
        <code className="font-mono">/v1/chart</code> reshapes to bars
        (data-inventory §10.1) — same component, swap{' '}
        <code className="font-mono">chartSeedFor(...)</code> for{' '}
        <code className="font-mono">useQuery(...)</code>.
      </p>
    </div>
  );
}

function Picker<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { key: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <span className="text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </span>
      <div className="inline-flex overflow-hidden rounded-md border border-slate-200 dark:border-slate-700">
        {options.map((opt) => (
          <button
            key={opt.key}
            type="button"
            onClick={() => onChange(opt.key)}
            className={`px-2 py-1 text-xs ${
              opt.key === value
                ? 'bg-brand-500 text-white'
                : 'bg-white text-slate-600 hover:bg-slate-50 dark:bg-slate-900 dark:text-slate-300 dark:hover:bg-slate-800'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  );
}
