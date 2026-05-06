'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useMemo, useState } from 'react';
import { Search, X } from 'lucide-react';

import { Panel } from '@/components/reveal';
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
  const queryParam = params.get('q') ?? '';

  const { data, isLoading, isError, error } = useCoins(100, issuerFilter);

  const sorted = useMemo(() => {
    if (!data) return [];
    return sortRows(data, sortParam);
  }, [data, sortParam]);

  const rows = useMemo(() => {
    const q = queryParam.trim().toLowerCase();
    if (!q) return sorted;
    return sorted.filter((c) =>
      c.code.toLowerCase().includes(q) ||
      c.slug.toLowerCase().includes(q) ||
      (c.issuer ?? '').toLowerCase().includes(q),
    );
  }, [sorted, queryParam]);

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

  const totalCount = sorted.length;
  const filteredCount = rows.length;

  return (
    <Panel
      title={issuerFilter ? `Coins by ${shortIssuer(issuerFilter)}` : undefined}
      hint={
        issuerFilter
          ? `${rows.length} match`
          : queryParam
            ? `${filteredCount} of ${totalCount}`
            : undefined
      }
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
      <SearchBar initialValue={queryParam} />
      {queryParam && filteredCount === 0 && (
        <p className="mb-2 px-4 text-sm text-slate-500">
          No coins match{' '}
          <code className="rounded bg-slate-100 px-1 font-mono text-xs dark:bg-slate-800">
            {queryParam}
          </code>
          . Search runs against code, slug, and issuer.
        </p>
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

// SearchBar — controlled input that mirrors `?q=` in the URL.
// Pure client-side filter (the /v1/coins endpoint doesn't take a
// search param yet); typing is responsive because the input owns
// local state and only commits to the URL on debounce.
function SearchBar({ initialValue }: { initialValue: string }) {
  const router = useRouter();
  const params = useSearchParams();
  const [value, setValue] = useState(initialValue);

  function commit(next: string) {
    const u = new URLSearchParams(params.toString());
    if (next) u.set('q', next);
    else u.delete('q');
    const qs = u.toString();
    router.replace(qs ? `/coins?${qs}` : '/coins', { scroll: false });
  }

  return (
    <div className="mb-3 flex items-center gap-2 px-4">
      <div className="relative flex-1 max-w-sm">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-400" />
        <input
          type="search"
          inputMode="search"
          autoComplete="off"
          spellCheck={false}
          placeholder="Search by code, slug, issuer…"
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            commit(e.target.value);
          }}
          className="w-full rounded-md border border-slate-200 bg-white py-1.5 pl-8 pr-8 text-sm placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:placeholder:text-slate-500"
        />
        {value && (
          <button
            type="button"
            onClick={() => {
              setValue('');
              commit('');
            }}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300"
            aria-label="Clear search"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
    </div>
  );
}
