'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';

interface SourceRow {
  name: string;
  class: string;
  paid?: boolean;
  backfill_available?: boolean;
  trade_count_24h?: number;
  volume_24h_usd?: string | null;
}

const NAMES: Record<string, string> = {
  coingecko: 'CoinGecko',
  coinmarketcap: 'CoinMarketCap',
  cryptocompare: 'CryptoCompare',
};

const HOMEPAGES: Record<string, string> = {
  coingecko: 'https://www.coingecko.com',
  coinmarketcap: 'https://coinmarketcap.com',
  cryptocompare: 'https://www.cryptocompare.com',
};

/**
 * ReferencePriceAggregators — surfaces the data-aggregator class
 * sources (CG / CMC / CryptoCompare) we cross-check against.
 * Distinct from on-chain routers / yield vaults on this page;
 * these don't price into VWAP either, but for a different reason
 * (they're aggregates of upstream venues we already index).
 */
export function ReferencePriceAggregators() {
  const q = useQuery<SourceRow[]>({
    queryKey: ['/v1/sources', 'class=aggregator'],
    queryFn: async () => {
      const env = await apiGet<{ data: SourceRow[] }>('/v1/sources', {
        class: 'aggregator',
        include: 'stats',
      });
      return (env.data ?? []).sort((a, b) => a.name.localeCompare(b.name));
    },
  });

  const rows = q.data ?? [];

  return (
    <Panel
      title="Reference price aggregators"
      hint="Cross-check sources — never priced into our VWAP"
      source={asExample('/v1/sources', { class: 'aggregator', include: 'stats' })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
              <Th>Source</Th>
              <Th>Cost</Th>
              <Th align="right">Backfill</Th>
              <Th>Role</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {q.isLoading && (
              <tr>
                <td colSpan={4} className="px-4 py-6 text-center text-sm text-ink-muted">
                  Loading sources…
                </td>
              </tr>
            )}
            {!q.isLoading && rows.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-6 text-center text-sm text-ink-muted">
                  No reference aggregators registered.
                </td>
              </tr>
            )}
            {rows.map((s) => (
              <tr key={s.name} className="hover:bg-surface-muted">
                <Td>
                  <a
                    href={HOMEPAGES[s.name] ?? '#'}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="font-medium text-ink hover:text-brand-600"
                  >
                    {NAMES[s.name] ?? s.name}
                  </a>
                </Td>
                <Td>
                  <span
                    className={`inline-block rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
                      s.paid
                        ? 'bg-warn-50 text-warn-700'
                        : 'bg-up-subtle text-up-strong'
                    }`}
                  >
                    {s.paid ? 'Paid feed' : 'Free tier'}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono text-xs text-ink-muted">
                    {s.backfill_available ? 'available' : '—'}
                  </span>
                </Td>
                <Td>
                  <span className="text-xs text-ink-body">
                    Cross-check + divergence detection
                  </span>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="border-t border-line px-4 py-2 text-xs text-ink-muted">
        Reference aggregators are <strong>excluded from VWAP</strong> by
        policy — they aggregate the same upstream venues we already
        index, so including them would double-count those underlying
        markets and let their methodology bleed into ours. They show
        up here so divergence flags can attribute &ldquo;us vs CG&rdquo; cleanly.
      </p>
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
