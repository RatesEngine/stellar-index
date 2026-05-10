'use client';

import { useEffect, useState } from 'react';
import dynamic from 'next/dynamic';

import { API_BASE_URL, apiGet, asExample } from '@/api/client';
import { Panel } from '@/components/reveal';

// Lazy-load lightweight-charts (~155 KB) so the rest of the venue
// page renders without paying the bundle tax up-front.
const CandleChart = dynamic(
  () => import('@/components/charts/CandleChart').then((m) => m.CandleChart),
  { ssr: false, loading: () => <div className="h-[360px]" /> },
);

type Timeframe = '1h' | '24h' | '1w' | '1mo' | '1y' | 'all';
type Granularity = '1m' | '15m' | '1h' | '4h' | '1d';

const TIMEFRAMES: { key: Timeframe; label: string }[] = [
  { key: '24h', label: '24h' },
  { key: '1w', label: '7d' },
  { key: '1mo', label: '30d' },
  { key: '1y', label: '1y' },
  { key: 'all', label: 'All' },
];

const GRANULARITIES: { key: Granularity; label: string }[] = [
  { key: '1m', label: '1m' },
  { key: '15m', label: '15m' },
  { key: '1h', label: '1h' },
  { key: '4h', label: '4h' },
  { key: '1d', label: '1d' },
];

interface Market {
  base: string;
  quote: string;
  volume_24h_usd?: string | null;
}

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
 * VenueChart — TradingView-style candle chart for a CEX venue's
 * pairs. Fetches the venue's pair list (sorted by volume), picks
 * the top pair as default, and renders a candle chart with
 * timeframe + granularity controls. A dropdown lets the user
 * switch between any of the venue's pairs.
 *
 * Data flow:
 *  1. /v1/markets?source=<venue> for the pair list
 *  2. /v1/chart?asset=<base>&quote=<quote>&timeframe=…&granularity=… for the candles
 *
 * The CandleChart import is lazy so the venue page TTI stays low
 * even though the lightweight-charts bundle is ~155 KB.
 */
export function VenueChart({ venue }: { venue: string }) {
  const [pairs, setPairs] = useState<Market[]>([]);
  const [selected, setSelected] = useState<{ base: string; quote: string } | null>(null);
  const [pairsLoading, setPairsLoading] = useState(true);
  const [pairsError, setPairsError] = useState<string | null>(null);
  const [timeframe, setTimeframe] = useState<Timeframe>('24h');
  const [granularity, setGranularity] = useState<Granularity>('1h');
  const [data, setData] = useState<
    { time: number; open: number; high: number; low: number; close: number }[]
  >([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Fetch the venue's pair list once on mount; default-select the
  // top-volume pair so the chart renders something useful immediately.
  useEffect(() => {
    let cancelled = false;
    setPairsLoading(true);
    setPairsError(null);
    apiGet<{ data: Market[] }>('/v1/markets', {
      source: venue,
      order_by: 'volume_24h_usd_desc',
      limit: 200,
    })
      .then((env) => {
        if (cancelled) return;
        const list = env.data ?? [];
        setPairs(list);
        if (list[0]) setSelected({ base: list[0].base, quote: list[0].quote });
        setPairsLoading(false);
      })
      .catch((err: Error) => {
        if (cancelled) return;
        setPairsError(err.message);
        setPairsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [venue]);

  // Fetch candles whenever the selected pair / timeframe / granularity changes.
  useEffect(() => {
    if (!selected) return;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    const url = `${API_BASE_URL}/v1/chart?asset=${encodeURIComponent(selected.base)}&quote=${encodeURIComponent(selected.quote)}&timeframe=${timeframe}&granularity=${granularity}&price_type=vwap`;
    fetch(url, { signal: controller.signal })
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.json() as Promise<{ data: ChartPoint[] | { points?: ChartPoint[] } }>;
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
  }, [selected, timeframe, granularity]);

  if (pairsLoading) {
    return (
      <Panel
        title="Live chart"
        hint="Loading pairs…"
        source={asExample('/v1/markets', { source: venue })}
      >
        <div className="h-[360px]" />
      </Panel>
    );
  }
  if (pairsError) {
    return (
      <Panel
        title="Live chart"
        hint="Pair list unavailable"
        source={asExample('/v1/markets', { source: venue })}
      >
        <div className="flex h-[360px] items-center justify-center px-4 text-center text-sm text-red-600 dark:text-red-400">
          Couldn&apos;t load pairs for this venue ({pairsError}). Refresh to
          retry, or check{' '}
          <a
            href="https://status.ratesengine.net"
            target="_blank"
            rel="noreferrer"
            className="underline"
          >
            status.ratesengine.net
          </a>
          .
        </div>
      </Panel>
    );
  }
  if (pairs.length === 0) {
    return (
      <Panel
        title="Live chart"
        hint="No pairs reporting"
        source={asExample('/v1/markets', { source: venue })}
      >
        <div className="flex h-[360px] items-center justify-center text-sm text-slate-500">
          No pairs reporting in the last 14 days.
        </div>
      </Panel>
    );
  }

  const baseLabel = selected ? labelOf(selected.base) : '';
  const quoteLabel = selected ? labelOf(selected.quote) : '';

  return (
    <Panel
      title="Live chart"
      hint={`OHLC candles powered by /v1/chart · ${baseLabel} / ${quoteLabel}`}
      source={
        selected
          ? asExample('/v1/chart', {
              asset: selected.base,
              quote: selected.quote,
              timeframe,
              granularity,
              price_type: 'vwap',
            })
          : asExample('/v1/chart', { asset: 'crypto:XLM', quote: 'crypto:USDT' })
      }
    >
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <PairPicker
            pairs={pairs}
            value={selected}
            onChange={(p) => setSelected(p)}
          />
          <Picker
            label="TF"
            options={TIMEFRAMES}
            value={timeframe}
            onChange={(v) => setTimeframe(v as Timeframe)}
          />
          <Picker
            label="GR"
            options={GRANULARITIES}
            value={granularity}
            onChange={(v) => setGranularity(v as Granularity)}
          />
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
    </Panel>
  );
}

// labelOf strips the canonical-form prefix so dropdown + header
// text reads as a plain ticker (e.g. "XLM / USDT" not "crypto:XLM /
// crypto:USDT").
function labelOf(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.slice(5);
  if (canonical.startsWith('crypto:')) return canonical.slice(7);
  const dashIx = canonical.indexOf('-');
  if (dashIx !== -1) return canonical.slice(0, dashIx);
  return canonical.length > 12 ? `${canonical.slice(0, 6)}…` : canonical;
}

function PairPicker({
  pairs,
  value,
  onChange,
}: {
  pairs: Market[];
  value: { base: string; quote: string } | null;
  onChange: (p: { base: string; quote: string }) => void;
}) {
  return (
    <label className="inline-flex items-center gap-1.5 rounded-md border border-slate-200 bg-white px-2 py-1 dark:border-slate-700 dark:bg-slate-900">
      <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
        Pair
      </span>
      <select
        value={value ? `${value.base}|${value.quote}` : ''}
        onChange={(e) => {
          const [base, quote] = e.target.value.split('|');
          onChange({ base, quote });
        }}
        className="bg-transparent text-xs font-mono uppercase tracking-wider text-slate-700 focus:outline-none dark:text-slate-300"
      >
        {pairs.map((p) => (
          <option key={`${p.base}|${p.quote}`} value={`${p.base}|${p.quote}`}>
            {labelOf(p.base)}/{labelOf(p.quote)}
          </option>
        ))}
      </select>
    </label>
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
