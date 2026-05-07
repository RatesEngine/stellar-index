'use client';

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Search } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';

interface CurrencyRow {
  ticker: string;
  name: string;
  rate_usd: number;
  updated_at?: string;
}

interface CurrenciesPayload {
  currencies?: CurrencyRow[];
  published_at?: string;
  fetched_at?: string;
  source?: string;
}

export function CurrenciesView() {
  const [q, setQ] = useState('');

  const query = useQuery<CurrenciesPayload>({
    queryKey: ['/v1/currencies'],
    queryFn: async () => {
      const env = await apiGet<{ data: CurrenciesPayload }>('/v1/currencies', {});
      return env.data ?? {};
    },
    refetchInterval: 60_000,
  });

  const all = query.data?.currencies ?? [];
  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase();
    if (!term) return all;
    return all.filter(
      (c) => c.ticker.toLowerCase().includes(term) || c.name.toLowerCase().includes(term),
    );
  }, [all, q]);

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Currencies</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          World fiat currencies with live rates against USD. Free
          coverage from{' '}
          <a
            href="https://github.com/fawazahmed0/exchange-api"
            target="_blank"
            rel="noreferrer noopener"
            className="text-brand-600 hover:underline"
          >
            currency-api
          </a>{' '}
          (ECB / FRBNY-aggregated, daily-updated). Higher-frequency,
          deeper coverage (1h / 24h / 7d change, market cap, volume,
          circulating supply) lands once we wire a paid forex feed.
        </p>
      </header>

      <Panel
        title={`${all.length} currencies`}
        hint={
          query.data?.published_at
            ? `Source rates published ${formatDate(query.data.published_at)}`
            : 'Loading rates…'
        }
        source={asExample('/v1/currencies', {})}
        bodyClassName="-mx-4"
      >
        <div className="px-4 pb-3 pt-1">
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Search by ticker or name…"
              className="w-72 rounded-md border border-slate-200 bg-white py-1.5 pl-8 pr-3 text-sm placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:placeholder:text-slate-500"
            />
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <Th>Ticker</Th>
                <Th>Name</Th>
                <Th align="right">1 USD =</Th>
                <Th align="right">1 unit = USD</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {query.isLoading && (
                <tr>
                  <td colSpan={4} className="px-4 py-8 text-center text-sm text-slate-500">
                    Loading currencies…
                  </td>
                </tr>
              )}
              {!query.isLoading && filtered.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-4 py-8 text-center text-sm text-slate-500">
                    {q ? `No currencies matched "${q}".` : 'No currencies — the rates worker is still warming up.'}
                  </td>
                </tr>
              )}
              {filtered.map((c) => (
                <tr key={c.ticker} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                  <Td>
                    <span className="font-mono text-[13px] font-medium text-slate-900 dark:text-slate-100">
                      {c.ticker}
                    </span>
                  </Td>
                  <Td>
                    <span className="text-slate-700 dark:text-slate-300">{c.name}</span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                      {formatRate(c.rate_usd)} {c.ticker}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                      {c.rate_usd > 0 ? `$${formatRate(1 / c.rate_usd)}` : '—'}
                    </span>
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>

      <p className="text-xs text-slate-500">
        Currency-api is community-maintained, MIT-licensed, and
        rate-limit-free via the jsDelivr CDN. The cache refreshes
        hourly; rate values are pulled once daily by the upstream
        from ECB + Federal Reserve reference series.
      </p>
    </div>
  );
}

function Th({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <th scope="col" className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>
      {children}
    </th>
  );
}

function Td({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <td className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>{children}</td>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  } catch {
    return iso;
  }
}
