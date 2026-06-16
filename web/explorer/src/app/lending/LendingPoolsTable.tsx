'use client';

import Link from 'next/link';
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

// Curated metadata for every Blend mainnet contract we know of.
// Sourced from docs/operations/wasm-audits/blend.md (Phase 4 walk,
// last verified 2026-05-03). Reserve-asset breakdown per pool
// needs a Blend-pool-storage reader that doesn't exist yet (#84);
// until then this table at least gives users deploy timestamps +
// initiator addresses so pools are distinguishable.
interface PoolMeta {
  label: string;
  deployedAt?: string;
  initiator?: string;
}

const BLEND_POOL_META: Record<string, PoolMeta> = {
  CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7: {
    label: 'Backstop V2',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU: {
    label: 'Pool Factory V2',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CAJJZSGMMM3PD7N33TAPHGBUGTB43OC73HVIK2L2G6BNGGGYOSSYBXBD: {
    label: 'Pool #1 (genesis)',
    deployedAt: '2025-04-14',
    initiator: 'GAX2VVWVHU5YQY5J3NJBXKHI3FFKZN54BE6GRJCWSIKSBZTQWJJNJMPC',
  },
  CBNR7PYFY775UG7W37B4OJG2OBBUKLFW6VIBHFDKKLR2HECPRMRZMDK3: {
    label: 'Pool #2',
    deployedAt: '2025-04-15',
    initiator: 'GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4',
  },
  CCCCIQSDILITHMM7PBSLVDT5MISSY7R26MNZXCX4H7J5JQ5FPIYOGYFS: {
    label: 'Pool #3',
    deployedAt: '2025-04-17',
    initiator: 'GBCAS7XIGDRZY4BMABJMGGW7J3YTITRRV5BTEMFQE5ZZSSVWHHX2ZSS4',
  },
  CB4OFHAY2TAEYUVPOJS36S657C6NYMSIFUNCCA5AHYT46Y5XUID3O2ED: {
    label: 'Pool #4',
    deployedAt: '2025-05-01',
    initiator: 'GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT',
  },
  CAE7QVOMBLZ53CDRGK3UNRRHG5EZ5NQA7HHTFASEMYBWHG6MDFZTYHXC: {
    label: 'Pool #5',
    deployedAt: '2025-05-01',
    initiator: 'GBIWJGAOSFC4KUPHXM573TKTWHMI7VW7D4GCHYZYH243Q6HVBV7ORBIT',
  },
  CBYOBT7ZCCLQCBUYYIABZLSEGDPEUWXCUXQTZYOG3YBDR7U357D5ZIRF: {
    label: 'Pool #6',
    deployedAt: '2025-07-13',
    initiator: 'GCCI7K6QU6FVVIXWSLKRPTBKJCFBLEJKPTZMP27A2KL37N4ZL3OCM3GI',
  },
  CALRF5I2OCJCU577R6MZBCY5IIXNMAAG6PNMN7GUKEYIXBJCJN2FJRVI: {
    label: 'Pool #7',
    deployedAt: '2025-11-22',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
  CADR6Q2UOCDJAGXMAB2E6SRT35STLZ2IGLZUCXJQG7TC2LNKCU5RTQVY: {
    label: 'Pool #8',
    deployedAt: '2025-11-25',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
  CDMAVJPFXPADND3YRL4BSM3AKZWCTFMX27GLLXCML3PD62HEQS5FPVAI: {
    label: 'Pool #9',
    deployedAt: '2025-11-25',
    initiator: 'GDH3FRHOOWXYXEASH43N2VOVFOPJSVJF3EQFSLBLJYFPHOUAF4N4AETH',
  },
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
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
              <Th>Protocol</Th>
              <Th>Pool</Th>
              <Th>Deployed</Th>
              <Th align="right">24h auctions</Th>
              <Th align="right">All-time auctions</Th>
              <Th align="right">Users (30d)</Th>
              <Th align="right">Last activity</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {q.isLoading && (
              <tr>
                <td colSpan={7} className="px-4 py-6 text-center text-sm text-ink-muted">
                  Loading pools…
                </td>
              </tr>
            )}
            {!q.isLoading && rows.length === 0 && (
              <tr>
                <td colSpan={7} className="px-4 py-6 text-center text-sm text-ink-muted">
                  No Blend pools have emitted auction events yet.
                </td>
              </tr>
            )}
            {rows.map((p) => {
              const meta = BLEND_POOL_META[p.pool];
              return (
                <tr key={p.pool} className="hover:bg-surface-muted">
                  <Td>
                    <span className="inline-block rounded bg-up-subtle px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-up-strong">
                      {p.protocol}
                    </span>
                  </Td>
                  <Td>
                    <div className="space-y-0.5">
                      <Link
                        href={`/lending/${p.pool}`}
                        className="block font-mono text-[11px] hover:text-brand-600"
                        title={p.pool}
                      >
                        {p.pool.slice(0, 6)}…{p.pool.slice(-6)}
                      </Link>
                      {meta?.label && (
                        <div className="text-[9px] uppercase tracking-wide text-ink-muted">
                          {meta.label}
                        </div>
                      )}
                    </div>
                  </Td>
                  <Td>
                    {meta?.deployedAt ? (
                      <div className="space-y-0.5">
                        <div className="font-mono text-[11px] text-ink-body">
                          {meta.deployedAt}
                        </div>
                        {meta.initiator && (
                          <div
                            className="font-mono text-[9px] text-ink-muted"
                            title={meta.initiator}
                          >
                            by {meta.initiator.slice(0, 4)}…{meta.initiator.slice(-4)}
                          </div>
                        )}
                      </div>
                    ) : (
                      <span className="text-ink-faint">—</span>
                    )}
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-ink-body">
                      {p.auctions_24h.toLocaleString()}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-ink-body">
                      {p.auctions_total.toLocaleString()}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-ink-body">
                      {p.unique_users_30d.toLocaleString()}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono text-xs text-ink-muted">
                      {formatRelative(p.last_seen)}
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
