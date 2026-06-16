'use client';

import Link from 'next/link';

import { useCoins, useVerifiedSlugs, type Coin } from '@/api/hooks';

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
  const { data: verifiedSlugs } = useVerifiedSlugs();

  const { gainers, losers } = pickMovers(data?.coins ?? []);

  if (isError) {
    return null;
  }

  return (
    <section className="space-y-3">
      <div className="space-y-1">
        <h2 className="text-2xl font-semibold tracking-tight">Top movers</h2>
        <p className="text-sm text-ink-body">
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
          verifiedSlugs={verifiedSlugs}
        />
        <MoverColumn
          title="Losers"
          tone="down"
          coins={losers}
          isLoading={isLoading && !data}
          verifiedSlugs={verifiedSlugs}
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
  verifiedSlugs,
}: {
  title: string;
  tone: 'up' | 'down';
  coins: Coin[];
  isLoading: boolean;
  verifiedSlugs?: Set<string>;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-line bg-surface">
      <header className="flex items-baseline justify-between border-b border-line bg-surface-muted px-4 py-2 text-[11px] uppercase tracking-wider text-ink-muted">
        <span className={tone === 'up' ? 'text-up' : 'text-down'}>
          {title}
        </span>
        <span>24h</span>
      </header>
      {isLoading ? (
        <div className="px-4 py-6 text-sm text-ink-muted">Loading…</div>
      ) : coins.length === 0 ? (
        <div className="px-4 py-6 text-sm text-ink-muted">
          Not enough movement to rank.
        </div>
      ) : (
        <ul className="divide-y divide-line-subtle">
          {coins.map((c) => (
            <li
              key={c.asset_id}
              className="flex items-center justify-between px-4 py-2.5 hover:bg-surface-muted"
            >
              <Link
                href={`/assets/${c.slug}`}
                className="flex items-baseline gap-2 text-sm"
              >
                <span className="font-medium text-ink">
                  {c.code}
                </span>
                {verifiedSlugs?.has(c.slug.toLowerCase()) && (
                  <span
                    title="Verified currency"
                    aria-label="Verified currency"
                    className="inline-flex items-center"
                  >
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      viewBox="0 0 20 20"
                      fill="currentColor"
                      className="h-3 w-3 text-up"
                      aria-hidden="true"
                    >
                      <path
                        fillRule="evenodd"
                        d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
                        clipRule="evenodd"
                      />
                    </svg>
                  </span>
                )}
                {c.price_usd && (
                  <span className="font-mono tabular-nums text-xs text-ink-muted">
                    ${formatPrice(Number(c.price_usd))}
                  </span>
                )}
              </Link>
              <span
                className={`font-mono tabular-nums text-xs ${
                  Number(c.change_24h_pct) > 0
                    ? 'text-up'
                    : 'text-down'
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
