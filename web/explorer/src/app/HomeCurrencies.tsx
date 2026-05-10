'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';

import { apiGet } from '@/api/client';

interface CurrencyRow {
  ticker: string;
  name: string;
  rate_usd: number;
  change_24h_pct?: number;
}

const FEATURED = ['EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD'];

/**
 * HomeCurrencies — strip of major-currency cards on the home page.
 * Surfaces the /currencies route by showing live USD-base rates for
 * a handful of majors, each clickable through to the per-currency
 * converter page.
 */
export function HomeCurrencies() {
  const q = useQuery<Record<string, CurrencyRow>>({
    queryKey: ['/v1/currencies', 'home-strip'],
    queryFn: async () => {
      const env = await apiGet<{ data: { currencies?: CurrencyRow[] } }>('/v1/currencies', {});
      const map: Record<string, CurrencyRow> = {};
      for (const c of env.data?.currencies ?? []) {
        map[c.ticker] = c;
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
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Live USD-base rates for the major fiat currencies — full
            ~200-ticker coverage at{' '}
            <Link href="/currencies" className="text-brand-600 hover:underline">
              /currencies
            </Link>
            .
          </p>
        </div>
        <Link
          href="/currencies"
          className="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline"
        >
          All currencies <ArrowRight className="h-3 w-3" />
        </Link>
      </div>
      {q.isError && (
        <div className="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/40 dark:bg-amber-950/40 dark:text-amber-200">
          Couldn&apos;t load live currency rates. The full directory at{' '}
          <Link href="/currencies" className="underline hover:no-underline">
            /currencies
          </Link>{' '}
          may have more luck — or check{' '}
          <a
            href="https://status.ratesengine.net"
            target="_blank"
            rel="noopener noreferrer"
            className="underline hover:no-underline"
          >
            status.ratesengine.net
          </a>{' '}
          for ongoing incidents.
        </div>
      )}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
        {FEATURED.map((t) => {
          const row = q.data?.[t];
          return (
            <Link
              key={t}
              href={`/currencies/${t}`}
              className="rounded-xl border border-slate-200 bg-white p-3 transition-colors hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
            >
              <div className="flex items-center justify-between">
                <span className="font-mono text-sm font-medium">{t}</span>
                <span className="text-[10px] uppercase tracking-wider text-slate-500">
                  vs USD
                </span>
              </div>
              <div className="mt-2 font-mono text-lg tabular-nums text-slate-900 dark:text-slate-100">
                {row && row.rate_usd > 0 ? formatRate(row.rate_usd) : '—'}
              </div>
              {row && (
                <div className="flex items-baseline justify-between gap-2">
                  <span className="text-[11px] text-slate-500 line-clamp-1" title={row.name}>
                    {row.name}
                  </span>
                  {row.change_24h_pct != null && Number.isFinite(row.change_24h_pct) && (
                    <span
                      className={`font-mono text-[11px] tabular-nums ${
                        row.change_24h_pct > 0
                          ? 'text-emerald-600 dark:text-emerald-400'
                          : row.change_24h_pct < 0
                            ? 'text-rose-600 dark:text-rose-400'
                            : 'text-slate-500'
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
