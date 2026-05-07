'use client';

import { useEffect, useMemo, useState } from 'react';
import Link from 'next/link';

import { useMarkets } from '@/api/hooks';
import { apiGet } from '@/api/client';

interface Trade {
  source: string;
  ts: string;
  base_asset: string;
  quote_asset: string;
  price: string;
  base_amount?: string;
  quote_amount?: string;
}

const REFRESH_INTERVAL_MS = 30_000;
const TOP_PAIRS = 3;
const PER_PAIR_LIMIT = 12;
const DISPLAY_LIMIT = 30;

/**
 * HomeRecentTrades — rolling feed of the most recent trades
 * across the top-3 pairs by 24h volume. Pulls from
 * /v1/markets to enumerate the pairs, then fans out to
 * /v1/history?base=…&quote=… for each. Merged client-side by
 * `ts desc` and rendered as a table.
 *
 * Refresh cadence is 30s — same as the status page, well above
 * any per-pair second-by-second flow but keeps the feed live
 * without hammering the API. The merge cap keeps the panel a
 * fixed height regardless of fan-out depth.
 */
export function HomeRecentTrades() {
  const markets = useMarkets(TOP_PAIRS, 'volume_24h_usd_desc');
  const [trades, setTrades] = useState<Trade[]>([]);
  const [error, setError] = useState<string | null>(null);

  // Refetch each pair's history every REFRESH_INTERVAL_MS,
  // merge by ts desc, take the top DISPLAY_LIMIT.
  const pairs = useMemo(
    () =>
      (markets.data?.markets ?? [])
        .slice(0, TOP_PAIRS)
        .map((m) => ({ base: m.base, quote: m.quote })),
    [markets.data],
  );

  useEffect(() => {
    if (pairs.length === 0) return;
    let cancelled = false;
    async function poll() {
      try {
        const fanouts = await Promise.all(
          pairs.map((p) =>
            apiGet<Trade[]>('/v1/history', {
              base: p.base,
              quote: p.quote,
              limit: PER_PAIR_LIMIT,
            }),
          ),
        );
        if (cancelled) return;
        const merged = fanouts
          .flat()
          .sort((a, b) => (a.ts < b.ts ? 1 : -1))
          .slice(0, DISPLAY_LIMIT);
        setTrades(merged);
        setError(null);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Network error');
      }
    }
    poll();
    const id = setInterval(poll, REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [pairs]);

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            Recent trades
            <span
              className="relative inline-flex h-2 w-2"
              aria-label="live feed"
              title="live feed"
            >
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"></span>
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500"></span>
            </span>
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Live feed merging the latest trades across the top {TOP_PAIRS}{' '}
            pairs by 24h USD volume. Refreshes every 30s.
          </p>
        </div>
      </div>
      <div className="overflow-hidden rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        {error && (
          <div className="px-4 py-2 text-xs text-rose-700">
            Live feed unreachable: {error}
          </div>
        )}
        {trades.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-slate-500">
            Waiting for first trades…
          </div>
        ) : (
          <div className="max-h-96 overflow-y-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead className="sticky top-0 bg-slate-50 dark:bg-slate-950">
                <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-2 font-medium">Time</th>
                  <th className="px-4 py-2 font-medium">Pair</th>
                  <th className="px-4 py-2 font-medium">Source</th>
                  <th className="px-4 py-2 text-right font-medium">Price</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 font-mono text-xs dark:divide-slate-800">
                {trades.map((t, i) => {
                  const slug = `${t.base_asset}~${t.quote_asset}`;
                  return (
                    <tr
                      key={`${t.ts}-${t.source}-${i}`}
                      className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                    >
                      <td className="px-4 py-2 tabular-nums text-slate-500">
                        {timeAgo(t.ts)}
                      </td>
                      <td className="px-4 py-2">
                        <Link
                          href={`/markets/${encodeURIComponent(slug)}`}
                          className="hover:text-brand-600"
                        >
                          {short(t.base_asset)} / {short(t.quote_asset)}
                        </Link>
                      </td>
                      <td className="px-4 py-2 uppercase tracking-wider text-slate-600 dark:text-slate-400">
                        {t.source}
                      </td>
                      <td className="px-4 py-2 text-right tabular-nums">
                        {t.price}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  );
}

function short(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}

function timeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms)) return '—';
  const s = Math.round(ms / 1000);
  if (s < 0) return 'now';
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}
