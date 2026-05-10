'use client';

import Link from 'next/link';

import { useCoins, type Coin } from '@/api/hooks';

/**
 * HomeTopMovers — top 5 gainers + top 5 losers by 24h % change.
 *
 * Pulls the top 50 by activity (the explorer's default
 * `/v1/coins` ordering), filters to assets with a non-null
 * change_24h_pct, then sorts and takes top 5 each direction.
 *
 * Pure client-side; no extra round trips beyond the
 * already-cached top-50 React Query payload.
 */
export function HomeTopMovers() {
  const { data, isLoading, isError } = useCoins(50);

  const { gainers, losers } = pickMovers(data?.coins ?? []);

  if (isError) {
    return null;
  }

  return (
    <section className="space-y-3">
      <div className="space-y-1">
        <h2 className="text-2xl font-semibold tracking-tight">Top movers</h2>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          24-hour price change across the most active classic
          assets. Updates every refresh; no synthesised data.
        </p>
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <MoverColumn
          title="Gainers"
          tone="up"
          coins={gainers}
          isLoading={isLoading && !data}
        />
        <MoverColumn
          title="Losers"
          tone="down"
          coins={losers}
          isLoading={isLoading && !data}
        />
      </div>
    </section>
  );
}

function MoverColumn({
  title,
  tone,
  coins,
  isLoading,
}: {
  title: string;
  tone: 'up' | 'down';
  coins: Coin[];
  isLoading: boolean;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
      <header className="flex items-baseline justify-between border-b border-slate-200 bg-slate-50 px-4 py-2 text-[11px] uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950">
        <span className={tone === 'up' ? 'text-emerald-700 dark:text-emerald-400' : 'text-rose-700 dark:text-rose-400'}>
          {title}
        </span>
        <span>24h</span>
      </header>
      {isLoading ? (
        <div className="px-4 py-6 text-sm text-slate-500">Loading…</div>
      ) : coins.length === 0 ? (
        <div className="px-4 py-6 text-sm text-slate-500">
          Not enough movement to rank.
        </div>
      ) : (
        <ul className="divide-y divide-slate-100 dark:divide-slate-800">
          {coins.map((c) => (
            <li
              key={c.asset_id}
              className="flex items-center justify-between px-4 py-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/40"
            >
              <Link
                href={`/assets/${c.slug}`}
                className="flex items-baseline gap-2 text-sm"
              >
                <span className="font-medium text-ink dark:text-slate-100">
                  {c.code}
                </span>
                {c.price_usd && (
                  <span className="font-mono tabular-nums text-xs text-slate-500">
                    ${formatPrice(Number(c.price_usd))}
                  </span>
                )}
              </Link>
              <span
                className={`font-mono tabular-nums text-xs ${
                  Number(c.change_24h_pct) > 0
                    ? 'text-emerald-600 dark:text-emerald-400'
                    : 'text-rose-600 dark:text-rose-400'
                }`}
              >
                {Number(c.change_24h_pct) > 0 ? '+' : ''}
                {Number(c.change_24h_pct).toFixed(2)}%
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function pickMovers(coins: Coin[]): { gainers: Coin[]; losers: Coin[] } {
  const withChange = coins.filter((c) => {
    if (!c.change_24h_pct) return false;
    const n = Number(c.change_24h_pct);
    return Number.isFinite(n) && n !== 0;
  });
  const sorted = [...withChange].sort(
    (a, b) => Number(b.change_24h_pct) - Number(a.change_24h_pct),
  );
  return {
    gainers: sorted.filter((c) => Number(c.change_24h_pct) > 0).slice(0, 5),
    losers: sorted.filter((c) => Number(c.change_24h_pct) < 0).slice(-5).reverse(),
  };
}

function formatPrice(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  if (n > 0) return n.toExponential(3);
  return '0';
}
