'use client';

import { useEffect, useState } from 'react';
import dynamic from 'next/dynamic';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';

// Lazy-load lightweight-charts (~155 KB) — only fetched when the
// asset page lands on the chart tab. Other tabs (overview / supply
// / history) don't pay the bundle cost.
const CandleChart = dynamic(
  () => import('@/components/charts/CandleChart').then((m) => m.CandleChart),
  { ssr: false, loading: () => <div className="h-[360px]" /> },
);

// API-canonical timeframes per ADR-0020. The earlier '7d' / '30d'
// labels were the obvious shorthand but the API rejects them as 400 —
// the chart was silently empty for any window beyond 24h.
type Timeframe = '1h' | '24h' | '1w' | '1mo' | '1y' | 'all';
type Granularity = '1m' | '15m' | '1h' | '4h' | '1d';
type Quote = 'native' | 'fiat:USD';

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

interface ChartMeta {
  truncated: boolean;
  data_starts_at?: string;
  requested_from?: string;
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
  //
  // For fiat assets (fiat:CNY, fiat:EUR, …) the only sensible quote
  // is USD — they don't trade against XLM directly. Force USD and
  // drop the XLM picker. The API also routes fiat:fiat pairs to
  // fx_quotes (Frankfurter-backed daily series), so charts work
  // back to ~1999 once fx-history-backfill has been run.
  const isNative = assetID === 'native';
  const isFiat = assetID.startsWith('fiat:');
  const quoteOptions = isNative || isFiat
    ? QUOTES.filter((q) => q.key !== 'native')
    : QUOTES;
  const [timeframe, setTimeframe] = useState<Timeframe>('24h');
  const [granularity, setGranularity] = useState<Granularity>('1h');
  const [quote, setQuote] = useState<Quote>(isNative || isFiat ? 'fiat:USD' : 'native');
  const [data, setData] = useState<
    { time: number; open: number; high: number; low: number; close: number }[]
  >([]);
  const [meta, setMeta] = useState<ChartMeta | null>(null);
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
          data:
            | ChartPoint[]
            | {
                points?: ChartPoint[];
                truncated?: boolean;
                data_starts_at?: string;
                requested_from?: string;
              };
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
        // Retention-truncation signal — surface when the consumer's
        // requested window extends beyond what the deployment has
        // ingested ("you asked for 1y, this is the 8d we have").
        if (!Array.isArray(env.data)) {
          setMeta({
            truncated: env.data?.truncated ?? false,
            data_starts_at: env.data?.data_starts_at,
            requested_from: env.data?.requested_from,
          });
        } else {
          setMeta(null);
        }
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
          <>
            {meta?.truncated && meta.data_starts_at && (
              <div className="mb-2 rounded border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
                Showing data from{' '}
                <strong>{formatYMD(meta.data_starts_at)}</strong> — the
                deployment hasn&apos;t accumulated the full {timeframe}{' '}
                window yet. Earlier history will appear automatically as
                ingestion continues.
              </div>
            )}
            <CandleChart data={data} height={420} />
          </>
        )}
      </Panel>
    </div>
  );
}

// formatYMD trims an ISO timestamp to YYYY-MM-DD so the banner
// reads naturally regardless of whether the API returned a date
// (Frankfurter / fx_quotes) or a full timestamp (prices_1m).
function formatYMD(iso: string): string {
  return iso.slice(0, 10);
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
