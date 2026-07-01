'use client';

import Link from 'next/link';
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetLink } from '@/components/AssetLink';
import { apiGet, asExample } from '@/api/client';
import { formatRelative } from '@/lib/format';

interface SourceRow {
  name: string;
  class: string;
  subclass: string;
  trade_count_24h?: number;
  markets_count_24h?: number;
  volume_24h_usd?: string | null;
}

interface OracleStream {
  source: string;
  contract_id?: string;
  asset: string;
  quote: string;
  ts: string;
  price: string;
  decimals: number;
  confidence?: number;
  observer?: string;
}

const TONE: Record<string, string> = {
  'reflector-dex': 'bg-up-subtle text-up-strong',
  'reflector-cex': 'bg-brand-100 text-brand-800',
  'reflector-fx': 'bg-violet-100 text-violet-800',
  redstone: 'bg-down-subtle text-down-strong',
  band: 'bg-warn-50 text-warn-700',
};

export function OraclesView() {
  const sources = useQuery<SourceRow[]>({
    queryKey: ['/v1/sources', 'stats', 'oracle'],
    queryFn: async () => {
      const env = await apiGet<{ data: SourceRow[] }>('/v1/sources', { include: 'stats' });
      const arr = env.data ?? [];
      return arr.filter((s) => s.class === 'oracle').sort((a, b) => a.name.localeCompare(b.name));
    },
  });

  const streams = useQuery<OracleStream[]>({
    queryKey: ['/v1/oracle/streams'],
    queryFn: async () => {
      const env = await apiGet<{ data: OracleStream[] }>('/v1/oracle/streams', {});
      return env.data ?? [];
    },
  });

  const oracles = sources.data ?? [];
  // Memoise the `streams.data ?? []` defaulting so the perSourceCounts
  // useMemo dep array stays referentially stable across renders. Without
  // this, `streamRows` is a fresh `[]` literal on every render when
  // streams.data is undefined, making the downstream useMemo recompute
  // every tick. F-1258 (audit-2026-05-12).
  const streamRows = useMemo(() => streams.data ?? [], [streams.data]);

  const perSourceCounts = useMemo(() => {
    const map: Record<string, { streams: number; latestTs: string }> = {};
    for (const s of streamRows) {
      const cur = map[s.source];
      if (!cur || s.ts > cur.latestTs) {
        map[s.source] = {
          streams: (cur?.streams ?? 0) + 1,
          latestTs: cur ? (s.ts > cur.latestTs ? s.ts : cur.latestTs) : s.ts,
        };
      } else {
        map[s.source].streams++;
      }
    }
    return map;
  }, [streamRows]);

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Oracles</h1>
        <p className="max-w-3xl text-sm text-ink-body">
          Every on-chain Stellar oracle we ingest and cross-reference.
          Oracles are reported alongside our independent VWAP but never
          included in it — mixing them would import their methodology
          and double-count whichever upstream markets they read.
        </p>
      </header>

      <Panel
        title="Connected oracles"
        hint="Per-oracle 24h activity"
        source={asExample('/v1/sources', { class: 'oracle', include: 'stats' })}
        bodyClassName="-mx-4"
      >
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <Th>Oracle</Th>
                <Th align="right">Active streams</Th>
                <Th align="right">Last update</Th>
                <Th align="right">In VWAP?</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {sources.isLoading && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-sm text-ink-muted">
                    Loading oracles…
                  </td>
                </tr>
              )}
              {!sources.isLoading && oracles.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-4 py-6 text-center text-sm text-ink-muted">
                    No oracles registered.
                  </td>
                </tr>
              )}
              {oracles.map((o) => {
                const perSrc = perSourceCounts[o.name];
                const tone = TONE[o.name] ?? 'bg-surface-subtle text-ink-body';
                return (
                  <tr key={o.name} className="hover:bg-surface-muted">
                    <Td>
                      <Link
                        href={`/sources/${o.name}`}
                        className={`inline-block rounded-sm px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {o.name}
                      </Link>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-ink-body">
                        {perSrc ? perSrc.streams : '—'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs text-ink-muted">
                        {perSrc ? formatRelative(perSrc.latestTs) : '—'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span
                        className={`inline-block rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
                          o.class === 'oracle'
                            ? 'bg-line text-ink-body'
                            : 'bg-up-subtle text-up-strong'
                        }`}
                        title="Oracle observations are reported but never included in the canonical VWAP — that would import their methodology."
                      >
                        no (policy)
                      </span>
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel
        title={`Price streams${streamRows.length > 0 ? ` (${streamRows.length} active)` : ''}`}
        hint="Latest observation per (oracle, asset, quote) — 7d window"
        source={asExample('/v1/oracle/streams', {})}
        bodyClassName="-mx-4"
      >
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <Th>Oracle</Th>
                <Th>Asset</Th>
                <Th>Quote</Th>
                <Th align="right">Latest price</Th>
                <Th align="right">Updated</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {streams.isLoading && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-ink-muted">
                    Loading streams…
                  </td>
                </tr>
              )}
              {!streams.isLoading && streamRows.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-ink-muted">
                    No oracle observations in the last 7 days.
                  </td>
                </tr>
              )}
              {streamRows.map((s, i) => {
                const tone = TONE[s.source] ?? 'bg-surface-subtle text-ink-body';
                return (
                  <tr key={`${s.source}|${s.asset}|${s.quote}|${i}`} className="hover:bg-surface-muted">
                    <Td>
                      <Link
                        href={`/sources/${s.source}`}
                        className={`inline-block rounded-sm px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {s.source}
                      </Link>
                    </Td>
                    <Td>
                      <AssetLink canonical={s.asset} />
                    </Td>
                    <Td>
                      <AssetLink canonical={s.quote} />
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-ink-body">
                        {formatPrice(s.price)}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs text-ink-muted">
                        {formatRelative(s.ts)}
                      </span>
                    </Td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </Panel>

      <Panel
        title="SEP-40 compatibility"
        hint="Drop-in oracle interface"
        source={asExample('/v1/oracle/lastprice', { asset: 'native' })}
        bodyClassName="space-y-2 text-sm text-ink-body"
      >
        <p>
          We expose three SEP-40 endpoints —{' '}
          <code className="font-mono text-xs">/v1/oracle/lastprice</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/prices</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/x_last_price</code>{' '}
          — that match the SEP-40 contract trait on-chain consumers
          already integrate against. Routing your existing on-chain{' '}
          <code className="font-mono text-xs">lastprice()</code> calls
          through Stellar Index swaps in independent VWAP-backed prices
          without touching the calling contract.
        </p>
      </Panel>
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

function formatPrice(p: string): string {
  const n = Number(p);
  if (!Number.isFinite(n)) return p;
  if (n === 0) return '0';
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.01) return n.toFixed(6);
  return n.toExponential(3);
}
