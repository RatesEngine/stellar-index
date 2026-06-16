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
  binance: 'bg-yellow-100 text-yellow-800',
  coinbase: 'bg-brand-100 text-brand-800',
  kraken: 'bg-purple-100 text-purple-800',
  bitstamp: 'bg-teal-100 text-teal-800',
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
        <p className="max-w-3xl text-sm text-ink-body">
          Connected centralised exchanges feeding the Stellar Index
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
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <Th>#</Th>
                <Th>Exchange</Th>
                <Th align="right">24h volume</Th>
                <Th>24h chart</Th>
                <Th align="right">24h trades</Th>
                <Th align="right">Pairs</Th>
                <Th align="right">Share of CEX vol</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {q.isLoading && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-sm text-ink-muted">
                    Loading exchanges…
                  </td>
                </tr>
              )}
              {!q.isLoading && rows.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-6 text-center text-sm text-ink-muted">
                    No CEX sources reporting.
                  </td>
                </tr>
              )}
              {rows.map((r, i) => {
                const vol = r.volume_24h_usd ? Number(r.volume_24h_usd) : 0;
                const tone = TONE[r.name] ?? 'bg-surface-subtle text-ink-body';
                const label = LABEL[r.name] ?? r.name;
                const share = totalVol > 0 ? (vol / totalVol) * 100 : 0;
                return (
                  <tr key={r.name} className="hover:bg-surface-muted">
                    <Td>
                      <span className="font-mono text-[11px] text-ink-faint">{i + 1}</span>
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
                        <span className="text-ink-faint">—</span>
                      )}
                    </Td>
                    <Td>
                      <SourceSparkline buckets={r.volume_history_24h} />
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-ink-body">
                        {r.trade_count_24h && r.trade_count_24h > 0 ? formatCompact(r.trade_count_24h) : '0'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-ink-body">
                        {r.markets_count_24h ?? 0}
                      </span>
                    </Td>
                    <Td align="right">
                      <div className="inline-flex items-center gap-2">
                        <div className="h-1.5 w-16 overflow-hidden rounded-full bg-line">
                          <div
                            className="h-full bg-brand-500"
                            style={{ width: `${Math.min(100, share)}%` }}
                          />
                        </div>
                        <span className="font-mono tabular-nums text-xs text-ink-muted">
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

      <AllCEXMarkets />

      <p className="text-xs text-ink-muted">
        Sources are pulled from the static venue registry; per-venue
        24h activity is aggregated from <code className="font-mono text-[11px]">trades</code>{' '}
        in TimescaleDB. We deliberately subscribe to a curated set of
        pairs per venue (the top-liquidity XLM markets and the
        crypto anchors that triangulate into them); see the per-venue
        page for the full list. Reach the per-pair candlestick view
        via any pair link below.
      </p>
    </div>
  );
}

interface CEXMarket {
  base: string;
  quote: string;
  source?: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
}

// AllCEXMarkets surfaces every CEX pair we observed in the last
// 14 days, sorted by 24h USD volume. The four venue-scoped fetches
// run concurrently and merge client-side — no new API endpoint
// required, and matches the volume-sort across venues.
function AllCEXMarkets() {
  const venues = ['binance', 'coinbase', 'kraken', 'bitstamp'];
  const queries = useQuery<CEXMarket[]>({
    queryKey: ['/v1/markets', 'all-cex'],
    queryFn: async () => {
      const all = await Promise.all(
        venues.map(async (v) => {
          const env = await apiGet<{ data: CEXMarket[] }>('/v1/markets', {
            source: v,
            limit: 200,
            order_by: 'volume_24h_usd_desc',
          });
          return (env.data ?? []).map((m) => ({ ...m, source: v }));
        }),
      );
      const merged = all.flat();
      return merged.sort((a, b) => {
        const av = a.volume_24h_usd ? Number(a.volume_24h_usd) : 0;
        const bv = b.volume_24h_usd ? Number(b.volume_24h_usd) : 0;
        return bv - av;
      });
    },
  });

  const markets = queries.data ?? [];

  return (
    <Panel
      title={`${markets.length} CEX pairs · sorted by 24h volume`}
      hint="One row per (venue, base, quote) tuple — every pair we observed across all four CEXes in the last 14 days"
      source={asExample('/v1/markets', { source: 'binance', limit: 200 })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
              <Th>#</Th>
              <Th>Venue</Th>
              <Th>Pair</Th>
              <Th align="right">Last price</Th>
              <Th align="right">24h volume</Th>
              <Th align="right">24h trades</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {queries.isLoading && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-ink-muted">
                  Loading pairs…
                </td>
              </tr>
            )}
            {!queries.isLoading && markets.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-ink-muted">
                  No CEX pairs reporting.
                </td>
              </tr>
            )}
            {markets.map((m, i) => {
              const slug = `${m.base}~${m.quote}`;
              const vol = m.volume_24h_usd ? Number(m.volume_24h_usd) : null;
              const tone = TONE[m.source ?? ''] ?? 'bg-surface-subtle text-ink-body';
              return (
                <tr
                  key={`${m.source}|${m.base}|${m.quote}`}
                  className="hover:bg-surface-muted"
                >
                  <Td>
                    <span className="font-mono text-[11px] text-ink-faint">{i + 1}</span>
                  </Td>
                  <Td>
                    <Link
                      href={`/exchanges/${m.source}`}
                      className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                    >
                      {LABEL[m.source ?? ''] ?? m.source}
                    </Link>
                  </Td>
                  <Td>
                    <Link
                      href={`/markets/${encodeURIComponent(slug)}`}
                      className="font-mono text-xs hover:text-brand-600"
                    >
                      {m.base.replace('crypto:', '')} / {m.quote.replace('crypto:', '').replace('fiat:', '')}
                    </Link>
                  </Td>
                  <Td align="right">
                    {m.last_price ? (
                      <span className="font-mono tabular-nums text-ink-body">
                        {Number(m.last_price).toFixed(4)}
                      </span>
                    ) : (
                      <span className="text-ink-faint">—</span>
                    )}
                  </Td>
                  <Td align="right">
                    {vol != null && Number.isFinite(vol) && vol > 0 ? (
                      <span className="font-mono tabular-nums">${formatCompact(vol)}</span>
                    ) : (
                      <span className="text-ink-faint">—</span>
                    )}
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-ink-body">
                      {m.trade_count_24h > 0 ? formatCompact(m.trade_count_24h) : '0'}
                    </span>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </Panel>
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
