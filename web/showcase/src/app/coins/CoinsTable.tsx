'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useMemo } from 'react';
import { X } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { Sparkline } from '@/components/primitives';
import { asExample } from '@/api/client';
import { useCoins, type Coin } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * Client-side table backed by the live `/v1/coins` endpoint.
 *
 * The frontend transitioned from a static seed to live data in PR
 * #574 (API endpoint) + this PR (TanStack Query wiring). Sort param
 * is read from `?sort=col:dir`. Future passes will swap to the
 * registry-aware super-table response (price + delta + volume) once
 * the endpoint joins change_summary_5m + classic_asset_stats_5m.
 *
 * Loading / error / empty states render small status panels rather
 * than the table chrome — saves a layout shift on slow connections
 * and keeps the loading state honest.
 */
export function CoinsTable() {
  const params = useSearchParams();
  const sortParam = params.get('sort') ?? 'observation_count:desc';
  const issuerFilter = params.get('issuer') ?? undefined;

  const { data, isLoading, isError, error } = useCoins(100, issuerFilter);

  const rows = useMemo(() => {
    if (!data) return [];
    return sortRows(data, sortParam);
  }, [data, sortParam]);

  // The example URL in the `<>` reveal tracks the actual call —
  // including the issuer param when one is in the URL — so what the
  // panel says it called matches what it called.
  const exampleParams: Record<string, string | number> = { limit: 100 };
  if (issuerFilter) exampleParams.issuer = issuerFilter;
  const exampleUrl = asExample('/v1/coins', exampleParams);

  if (isError) {
    return (
      <Panel
        title="Coin directory"
        source={exampleUrl}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load coins: {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Coin directory"
        source={exampleUrl}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }

  return (
    <Panel
      title={issuerFilter ? `Coins by ${shortIssuer(issuerFilter)}` : undefined}
      hint={issuerFilter ? `${rows.length} match` : undefined}
      source={exampleUrl}
      bodyClassName="-mx-4"
    >
      {issuerFilter && (
        <div className="mb-3 flex items-center gap-2 px-4 text-xs">
          <span className="text-slate-500">Filtered by issuer:</span>
          <span className="rounded bg-brand-100 px-1.5 py-0.5 font-mono text-[10px] text-brand-700 dark:bg-brand-900 dark:text-brand-200">
            {issuerFilter.slice(0, 8)}…{issuerFilter.slice(-4)}
          </span>
          <Link
            href="/coins"
            className="inline-flex items-center gap-0.5 text-slate-500 hover:text-brand-600"
          >
            <X className="h-3 w-3" />
            clear
          </Link>
        </div>
      )}
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>Asset</Th>
              <Th>Issuer</Th>
              <Th align="right">Observations</Th>
              <Th align="right">First seen</Th>
              <Th align="right">Last seen</Th>
              <Th align="right">Activity</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {rows.map((row, i) => (
              <tr
                key={row.asset_id}
                className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
              >
                <Td>
                  <span className="text-slate-400">{i + 1}</span>
                </Td>
                <Td>
                  <Link
                    href={`/coins/${row.slug}`}
                    className="flex items-baseline gap-2 hover:text-brand-600"
                  >
                    <span className="font-medium">{row.code}</span>
                    <span className="text-xs text-slate-500">
                      {row.slug}
                    </span>
                  </Link>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-slate-500">
                    {row.issuer.slice(0, 8)}…{row.issuer.slice(-4)}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">
                    {formatCompact(row.observation_count)}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-xs text-slate-500">
                    #{row.first_seen_ledger.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-xs text-slate-500">
                    #{row.last_seen_ledger.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <Sparkline
                    values={fakeActivity(row.observation_count)}
                    tone="neutral"
                  />
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function Th({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <th
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
      scope="col"
    >
      {children}
    </th>
  );
}

function Td({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <td
      className={`px-4 py-3 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      {children}
    </td>
  );
}

function shortIssuer(g: string): string {
  return `${g.slice(0, 6)}…${g.slice(-4)}`;
}

function sortRows(rows: Coin[], sortParam: string): Coin[] {
  const [col, dir] = sortParam.split(':');
  const desc = dir === 'desc';
  const sorted = [...rows].sort((a, b) => {
    const av = (a as unknown as Record<string, number>)[col!] ?? 0;
    const bv = (b as unknown as Record<string, number>)[col!] ?? 0;
    return desc ? bv - av : av - bv;
  });
  return sorted;
}

/**
 * fakeActivity — deterministic-looking activity sparkline derived
 * from the observation count. Replaced with real prices_1m points
 * once the super-table endpoint surfaces them (Phase 5.1 v1).
 */
function fakeActivity(seed: number): number[] {
  const out: number[] = [];
  let v = (seed % 100) / 10 + 1;
  for (let i = 0; i < 7; i++) {
    out.push(v);
    v = Math.max(0.1, v + ((seed % (i + 7)) - 3) / 5);
  }
  return out;
}
