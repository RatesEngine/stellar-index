'use client';

import Link from 'next/link';
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetLabel } from '@/components/AssetLabel';
import { apiGet, asExample } from '@/api/client';

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
  'reflector-dex': 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200',
  'reflector-cex': 'bg-sky-100 text-sky-800 dark:bg-sky-900/40 dark:text-sky-200',
  'reflector-fx': 'bg-violet-100 text-violet-800 dark:bg-violet-900/40 dark:text-violet-200',
  redstone: 'bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-200',
  band: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
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
  const streamRows = streams.data ?? [];

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
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
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
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <Th>Oracle</Th>
                <Th align="right">24h updates</Th>
                <Th align="right">Active streams</Th>
                <Th align="right">Last update</Th>
                <Th align="right">In VWAP?</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {sources.isLoading && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-500">
                    Loading oracles…
                  </td>
                </tr>
              )}
              {!sources.isLoading && oracles.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-500">
                    No oracles registered.
                  </td>
                </tr>
              )}
              {oracles.map((o) => {
                const perSrc = perSourceCounts[o.name];
                const tone = TONE[o.name] ?? 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
                return (
                  <tr key={o.name} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                    <Td>
                      <Link
                        href={`/sources/${o.name}`}
                        className={`inline-block rounded px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {o.name}
                      </Link>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        {o.trade_count_24h && o.trade_count_24h > 0 ? o.trade_count_24h.toLocaleString() : '0'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        {perSrc ? perSrc.streams : '—'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs text-slate-500">
                        {perSrc ? formatRelative(perSrc.latestTs) : '—'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span
                        className={`inline-block rounded px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${
                          o.class === 'oracle'
                            ? 'bg-slate-200 text-slate-600 dark:bg-slate-800 dark:text-slate-400'
                            : 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200'
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
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <Th>Oracle</Th>
                <Th>Asset</Th>
                <Th>Quote</Th>
                <Th align="right">Latest price</Th>
                <Th align="right">Updated</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {streams.isLoading && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-500">
                    Loading streams…
                  </td>
                </tr>
              )}
              {!streams.isLoading && streamRows.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-sm text-slate-500">
                    No oracle observations in the last 7 days.
                  </td>
                </tr>
              )}
              {streamRows.map((s, i) => {
                const tone = TONE[s.source] ?? 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
                return (
                  <tr key={`${s.source}|${s.asset}|${s.quote}|${i}`} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                    <Td>
                      <Link
                        href={`/sources/${s.source}`}
                        className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {s.source}
                      </Link>
                    </Td>
                    <Td>
                      <AssetLabel canonical={s.asset} />
                    </Td>
                    <Td>
                      <AssetLabel canonical={s.quote} />
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        {formatPrice(s.price)}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs text-slate-500">
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
        bodyClassName="space-y-2 text-sm text-slate-600 dark:text-slate-400"
      >
        <p>
          We expose three SEP-40 endpoints —{' '}
          <code className="font-mono text-xs">/v1/oracle/lastprice</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/prices</code>,{' '}
          <code className="font-mono text-xs">/v1/oracle/x_last_price</code>{' '}
          — that match the SEP-40 contract trait on-chain consumers
          already integrate against. Routing your existing on-chain{' '}
          <code className="font-mono text-xs">lastprice()</code> calls
          through Rates Engine swaps in independent VWAP-backed prices
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

function formatPrice(p: string): string {
  const n = Number(p);
  if (!Number.isFinite(n)) return p;
  if (n === 0) return '0';
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.01) return n.toFixed(6);
  return n.toExponential(3);
}
