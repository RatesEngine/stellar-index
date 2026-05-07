'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { SourceSparkline } from '@/components/SourceSparkline';

interface VolumeBucket {
  hour: string;
  volume_usd: string;
}

interface SourceRow {
  name: string;
  class: string;
  subclass: string;
  trade_count_24h?: number;
  markets_count_24h?: number;
  volume_24h_usd?: string | null;
  volume_history_24h?: VolumeBucket[];
}

const TONE: Record<string, string> = {
  binance: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200',
  coinbase: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200',
  kraken: 'bg-purple-100 text-purple-800 dark:bg-purple-900/40 dark:text-purple-200',
  bitstamp: 'bg-teal-100 text-teal-800 dark:bg-teal-900/40 dark:text-teal-200',
};

const LABEL: Record<string, string> = {
  binance: 'Binance',
  coinbase: 'Coinbase',
  kraken: 'Kraken',
  bitstamp: 'Bitstamp',
};

export function ExchangesView() {
  const q = useQuery<SourceRow[]>({
    queryKey: ['/v1/sources', 'stats,sparkline', 'cex'],
    queryFn: async () => {
      const env = await apiGet<{ data: SourceRow[] }>('/v1/sources', { include: 'stats,sparkline' });
      const arr = env.data ?? [];
      return arr
        .filter((s) => s.class === 'exchange' && s.subclass === 'cex')
        .sort((a, b) => {
          const av = a.volume_24h_usd ? Number(a.volume_24h_usd) : 0;
          const bv = b.volume_24h_usd ? Number(b.volume_24h_usd) : 0;
          return bv - av;
        });
    },
  });

  const rows = q.data ?? [];
  const totalVol = rows.reduce((s, r) => s + (r.volume_24h_usd ? Number(r.volume_24h_usd) : 0), 0);
  const totalTrades = rows.reduce((s, r) => s + (r.trade_count_24h ?? 0), 0);
  const totalMarkets = rows.reduce((s, r) => s + (r.markets_count_24h ?? 0), 0);

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Exchanges</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Connected centralised exchanges feeding the Rates Engine
          aggregator. Per-venue 24h USD volume, trade count, and
          coverage. Click a venue for its full pair list. On-chain
          DEXes and AMM pools live at{' '}
          <Link href="/dexes" className="text-brand-600 hover:underline">
            /dexes
          </Link>
          .
        </p>
      </header>

      <Panel
        title={`${rows.length} centralised exchanges`}
        hint={
          rows.length > 0
            ? `Total 24h: $${formatCompact(totalVol)} across ${formatCompact(totalTrades)} trades on ${totalMarkets} pairs`
            : 'Source: /v1/sources?include=stats'
        }
        source={asExample('/v1/sources', { include: 'stats' })}
        bodyClassName="-mx-4"
      >
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <Th>#</Th>
                <Th>Exchange</Th>
                <Th align="right">24h volume</Th>
                <Th>24h chart</Th>
                <Th align="right">24h trades</Th>
                <Th align="right">Pairs</Th>
                <Th align="right">Share of CEX vol</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {q.isLoading && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-sm text-slate-500">
                    Loading exchanges…
                  </td>
                </tr>
              )}
              {!q.isLoading && rows.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-sm text-slate-500">
                    No CEX sources reporting.
                  </td>
                </tr>
              )}
              {rows.map((r, i) => {
                const vol = r.volume_24h_usd ? Number(r.volume_24h_usd) : 0;
                const tone = TONE[r.name] ?? 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
                const label = LABEL[r.name] ?? r.name;
                const share = totalVol > 0 ? (vol / totalVol) * 100 : 0;
                return (
                  <tr key={r.name} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                    <Td>
                      <span className="font-mono text-[11px] text-slate-400">{i + 1}</span>
                    </Td>
                    <Td>
                      <Link
                        href={`/exchanges/${r.name}`}
                        className={`inline-block rounded px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {label}
                      </Link>
                    </Td>
                    <Td align="right">
                      {vol > 0 ? (
                        <span className="font-mono tabular-nums">${formatCompact(vol)}</span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">—</span>
                      )}
                    </Td>
                    <Td>
                      <SourceSparkline buckets={r.volume_history_24h} />
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        {r.trade_count_24h && r.trade_count_24h > 0 ? formatCompact(r.trade_count_24h) : '0'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        {r.markets_count_24h ?? 0}
                      </span>
                    </Td>
                    <Td align="right">
                      <div className="inline-flex items-center gap-2">
                        <div className="h-1.5 w-16 overflow-hidden rounded-full bg-slate-200 dark:bg-slate-800">
                          <div
                            className="h-full bg-brand-500"
                            style={{ width: `${Math.min(100, share)}%` }}
                          />
                        </div>
                        <span className="font-mono tabular-nums text-xs text-slate-500">
                          {share.toFixed(1)}%
                        </span>
                      </div>
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>

      <p className="text-xs text-slate-500">
        Sources are pulled from the static venue registry; per-venue
        24h activity is aggregated from <code className="font-mono text-[11px]">trades</code>{' '}
        in TimescaleDB. Reach the per-pair candlestick view via
        the venue&apos;s pair table.
      </p>
    </div>
  );
}

function Th({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <th
      scope="col"
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      {children}
    </th>
  );
}

function Td({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <td className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>{children}</td>
  );
}
