'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQueries } from '@tanstack/react-query';

import { apiGet } from '@/api/client';
import type { Coin } from '@/api/hooks';

const MAX_COMPARE = 6;
const SUGGESTED = ['USDC', 'XLM', 'USDT', 'AQUA'];

interface CoinDetail extends Coin {
  top_markets?: Array<{ counterparty: string; side: string; trade_count_24h: number; volume_24h_usd?: string | null }>;
  price_history_24h?: Array<{ t: string; p?: string | null }>;
  markets_count?: number | null;
}

export function CompareView() {
  const params = useSearchParams();
  const slugsRaw = params.get('assets') ?? '';
  const slugs = slugsRaw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .slice(0, MAX_COMPARE);

  // useQueries lets us fan out one fetch per slug while sharing the
  // React Query cache with the rest of the app — picking the same
  // asset on /assets/{slug} after coming from here is instant.
  const queries = useQueries({
    queries: slugs.map((slug) => ({
      queryKey: ['/v1/coins', slug],
      queryFn: () =>
        apiGet<CoinDetail>(`/v1/coins/${encodeURIComponent(slug)}`),
      staleTime: 30_000,
    })),
  });

  if (slugs.length === 0) {
    return <EmptyState />;
  }

  const isLoading = queries.some((q) => q.isLoading);
  const coins: (CoinDetail | null)[] = queries.map((q) => q.data ?? null);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <span className="text-xs text-slate-500">Comparing:</span>
        {slugs.map((s) => (
          <span
            key={s}
            className="rounded-full bg-slate-100 px-2 py-0.5 font-mono text-xs dark:bg-slate-800"
          >
            {s}
          </span>
        ))}
        <span className="text-xs text-slate-400">
          {slugs.length} of max {MAX_COMPARE}
        </span>
      </div>

      <div className="overflow-hidden rounded-lg border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-left text-[10px] uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950">
              <th className="px-4 py-2 font-medium">Metric</th>
              {coins.map((c, i) => (
                <th key={i} className="px-4 py-2 text-right font-medium">
                  {c ? (
                    <Link
                      href={`/assets/${c.slug}`}
                      className="hover:text-brand-600"
                    >
                      {c.code}
                    </Link>
                  ) : (
                    <span className="text-slate-400">{slugs[i]}</span>
                  )}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            <Row
              label="Price (USD)"
              cells={coins.map((c) =>
                c?.price_usd ? formatPrice(Number(c.price_usd)) : '—',
              )}
              mono
            />
            <Row
              label="1h change"
              cells={coins.map((c) => fmtChange(c?.change_1h_pct))}
              tones={coins.map((c) => toneFor(c?.change_1h_pct))}
            />
            <Row
              label="24h change"
              cells={coins.map((c) => fmtChange(c?.change_24h_pct))}
              tones={coins.map((c) => toneFor(c?.change_24h_pct))}
            />
            <Row
              label="7d change"
              cells={coins.map((c) => fmtChange(c?.change_7d_pct))}
              tones={coins.map((c) => toneFor(c?.change_7d_pct))}
            />
            <Row
              label="24h volume"
              cells={coins.map((c) =>
                c?.volume_24h_usd ? `$${formatCompact(Number(c.volume_24h_usd))}` : '—',
              )}
              mono
            />
            <Row
              label="Markets 24h"
              cells={coins.map((c) =>
                c?.markets_count != null ? c.markets_count.toLocaleString() : '—',
              )}
              mono
            />
            <Row
              label="Observations"
              cells={coins.map((c) =>
                c?.observation_count != null ? formatCompact(c.observation_count) : '—',
              )}
              mono
            />
            <tr>
              <td className="px-4 py-3 align-top text-[11px] uppercase tracking-wider text-slate-500">
                24h sparkline
              </td>
              {coins.map((c, i) => (
                <td key={i} className="px-4 py-3">
                  {c?.price_history_24h && c.price_history_24h.length > 0 ? (
                    <Sparkline points={c.price_history_24h} />
                  ) : (
                    <span className="text-slate-400">—</span>
                  )}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>

      {isLoading && (
        <div className="text-xs text-slate-500">Refreshing…</div>
      )}
    </div>
  );
}

function EmptyState() {
  return (
    <div className="space-y-4 rounded-lg border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
      <p className="text-sm text-slate-600 dark:text-slate-400">
        Pick 2&ndash;{MAX_COMPARE} assets to compare via the URL:
      </p>
      <code className="block overflow-x-auto rounded bg-slate-950 p-3 font-mono text-xs text-slate-100">
        /compare?assets={SUGGESTED.join(',')}
      </code>
      <Link
        href={`/compare?assets=${SUGGESTED.join(',')}`}
        className="inline-block rounded-md bg-brand-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-brand-700"
      >
        Try it →
      </Link>
    </div>
  );
}

function Row({
  label,
  cells,
  mono,
  tones,
}: {
  label: string;
  cells: string[];
  mono?: boolean;
  tones?: ('up' | 'down' | undefined)[];
}) {
  return (
    <tr>
      <td className="px-4 py-3 text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </td>
      {cells.map((c, i) => (
        <td
          key={i}
          className={`px-4 py-3 text-right tabular-nums ${mono ? 'font-mono' : ''} ${
            tones?.[i] === 'up'
              ? 'text-emerald-700 dark:text-emerald-400'
              : tones?.[i] === 'down'
                ? 'text-rose-700 dark:text-rose-400'
                : ''
          }`}
        >
          {c}
        </td>
      ))}
    </tr>
  );
}

function Sparkline({ points }: { points: { p?: string | null }[] }) {
  const prices = points
    .map((p) => Number(p.p))
    .filter((n) => Number.isFinite(n) && n > 0);
  if (prices.length === 0) return <span className="text-slate-400">—</span>;
  const min = Math.min(...prices);
  const max = Math.max(...prices);
  const range = max - min || max * 0.01;
  const w = 120;
  const h = 32;
  const xStep = points.length > 1 ? w / (points.length - 1) : 0;
  const path = points
    .map((p, i) => {
      const n = Number(p.p);
      if (!Number.isFinite(n)) return null;
      const x = i * xStep;
      const y = h - ((n - min) / range) * h;
      return `${i === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .filter(Boolean)
    .join(' ');
  const trendUp = prices[prices.length - 1]! >= prices[0]!;
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="ml-auto h-8 w-30"
      style={{ width: w }}
    >
      <path
        d={path}
        fill="none"
        strokeWidth="1.5"
        className={trendUp ? 'stroke-emerald-500' : 'stroke-rose-500'}
      />
    </svg>
  );
}

function fmtChange(raw: string | null | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  const sign = n > 0 ? '+' : '';
  return `${sign}${n.toFixed(2)}%`;
}

function toneFor(raw: string | null | undefined): 'up' | 'down' | undefined {
  if (!raw) return undefined;
  const n = Number(raw);
  if (!Number.isFinite(n)) return undefined;
  if (n > 0) return 'up';
  if (n < 0) return 'down';
  return undefined;
}

function formatPrice(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1) return `$${n.toFixed(n >= 100 ? 2 : 4)}`;
  if (n >= 0.001) return `$${n.toFixed(6)}`;
  if (n > 0) return `$${n.toExponential(3)}`;
  return '—';
}

function formatCompact(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toFixed(2);
}
