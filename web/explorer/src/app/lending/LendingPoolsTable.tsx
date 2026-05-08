'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';

interface LendingPool {
  protocol: string;
  pool: string;
  auctions_24h: number;
  auctions_total: number;
  unique_users_30d: number;
  last_seen: string;
}

// Curated annotations for the well-known Blend mainnet contracts.
// Sourced from docs/operations/wasm-audits/blend.md (Phase 4 walk
// of the on-chain Blend deployment, last verified 2026-05-03).
// Other pool addresses fall through to "Lending pool" — the
// reserve-asset breakdown per pool needs a Blend-pool-storage
// reader that doesn't exist yet (#84).
const BLEND_POOL_LABELS: Record<string, string> = {
  CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7: 'Backstop V2',
  CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU: 'Pool Factory V2',
};

export function LendingPoolsTable() {
  const q = useQuery<LendingPool[]>({
    queryKey: ['/v1/lending/pools'],
    queryFn: async () => {
      const env = await apiGet<{ data: LendingPool[] }>('/v1/lending/pools', {});
      return env.data ?? [];
    },
  });

  const rows = q.data ?? [];

  return (
    <Panel
      title={`Pools${rows.length > 0 ? ` (${rows.length})` : ''}`}
      hint="One row per Blend pool observed in the auction stream"
      source={asExample('/v1/lending/pools', {})}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
              <Th>Protocol</Th>
              <Th>Pool</Th>
              <Th align="right">24h auctions</Th>
              <Th align="right">All-time auctions</Th>
              <Th align="right">Users (30d)</Th>
              <Th align="right">Last activity</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {q.isLoading && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-slate-500">
                  Loading pools…
                </td>
              </tr>
            )}
            {!q.isLoading && rows.length === 0 && (
              <tr>
                <td colSpan={6} className="px-4 py-6 text-center text-sm text-slate-500">
                  No Blend pools have emitted auction events yet.
                </td>
              </tr>
            )}
            {rows.map((p) => (
              <tr key={p.pool} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                <Td>
                  <span className="inline-block rounded bg-emerald-100 px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200">
                    {p.protocol}
                  </span>
                </Td>
                <Td>
                  <div className="space-y-0.5">
                    <a
                      href={`https://stellar.expert/explorer/public/contract/${p.pool}`}
                      target="_blank"
                      rel="noreferrer noopener"
                      className="block font-mono text-[11px] hover:text-brand-600"
                      title={p.pool}
                    >
                      {p.pool.slice(0, 6)}…{p.pool.slice(-6)}
                    </a>
                    {BLEND_POOL_LABELS[p.pool] && (
                      <div className="text-[9px] uppercase tracking-wide text-slate-500">
                        {BLEND_POOL_LABELS[p.pool]}
                      </div>
                    )}
                  </div>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                    {p.auctions_24h.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                    {p.auctions_total.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                    {p.unique_users_30d.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono text-xs text-slate-500">
                    {formatRelative(p.last_seen)}
                  </span>
                </Td>
              </tr>
            ))}
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

function formatRelative(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms)) return '—';
  const s = Math.round(ms / 1000);
  if (s < 0) return 'now';
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}
