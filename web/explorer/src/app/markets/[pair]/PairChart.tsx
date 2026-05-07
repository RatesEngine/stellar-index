'use client';

import { useEffect, useState } from 'react';

import { CandleChart } from '@/components/charts/CandleChart';
import { API_BASE_URL } from '@/api/client';

type Timeframe = '24h' | '7d' | '30d' | '1y';
type Granularity = '1m' | '15m' | '1h' | '4h' | '1d';

const TIMEFRAMES: { key: Timeframe; label: string }[] = [
  { key: '24h', label: '24h' },
  { key: '7d', label: '7d' },
  { key: '30d', label: '30d' },
  { key: '1y', label: '1y' },
];

const GRANULARITIES: { key: Granularity; label: string }[] = [
  { key: '1m', label: '1m' },
  { key: '15m', label: '15m' },
  { key: '1h', label: '1h' },
  { key: '4h', label: '4h' },
  { key: '1d', label: '1d' },
];

interface ChartPoint {
  ts?: string;
  t?: string;
  p?: string;
  vwap?: string;
  open?: string;
  high?: string;
  low?: string;
  close?: string;
}

/**
 * PairChart — full timeframe/granularity-controlled candle chart
 * for a (base, quote) pair. Mirrors the /assets/[slug] ChartPanel
 * but with quote fixed to the URL pair (no quote toggle — the
 * pair already nailed it down).
 *
 * Reads from /v1/chart with `asset=<base>&quote=<quote>`. Each
 * point renders as a flat candle until the API switches to bar
 * shape; once it does, the OHLC fields are picked up
 * automatically.
 */
export function PairChart({
  base,
  quote,
  baseLabel,
  quoteLabel,
}: {
  base: string;
  quote: string;
  baseLabel: string;
  quoteLabel: string;
}) {
  const [timeframe, setTimeframe] = useState<Timeframe>('24h');
  const [granularity, setGranularity] = useState<Granularity>('1h');
  const [data, setData] = useState<
    { time: number; open: number; high: number; low: number; close: number }[]
  >([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    const url = `${API_BASE_URL}/v1/chart?asset=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&timeframe=${timeframe}&granularity=${granularity}&price_type=vwap`;
    fetch(url, { signal: controller.signal })
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.json() as Promise<{
          data: ChartPoint[] | { points?: ChartPoint[] };
        }>;
      })
      .then((env) => {
        const points = Array.isArray(env.data)
          ? env.data
          : (env.data?.points ?? []);
        const bars = points.map((p) => {
          const t = p.t ?? p.ts ?? '';
          const v = Number(p.p ?? p.vwap ?? p.close ?? '0');
          const open = Number(p.open ?? v);
          const high = Number(p.high ?? Math.max(open, v));
          const low = Number(p.low ?? Math.min(open, v));
          const close = Number(p.close ?? v);
          return {
            time: Math.floor(new Date(t).getTime() / 1000),
            open,
            high,
            low,
            close,
          };
        });
        setData(bars);
        setLoading(false);
      })
      .catch((err: Error) => {
        if (err.name === 'AbortError') return;
        setError(err.message);
        setLoading(false);
      });
    return () => controller.abort();
  }, [base, quote, timeframe, granularity]);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <Picker
          label="Timeframe"
          options={TIMEFRAMES}
          value={timeframe}
          onChange={(v) => setTimeframe(v as Timeframe)}
        />
        <Picker
          label="Granularity"
          options={GRANULARITIES}
          value={granularity}
          onChange={(v) => setGranularity(v as Granularity)}
        />
        <span className="ml-auto text-slate-500">
          {baseLabel} / {quoteLabel}
        </span>
      </div>
      {loading && (
        <div className="flex h-[360px] items-center justify-center text-sm text-slate-500">
          Loading…
        </div>
      )}
      {error && !loading && (
        <div className="flex h-[360px] items-center justify-center text-sm text-red-600 dark:text-red-400">
          {error === 'HTTP 404'
            ? 'No chart data for this pair + window yet'
            : `Chart data unavailable: ${error}`}
        </div>
      )}
      {!loading && !error && data.length === 0 && (
        <div className="flex h-[360px] items-center justify-center text-sm text-slate-500">
          No chart data for this pair + window yet
        </div>
      )}
      {!loading && !error && data.length > 0 && (
        <CandleChart data={data} height={360} />
      )}
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
    <div className="inline-flex items-center gap-1.5 rounded-md border border-slate-200 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900">
      <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
        {label}
      </span>
      <div className="flex gap-0.5">
        {options.map((o) => (
          <button
            key={o.key}
            type="button"
            onClick={() => onChange(o.key)}
            className={`rounded px-1.5 py-0.5 text-[10px] font-mono uppercase tracking-wider ${
              value === o.key
                ? 'bg-brand-600 text-white'
                : 'text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800'
            }`}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}
