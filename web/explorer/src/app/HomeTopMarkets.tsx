'use client';

import Link from 'next/link';

import { useMarkets } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * HomeTopMarkets — top 10 trading pairs by trailing-24h USD
 * volume. Sits alongside the asset-centric panels on the home
 * page so a visitor sees *both* the most active assets and the
 * most-traded pairs without leaving the landing page.
 *
 * Pulls /v1/markets?limit=500&order_by=volume_24h_usd_desc and
 * surfaces the head. Each row deep-links to the per-pair detail
 * page at /markets/{base~quote} (PR #803).
 */
export function HomeTopMarkets() {
  const { data, isLoading, isError } = useMarkets(500, 'volume_24h_usd_desc');

  if (isError) return null;

  const top = (data?.markets ?? []).slice(0, 10);

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">
            Top markets
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Pairs ranked by trailing-24h USD volume across all
            sources. Click a row for chart, recent trades, and
            per-source breakdown.
          </p>
        </div>
        <Link
          href="/markets"
          className="text-xs text-brand-600 hover:underline"
        >
          All markets →
        </Link>
      </div>
      <div className="overflow-hidden rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        {isLoading && top.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-slate-500">
            Loading…
          </div>
        ) : top.length === 0 ? (
          <div className="px-4 py-6 text-center text-sm text-slate-500">
            No markets returned.
          </div>
        ) : (
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead className="bg-slate-50 dark:bg-slate-950">
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <th className="px-4 py-2 font-medium">#</th>
                <th className="px-4 py-2 font-medium">Pair</th>
                <th className="px-4 py-2 text-right font-medium">
                  Last price
                </th>
                <th className="px-4 py-2 text-right font-medium">
                  24h volume
                </th>
                <th className="px-4 py-2 text-right font-medium">
                  24h trades
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {top.map((m, i) => {
                const slug = `${m.base}~${m.quote}`;
                return (
                  <tr
                    key={`${m.base}|${m.quote}`}
                    className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                  >
                    <td className="px-4 py-2.5 text-slate-400">
                      <Link
                        href={`/markets/${encodeURIComponent(slug)}`}
                        className="hover:text-brand-600"
                      >
                        {i + 1}
                      </Link>
                    </td>
                    <td className="px-4 py-2.5">
                      <Link
                        href={`/markets/${encodeURIComponent(slug)}`}
                        className="hover:text-brand-600"
                      >
                        <span className="font-medium">
                          {shortAsset(m.base)}
                        </span>
                        <span className="mx-1 text-slate-400">/</span>
                        <span className="font-medium">
                          {shortAsset(m.quote)}
                        </span>
                      </Link>
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      {m.last_price ? (
                        <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                          {formatLastPrice(m.last_price)}
                        </span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">
                          —
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      {m.volume_24h_usd ? (
                        <span className="font-mono tabular-nums">
                          ${formatCompact(Number(m.volume_24h_usd))}
                        </span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">
                          —
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
                        {formatCompact(m.trade_count_24h)}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}

function formatLastPrice(raw: string): string {
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return n >= 1000 ? n.toFixed(2) : n >= 1 ? n.toFixed(4) : n >= 0.0001 ? n.toFixed(6) : n.toExponential(3);
}

function shortAsset(canonical: string | undefined | null): string {
  if (!canonical) return '—';
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}
