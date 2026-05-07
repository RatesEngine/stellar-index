'use client';

import { useEffect, useState } from 'react';

import { Panel } from '@/components/reveal';
import { CandleChart } from '@/components/charts/CandleChart';
import { asExample, API_BASE_URL } from '@/api/client';

type Timeframe = '24h' | '7d' | '30d' | '1y';
type Granularity = '1m' | '15m' | '1h' | '4h' | '1d';
type Quote = 'native' | 'fiat:USD';

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

const QUOTES: { key: Quote; label: string }[] = [
  { key: 'native', label: 'XLM' },
  { key: 'fiat:USD', label: 'USD' },
];

interface ChartPoint {
  ts: string;
  vwap?: string;
  open?: string;
  high?: string;
  low?: string;
  close?: string;
}

/**
 * Chart tab content for /assets/[slug]?tab=chart.
 *
 * Pulls live data from /v1/chart at request time. The endpoint
 * today returns VWAP points (single-value series); we render
 * each point as a flat candle (open=high=low=close=vwap) until
 * the OHLC bar reshape lands. When the API switches to bar
 * shape this component reads the new fields without further
 * change.
 *
 * Quote defaults to `native` (XLM). Most active classic Stellar
 * assets only have asset/native trades on SDEX; fiat:USD direct
 * VWAP rarely exists. Users can toggle to fiat:USD when the
 * asset is an off-chain crypto:* feed (Binance / Bitstamp /
 * etc.) that does have direct USD pairs.
 */
export function ChartPanel({
  assetID,
}: {
  assetID: string;
}) {
  // When the asset itself is native XLM, "vs XLM" is the identity
  // pair (the API rightly returns 400). Default to USD and drop
  // the XLM option from the picker for that case.
  const isNative = assetID === 'native';
  const quoteOptions = isNative
    ? QUOTES.filter((q) => q.key !== 'native')
    : QUOTES;
  const [timeframe, setTimeframe] = useState<Timeframe>('24h');
  const [granularity, setGranularity] = useState<Granularity>('1h');
  const [quote, setQuote] = useState<Quote>(isNative ? 'fiat:USD' : 'native');
  const [data, setData] = useState<
    { time: number; open: number; high: number; low: number; close: number }[]
  >([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    const url = `${API_BASE_URL}/v1/chart?asset=${encodeURIComponent(assetID)}&quote=${encodeURIComponent(quote)}&timeframe=${timeframe}&granularity=${granularity}&price_type=vwap`;
    fetch(url, { signal: controller.signal })
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return r.json() as Promise<{
          data: ChartPoint[] | { points?: ChartPoint[] };
        }>;
      })
      .then((env) => {
        // /v1/chart returns { data: { points: [{ t, p, v_usd }] } }.
        // Each point maps to a flat candle until the OHLC bar
        // reshape lands. Defensive: tolerate the old top-level-
        // array shape too.
        const points = Array.isArray(env.data)
          ? (env.data as ChartPoint[])
          : (env.data?.points ?? []);
        const bars = points.map((p) => {
          const t = (p as unknown as { t?: string; ts?: string }).t ?? p.ts ?? '';
          const v = Number(
            (p as unknown as { p?: string }).p ?? p.vwap ?? p.close ?? '0',
          );
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
  }, [assetID, quote, timeframe, granularity]);

  return (
    <div className="space-y-4">
      <Panel
        title="Price chart"
        hint={`${timeframe} · ${granularity} · vs ${quote === 'native' ? 'XLM' : 'USD'}`}
        source={asExample('/v1/chart', {
          asset: assetID,
          quote,
          timeframe,
          granularity,
          price_type: 'vwap',
        })}
      >
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <Picker
            label="Quote"
            options={quoteOptions}
            value={quote}
            onChange={(v) => setQuote(v as Quote)}
          />
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
        </div>
        {loading && (
          <div className="flex h-[420px] items-center justify-center text-sm text-slate-500">
            Loading…
          </div>
        )}
        {error && !loading && (
          <div className="flex h-[420px] items-center justify-center text-sm text-red-600 dark:text-red-400">
            {error === 'HTTP 404'
              ? 'No chart data for this asset + window yet'
              : `Chart data unavailable: ${error}`}
          </div>
        )}
        {!loading && !error && data.length === 0 && (
          <div className="flex h-[420px] items-center justify-center text-sm text-slate-500">
            No chart data for this asset + window yet
          </div>
        )}
        {!loading && !error && data.length > 0 && (
          <CandleChart data={data} height={420} />
        )}
      </Panel>
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
