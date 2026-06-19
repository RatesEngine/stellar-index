'use client';

import Link from 'next/link';

import { MarketChart } from '@/components/charts/MarketChart';
import { useCoins } from '@/api/hooks';

/**
 * HomeHeroChart — a featured XLM/USD OHLC+volume chart on the landing
 * page so the home view leads with live price action, not just tables.
 * XLM (native) is the network's representative asset; the headline price
 * + 24h change come from /v1/assets, the candles from /v1/ohlc.
 */
export function HomeHeroChart() {
  const { data } = useCoins(1, undefined, undefined, 'XLM');
  const xlm = data?.coins?.find((c) => c.code === 'XLM' || c.slug.toLowerCase() === 'xlm') ?? data?.coins?.[0];
  const price = xlm?.price_usd ? Number(xlm.price_usd) : null;
  const change = xlm?.change_24h_pct != null ? Number(xlm.change_24h_pct) : null;

  return (
    <section className="rounded-card border border-line bg-surface p-5 shadow-card">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-3">
        <div className="flex flex-wrap items-baseline gap-2.5">
          <Link href="/assets/XLM" className="text-h3 font-semibold text-ink hover:text-brand-600">
            XLM
          </Link>
          <span className="text-sm text-ink-muted">Stellar Lumens · live USD price</span>
          {price != null && (
            <span className="font-mono text-lg tabular-nums text-ink">
              ${price >= 1 ? price.toFixed(4) : price.toFixed(6)}
            </span>
          )}
          {change != null && (
            <span
              className={`font-mono text-sm tabular-nums ${
                change > 0 ? 'text-up' : change < 0 ? 'text-down' : 'text-ink-muted'
              }`}
            >
              {change > 0 ? '▲' : change < 0 ? '▼' : ''} {change > 0 ? '+' : ''}
              {change.toFixed(2)}% <span className="text-ink-faint">(24h)</span>
            </span>
          )}
        </div>
        <Link href="/assets/XLM" className="text-xs text-brand-600 hover:underline">
          Full XLM detail →
        </Link>
      </div>
      <MarketChart base="native" quote="fiat:USD" baseLabel="XLM" quoteLabel="USD" height={300} />
    </section>
  );
}
