'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';

import { apiGet } from '@/api/client';
import { assetHrefFor } from '@/lib/fiat-slugs';

interface CurrencyRow {
  ticker: string;
  name: string;
  rate_usd: number;
  change_24h_pct?: number;
}

// Names hardcoded — fiat ISO 4217 ticker → English name is stable
// and the 6-tile home strip doesn't justify an extra round-trip
// to /v1/assets/verified for the human-readable label.
const FEATURED: Array<{ ticker: string; name: string }> = [
  { ticker: 'EUR', name: 'Euro' },
  { ticker: 'GBP', name: 'British Pound' },
  { ticker: 'JPY', name: 'Japanese Yen' },
  { ticker: 'CHF', name: 'Swiss Franc' },
  { ticker: 'CAD', name: 'Canadian Dollar' },
  { ticker: 'AUD', name: 'Australian Dollar' },
];

/**
 * HomeCurrencies — strip of major-currency cards on the home page.
 *
 * Migration history (F-1201 audit-2026-05-12): pre-rc.48 this
 * called /v1/currencies (the single bulk endpoint that returned
 * every catalogue currency's ticker + name + rate_usd +
 * change_24h_pct). rc.48 removed that route as part of the
 * /v1/coins + /v1/currencies → /v1/assets consolidation. The
 * home strip now uses /v1/price/batch to get the 6 featured rates
 * in one round-trip against fiat:USD; names are hardcoded above.
 * change_24h_pct is intentionally dropped from the home strip —
 * the per-currency detail page (/v1/assets/{slug}) carries it
 * when consumers want it.
 */
export function HomeCurrencies() {
  const q = useQuery<Record<string, CurrencyRow>>({
    queryKey: ['/v1/price/batch', 'home-currencies'],
    queryFn: async () => {
      const assetIds = FEATURED.map((f) => `fiat:${f.ticker}`).join(',');
      const env = await apiGet<{
        data: Array<{ asset_id: string; price: string | null }>;
      }>(`/v1/price/batch?asset_ids=${encodeURIComponent(assetIds)}&quote=fiat:USD`, {});
      const map: Record<string, CurrencyRow> = {};
      // /v1/price/batch returns "price of asset in quote", i.e.
      // 1 EUR = X USD. That's exactly the rate_usd field shape
      // the home strip already displays — no inversion needed.
      for (const row of env.data ?? []) {
        const ticker = row.asset_id.replace(/^fiat:/, '');
        const featured = FEATURED.find((f) => f.ticker === ticker);
        if (!featured || !row.price) continue;
        const rate = Number(row.price);
        if (!(rate > 0)) continue;
        map[ticker] = {
          ticker,
          name: featured.name,
          rate_usd: rate,
        };
      }
      return map;
    },
    refetchInterval: 5 * 60_000,
  });

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">World currencies</h2>
          <p className="text-sm text-ink-body">
            Live USD-base rates for the major fiat currencies — full
            ~200-ticker coverage at{' '}
            <Link href="/assets" className="text-brand-600 hover:underline">
              /assets
            </Link>
            .
          </p>
        </div>
        <Link
          href="/assets"
          className="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline"
        >
          All assets <ArrowRight className="h-3 w-3" />
        </Link>
      </div>
      {q.isError && (
        <div className="rounded-md border border-warn-300 bg-warn-50 px-4 py-3 text-sm text-warn-700">
          Couldn&apos;t load live currency rates. The full directory at{' '}
          <Link href="/assets" className="underline hover:no-underline">
            /assets
          </Link>{' '}
          may have more luck — or check{' '}
          <a
            href="https://status.stellarindex.io"
            target="_blank"
            rel="noopener noreferrer"
            className="underline hover:no-underline"
          >
            status.stellarindex.io
          </a>{' '}
          for ongoing incidents.
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
        {FEATURED.map(({ ticker: t }) => {
          const row = q.data?.[t];
          return (
            <Link
              key={t}
              href={assetHrefFor(t)}
              className="rounded-xl border border-line bg-surface p-3 transition-colors hover:border-brand-500"
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-sm font-medium">{t}</span>
                <span className="text-[10px] uppercase tracking-wider text-ink-muted">
                  vs USD
                </span>
              </div>
              <div className="mt-2 font-mono text-lg tabular-nums text-ink">
                {row && row.rate_usd > 0 ? formatRate(row.rate_usd) : '—'}
              </div>
              {row && (
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-[11px] text-ink-muted line-clamp-1" title={row.name}>
                    {row.name}
                  </span>
                  {row.change_24h_pct != null && Number.isFinite(row.change_24h_pct) && (
                    <span
                      className={`font-mono text-[11px] tabular-nums ${
                        row.change_24h_pct > 0
                          ? 'text-up'
                          : row.change_24h_pct < 0
                            ? 'text-down'
                            : 'text-ink-muted'
                      }`}
                      title="24h % change in USD value (daily-grain feed)"
                    >
                      {row.change_24h_pct > 0 ? '+' : ''}
                      {row.change_24h_pct.toFixed(2)}%
                    </span>
                  )}
                </div>
              )}
            </Link>
          );
        })}
      </div>
    </section>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  return n.toFixed(6);
}
