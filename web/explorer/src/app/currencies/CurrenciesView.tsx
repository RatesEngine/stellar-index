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
  change_24h_pct?: number;
  change_7d_pct?: number;
  history_7d_rates?: number[];
  updated_at?: string;
}

interface CurrenciesPayload {
  currencies?: CurrencyRow[];
  published_at?: string;
  fetched_at?: string;
  source?: string;
}

type SortKey = 'ticker' | 'rate' | 'change_24h' | 'change_7d';
type SortDir = 'asc' | 'desc';

export function CurrenciesView() {
  const [q, setQ] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('ticker');
  const [sortDir, setSortDir] = useState<SortDir>('asc');

  const query = useQuery<CurrenciesPayload>({
    queryKey: ['/v1/currencies', 'sparkline'],
    queryFn: async () => {
      const env = await apiGet<{ data: CurrenciesPayload }>('/v1/currencies', {
        include: 'sparkline',
      });
      return env.data ?? {};
    },
    refetchInterval: 60_000,
  });

  const all = query.data?.currencies ?? [];
  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase();
    const matched = !term
      ? all
      : all.filter(
          (c) =>
            c.ticker.toLowerCase().includes(term) ||
            c.name.toLowerCase().includes(term),
        );
    const sorted = [...matched].sort((a, b) => compareCurrencies(a, b, sortKey, sortDir));
    return sorted;
  }, [all, q, sortKey, sortDir]);

  function toggleSort(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      // Default direction by axis: ticker A→Z, rate / change desc
      // (most users want "biggest first" for numeric columns).
      setSortDir(key === 'ticker' ? 'asc' : 'desc');
    }
  }

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
                <SortableTh sortKey="ticker" current={sortKey} dir={sortDir} onToggle={toggleSort}>
                  Ticker
                </SortableTh>
                <Th>Name</Th>
                <SortableTh sortKey="rate" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  1 USD =
                </SortableTh>
                <Th align="right">1 unit = USD</Th>
                <SortableTh sortKey="change_24h" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  24h %
                </SortableTh>
                <SortableTh sortKey="change_7d" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  7d %
                </SortableTh>
                <Th align="right">7d chart</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {query.isLoading && (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-sm text-slate-500">
                    Loading currencies…
                  </td>
                </tr>
              )}
              {!query.isLoading && filtered.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-sm text-slate-500">
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
                  <Td align="right">
                    <ChangePct value={c.change_24h_pct} />
                  </Td>
                  <Td align="right">
                    <ChangePct value={c.change_7d_pct} />
                  </Td>
                  <Td align="right">
                    <RateSparkline points={c.history_7d_rates} positive={(c.change_7d_pct ?? 0) >= 0} />
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

function Th({ children, align, hint }: { children: React.ReactNode; align?: 'left' | 'right'; hint?: string }) {
  return (
    <th scope="col" className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`} title={hint}>
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

function compareCurrencies(
  a: CurrencyRow,
  b: CurrencyRow,
  key: SortKey,
  dir: SortDir,
): number {
  let cmp = 0;
  switch (key) {
    case 'ticker':
      cmp = a.ticker.localeCompare(b.ticker);
      break;
    case 'rate':
      cmp = (a.rate_usd ?? 0) - (b.rate_usd ?? 0);
      break;
    case 'change_24h':
    case 'change_7d': {
      // null change → push to end of either direction so unknowns
      // stay together rather than pretending they're zero.
      const av = (key === 'change_24h' ? a.change_24h_pct : a.change_7d_pct) ?? null;
      const bv = (key === 'change_24h' ? b.change_24h_pct : b.change_7d_pct) ?? null;
      if (av == null && bv == null) cmp = 0;
      else if (av == null) cmp = 1;
      else if (bv == null) cmp = -1;
      else cmp = av - bv;
      break;
    }
  }
  return dir === 'asc' ? cmp : -cmp;
}

function SortableTh({
  sortKey,
  current,
  dir,
  onToggle,
  align,
  children,
}: {
  sortKey: SortKey;
  current: SortKey;
  dir: SortDir;
  onToggle: (k: SortKey) => void;
  align?: 'left' | 'right';
  children: React.ReactNode;
}) {
  const active = current === sortKey;
  const arrow = active ? (dir === 'asc' ? '↑' : '↓') : '';
  return (
    <th
      scope="col"
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      <button
        type="button"
        onClick={() => onToggle(sortKey)}
        className={`inline-flex items-center gap-1 text-[10px] uppercase tracking-wider ${
          active
            ? 'text-brand-600 dark:text-brand-400'
            : 'text-slate-500 hover:text-slate-700 dark:hover:text-slate-300'
        }`}
      >
        {children}
        {arrow && <span className="font-mono text-xs">{arrow}</span>}
      </button>
    </th>
  );
}

function ChangePct({ value }: { value?: number }) {
  if (value == null || !Number.isFinite(value)) {
    return <span className="font-mono text-xs text-slate-300 dark:text-slate-700">—</span>;
  }
  const positive = value >= 0;
  return (
    <span
      className={`font-mono tabular-nums text-xs ${
        positive ? 'text-emerald-700 dark:text-emerald-400' : 'text-rose-700 dark:text-rose-400'
      }`}
    >
      {positive ? '+' : ''}
      {value.toFixed(2)}%
    </span>
  );
}

function RateSparkline({ points, positive }: { points?: number[]; positive: boolean }) {
  if (!points || points.length < 2) {
    return <span className="font-mono text-[10px] text-slate-300 dark:text-slate-700">—</span>;
  }
  const width = 80;
  const height = 24;
  const min = Math.min(...points);
  const max = Math.max(...points);
  const range = max - min || 1;
  const stepX = width / (points.length - 1);
  const path = points
    .map((p, i) => {
      const x = i * stepX;
      const y = height - ((p - min) / range) * height;
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  const stroke = positive ? '#059669' : '#e11d48';
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="inline-block">
      <path d={path} fill="none" stroke={stroke} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}
