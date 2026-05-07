'use client';

import Link from 'next/link';

import { useCoins, type Coin } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * HomeTopAssets — the activity-ranked top 10 from /v1/coins.
 *
 * The endpoint orders by observation_count desc (a proxy for
 * activity), so the first page roughly = "most active assets
 * across all of Stellar." Volume is the trailing-24h USD figure
 * computed from prices_1m. Fields the API doesn't yet expose
 * (price_usd / market_cap_usd) render as `—`.
 *
 * Server-rendered list this isn't — we want this to refresh on
 * the same TanStack cadence as the rest of the home page.
 */
export function HomeTopAssets() {
  const { data, isLoading, isError } = useCoins(
    10,
    undefined,
    undefined,
    undefined,
    undefined,
    { sparkline: true },
  );

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">
            Top assets by activity
          </h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Ranked by total observation count across every venue we
            ingest from. 24h volume sums every (base, quote) pair the
            asset participates in.
          </p>
        </div>
        <Link
          href="/assets"
          className="text-sm text-brand-600 hover:underline dark:text-brand-400"
        >
          See all →
        </Link>
      </div>
      <div className="overflow-x-auto rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-left text-[11px] uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950">
              <th className="px-4 py-2.5 font-medium">#</th>
              <th className="px-4 py-2.5 font-medium">Asset</th>
              <th className="px-4 py-2.5 text-right font-medium">Price</th>
              <th className="px-4 py-2.5 text-right font-medium">24h %</th>
              <th className="px-4 py-2.5 text-right font-medium">
                24h volume
              </th>
              <th className="px-4 py-2.5 text-right font-medium">
                24h chart
              </th>
              <th className="px-4 py-2.5 text-right font-medium">
                Observations
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {isError && (
              <tr>
                <td
                  colSpan={7}
                  className="py-8 text-center text-sm text-down-strong"
                >
                  Failed to load top assets.
                </td>
              </tr>
            )}
            {isLoading && !data && (
              <tr>
                <td
                  colSpan={7}
                  className="py-8 text-center text-sm text-slate-500"
                >
                  Loading…
                </td>
              </tr>
            )}
            {data?.coins.map((coin, idx) => (
              <Row key={coin.asset_id} coin={coin} rank={idx + 1} />
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Row({ coin, rank }: { coin: Coin; rank: number }) {
  const price = parseDec(coin.price_usd);
  const volume = parseDec(coin.volume_24h_usd);
  return (
    <tr className="hover:bg-slate-50 dark:hover:bg-slate-800/40">
      <td className="px-4 py-3 text-slate-400">{rank}</td>
      <td className="px-4 py-3">
        <Link
          href={`/assets/${coin.slug}`}
          className="group flex items-baseline gap-2"
        >
          <span className="font-medium text-ink group-hover:text-brand-600 dark:text-slate-100">
            {coin.code}
          </span>
          <span className="text-[11px] text-slate-500">{coin.slug}</span>
        </Link>
      </td>
      <td className="px-4 py-3 text-right">
        {price != null ? (
          <span className="font-mono tabular-nums text-ink dark:text-slate-100">
            ${formatPrice(price)}
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td className="px-4 py-3 text-right">
        <ChangePct raw={coin.change_24h_pct} />
      </td>
      <td className="px-4 py-3 text-right">
        {volume != null ? (
          <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
            ${formatCompact(volume)}
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td className="px-4 py-3 text-right">
        <RowSparkline points={coin.price_history_24h} />
      </td>
      <td className="px-4 py-3 text-right">
        <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
          {formatCompact(coin.observation_count)}
        </span>
      </td>
    </tr>
  );
}

function parseDec(s: string | null | undefined): number | null {
  if (!s) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

function formatPrice(n: number): string {
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  if (n > 0) return n.toExponential(3);
  return '0';
}

function Dash() {
  return <span className="text-slate-300 dark:text-slate-700">—</span>;
}

function RowSparkline({
  points,
}: {
  points?: { t: string; p?: string | null }[];
}) {
  const values = (points ?? [])
    .map((pt) => (pt.p ? Number(pt.p) : null))
    .filter((v): v is number => v != null && Number.isFinite(v));
  if (values.length < 2) {
    return <span className="font-mono text-[10px] text-slate-300 dark:text-slate-700">—</span>;
  }
  const W = 80;
  const H = 22;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const stepX = W / (values.length - 1);
  const path = values
    .map((v, i) => {
      const x = i * stepX;
      const y = H - ((v - min) / range) * H;
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  const positive = values[values.length - 1] >= values[0];
  const stroke = positive ? '#10b981' : '#f43f5e';
  return (
    <svg width={W} height={H} className="inline-block" viewBox={`0 0 ${W} ${H}`} role="img" aria-label="24h price chart">
      <path d={path} fill="none" stroke={stroke} strokeWidth={1.25} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function ChangePct({ raw }: { raw: string | null | undefined }) {
  if (raw == null) return <Dash />;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <Dash />;
  const tone =
    n > 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : n < 0
        ? 'text-rose-600 dark:text-rose-400'
        : 'text-slate-500';
  const sign = n > 0 ? '+' : '';
  return (
    <span className={`font-mono tabular-nums ${tone}`}>
      {sign}
      {n.toFixed(2)}%
    </span>
  );
}
