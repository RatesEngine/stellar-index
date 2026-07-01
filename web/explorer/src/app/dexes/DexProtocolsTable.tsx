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
  soroswap: 'bg-up-subtle text-up-strong',
  phoenix: 'bg-warn-50 text-warn-700',
  aquarius: 'bg-brand-100 text-brand-800',
  sdex: 'bg-line text-ink',
  comet: 'bg-violet-100 text-violet-800',
};

/**
 * DexProtocolsTable — per-DEX summary row (volume, trades, markets).
 * Backed by /v1/sources?include=stats filtered to Subclass=DEX.
 * Sits above the all-pools table on /dexes.
 */
export function DexProtocolsTable() {
  const q = useQuery<SourceRow[]>({
    queryKey: ['/v1/sources', 'stats,sparkline', 'dex'],
    queryFn: async () => {
      const env = await apiGet<{ data: SourceRow[] }>('/v1/sources', { include: 'stats,sparkline' });
      const arr = env.data ?? [];
      return arr
        .filter((s) => s.class === 'exchange' && s.subclass === 'dex')
        .sort((a, b) => {
          const av = a.volume_24h_usd ? Number(a.volume_24h_usd) : 0;
          const bv = b.volume_24h_usd ? Number(b.volume_24h_usd) : 0;
          if (bv !== av) return bv - av;
          return (b.trade_count_24h ?? 0) - (a.trade_count_24h ?? 0);
        });
    },
  });

  const rows = q.data ?? [];

  return (
    <Panel
      title="DEX protocols"
      hint="Per-protocol 24h activity"
      source={asExample('/v1/sources', { include: 'stats' })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
              <Th>Protocol</Th>
              <Th align="right">24h volume</Th>
              <Th>24h chart</Th>
              <Th align="right">24h trades</Th>
              <Th align="right">Active pools</Th>
              <Th align="right">VWAP weight</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {q.isLoading && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-ink-muted">
                  Loading protocols…
                </td>
              </tr>
            )}
            {!q.isLoading && rows.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-ink-muted">
                  No DEX protocols reporting 24h activity.
                </td>
              </tr>
            )}
            {rows.map((r) => {
              const vol = r.volume_24h_usd ? Number(r.volume_24h_usd) : null;
              const tone = TONE[r.name] ?? 'bg-surface-subtle text-ink-body';
              return (
                <tr key={r.name} className="hover:bg-surface-muted">
                  <Td>
                    <Link
                      href={`/dexes/${r.name}`}
                      className={`inline-block rounded-sm px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                    >
                      {r.name}
                    </Link>
                  </Td>
                  <Td align="right">
                    {vol != null && Number.isFinite(vol) && vol > 0 ? (
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
                      {r.trade_count_24h && r.trade_count_24h > 0
                        ? formatCompact(r.trade_count_24h)
                        : '0'}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-ink-body">
                      {r.markets_count_24h && r.markets_count_24h > 0
                        ? formatCompact(r.markets_count_24h)
                        : '0'}
                    </span>
                  </Td>
                  <Td align="right">
                    <Link
                      href={`/dexes/${r.name}`}
                      className="text-xs text-brand-600 hover:underline"
                    >
                      details →
                    </Link>
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
