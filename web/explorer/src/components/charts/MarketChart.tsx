'use client';

import { useState } from 'react';
import dynamic from 'next/dynamic';
import { useQuery } from '@tanstack/react-query';

import { API_BASE_URL } from '@/api/client';

// CandleChart pulls in lightweight-charts (~155 KB). Lazy-load it so
// the surrounding page renders without paying the bundle tax up front.
const CandleChart = dynamic(
  () => import('@/components/charts/CandleChart').then((m) => m.CandleChart),
  { ssr: false, loading: () => <div className="h-[360px]" /> },
);

// One timeframe control that also picks the candle granularity +
// window (the exchange-standard UX — users pick "how far back", not the
// bar size). Each maps to a /v1/ohlc (interval, limit). We pick the
// FINEST interval that still covers the whole window within the API's
// 1000-bar/request cap — so each window shows the most detail we can
// serve (e.g. 24h is 5-minute candles, not 15m; 90d is 4h, not daily).
type TF = '24h' | '7d' | '30d' | '90d' | '1y' | 'all';
const TIMEFRAMES: { key: TF; label: string; interval: string; limit: number }[] = [
  { key: '24h', label: '24h', interval: '5m', limit: 288 }, // 24h × 12
  { key: '7d', label: '7d', interval: '15m', limit: 672 }, //  7d × 96
  { key: '30d', label: '30d', interval: '1h', limit: 720 }, // 30d × 24
  { key: '90d', label: '90d', interval: '4h', limit: 540 }, // 90d × 6
  { key: '1y', label: '1y', interval: '1d', limit: 365 }, //  finest ≤1000 bars for a year
  { key: 'all', label: 'All', interval: '1w', limit: 600 }, // full history (daily would blow the cap)
];

interface OHLCBar {
  t: string;
  o: string;
  h: string;
  l: string;
  c: string;
  v_base: string;
  v_quote: string;
  n: number;
}

type Bar = { time: number; open: number; high: number; low: number; close: number; volume: number };

/**
 * MarketChart — the canonical price chart used across every market /
 * pair / exchange surface: real OHLC candlesticks with a volume
 * histogram underneath, served by GET /v1/ohlc?base&quote&interval
 * (which carries true open/high/low/close + per-bar quote volume,
 * unlike the VWAP-only /v1/chart). A single timeframe control drives
 * both the lookback window and the candle granularity.
 */
export function MarketChart({
  base,
  quote,
  baseLabel,
  quoteLabel,
  height = 380,
  defaultTimeframe = '7d',
}: {
  base: string;
  quote: string;
  baseLabel: string;
  quoteLabel: string;
  height?: number;
  defaultTimeframe?: TF;
}) {
  const [tf, setTf] = useState<TF>(defaultTimeframe);
  const spec = TIMEFRAMES.find((t) => t.key === tf) ?? TIMEFRAMES[1];

  const query = useQuery<Bar[], Error>({
    queryKey: ['/v1/ohlc', base, quote, spec.interval, spec.limit],
    queryFn: async ({ signal }) => {
      const url = `${API_BASE_URL}/v1/ohlc?base=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&interval=${spec.interval}&limit=${spec.limit}`;
      const r = await fetch(url, { signal });
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      const env = (await r.json()) as { data?: { intervals?: OHLCBar[] } };
      return (env.data?.intervals ?? []).map((b) => ({
        time: Math.floor(new Date(b.t).getTime() / 1000),
        open: Number(b.o),
        high: Number(b.h),
        low: Number(b.l),
        close: Number(b.c),
        volume: Number(b.v_quote),
      }));
    },
  });

  const data = query.data ?? [];
  const loading = query.isLoading;
  const error = query.error ? query.error.message : null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <div className="inline-flex gap-0.5 rounded-md border border-line bg-surface p-0.5">
          {TIMEFRAMES.map((o) => (
            <button
              key={o.key}
              type="button"
              onClick={() => setTf(o.key)}
              aria-pressed={tf === o.key}
              className={`rounded px-2 py-0.5 text-[11px] font-mono uppercase tracking-wider ${
                tf === o.key ? 'bg-brand-600 text-white' : 'text-ink-muted hover:bg-surface-subtle'
              }`}
            >
              {o.label}
            </button>
          ))}
        </div>
        <span className="ml-auto font-mono text-ink-muted">
          {baseLabel} / {quoteLabel}
        </span>
      </div>
      {loading && (
        <div className="flex items-center justify-center text-sm text-ink-muted" style={{ height }}>
          Loading…
        </div>
      )}
      {error && !loading && (
        <div className="flex items-center justify-center text-sm text-ink-muted" style={{ height }}>
          {error === 'HTTP 404'
            ? 'No price history for this pair + window yet.'
            : `Chart unavailable (${error}).`}
        </div>
      )}
      {!loading && !error && data.length === 0 && (
        <div className="flex items-center justify-center text-sm text-ink-muted" style={{ height }}>
          No price history for this pair + window yet.
        </div>
      )}
      {!loading && !error && data.length > 0 && (
        <CandleChart
          data={data}
          height={height}
          ariaLabel={`${baseLabel}/${quoteLabel} OHLC candlestick chart with volume`}
        />
      )}
    </div>
  );
}
