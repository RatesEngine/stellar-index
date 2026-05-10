'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { AssetLabel } from '@/components/AssetLabel';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';

interface Market {
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
}

type Order = 'volume_24h_usd_desc' | 'pair';

const PAGE_LIMIT = 100;

/**
 * PoolsTable — every (base, quote) pair the source observed in
 * the trailing 14d. Backed by /v1/markets?source=<name> with
 * cursor pagination. Sortable header on the volume column flips
 * order_by between volume_desc and pair-alphabetical; cursor
 * resets on order change so paging stays consistent.
 *
 * Rendered client-side so the table can paginate without route
 * changes. Deep-link to /markets/<base~quote> on each row gives
 * the standard pair detail (chart, OHLC, recent trades, per-source
 * breakdown).
 */
export function PoolsTable({
  source,
  sourceName,
}: {
  source: string;
  sourceName: string;
}) {
  const [order, setOrder] = useState<Order>('volume_24h_usd_desc');
  const [cursor, setCursor] = useState<string>('');
  const [cursorStack, setCursorStack] = useState<string[]>([]);

  const q = useQuery<{ markets: Market[]; nextCursor?: string }>({
    queryKey: ['/v1/markets', source, order, cursor],
    queryFn: async () => {
      const env = await apiGet<{
        data: Market[];
        pagination?: { next?: string };
      }>('/v1/markets', {
        source,
        order_by: order,
        limit: PAGE_LIMIT,
        ...(cursor ? { cursor } : {}),
      });
      return {
        markets: env.data ?? [],
        nextCursor: env.pagination?.next,
      };
    },
  });

  function nextPage() {
    const next = q.data?.nextCursor;
    if (!next) return;
    setCursorStack((s) => [...s, cursor]);
    setCursor(next);
  }
  function prevPage() {
    setCursorStack((s) => {
      const head = s[s.length - 1] ?? '';
      setCursor(head);
      return s.slice(0, -1);
    });
  }
  function changeOrder(next: Order) {
    setOrder(next);
    setCursor('');
    setCursorStack([]);
  }

  const markets = q.data?.markets ?? [];
  const hasNext = !!q.data?.nextCursor;
  const hasPrev = cursorStack.length > 0;

  return (
    <Panel
      title={`${sourceName} pools`}
      hint="One row per (base, quote) pair observed in the last 14 days"
      source={asExample('/v1/markets', { source, order_by: order, limit: PAGE_LIMIT })}
      bodyClassName="-mx-4"
    >
      <div className="px-4 pb-3 pt-1">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-slate-500">Sort:</span>
          <SortPill
            active={order === 'volume_24h_usd_desc'}
            onClick={() => changeOrder('volume_24h_usd_desc')}
          >
            24h volume ↓
          </SortPill>
          <SortPill
            active={order === 'pair'}
            onClick={() => changeOrder('pair')}
          >
            Pair (A→Z)
          </SortPill>
          <span className="ml-auto font-mono text-[11px] text-slate-500">
            {markets.length} on this page
            {q.isFetching && ' · refreshing…'}
          </span>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>Base</Th>
              <Th>Quote</Th>
              <Th align="right">Last price</Th>
              <Th align="right">24h volume</Th>
              <Th align="right">24h trades</Th>
              <Th align="right">Last trade</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {q.isLoading && !q.data && (
              <tr>
                <td colSpan={7} className="px-4 py-8 text-center text-sm text-slate-500">
                  Loading pools…
                </td>
              </tr>
            )}
            {!q.isLoading && markets.length === 0 && (
              <tr>
                <td colSpan={7} className="px-4 py-8 text-center text-sm text-slate-500">
                  No pools found in the last 14 days.
                </td>
              </tr>
            )}
            {markets.map((m, i) => {
              const slug = `${m.base}~${m.quote}`;
              const offset = cursorStack.length * PAGE_LIMIT + i + 1;
              const vol = m.volume_24h_usd ? Number(m.volume_24h_usd) : null;
              return (
                <tr
                  key={`${m.base}|${m.quote}`}
                  className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                >
                  <Td>
                    <span className="font-mono text-[11px] text-slate-400">
                      {offset}
                    </span>
                  </Td>
                  <Td>
                    <Link
                      href={`/markets/${encodeURIComponent(slug)}`}
                      className="hover:text-brand-600"
                    >
                      <AssetLabel canonical={m.base} />
                    </Link>
                  </Td>
                  <Td>
                    <Link
                      href={`/markets/${encodeURIComponent(slug)}`}
                      className="hover:text-brand-600"
                    >
                      <AssetLabel canonical={m.quote} />
                    </Link>
                  </Td>
                  <Td align="right">
                    <LastPriceCell raw={m.last_price} />
                  </Td>
                  <Td align="right">
                    {vol != null && Number.isFinite(vol) && vol > 0 ? (
                      <span className="font-mono tabular-nums">
                        ${formatCompact(vol)}
                      </span>
                    ) : (
                      <span className="text-slate-300 dark:text-slate-700">—</span>
                    )}
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
                      {m.trade_count_24h > 0
                        ? formatCompact(m.trade_count_24h)
                        : '0'}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-xs text-slate-500">
                      {formatRelative(m.last_trade_at)}
                    </span>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between border-t border-slate-200 px-4 py-2 text-xs dark:border-slate-800">
        <button
          type="button"
          onClick={prevPage}
          disabled={!hasPrev}
          className="rounded-md border border-slate-200 px-3 py-1 text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-400"
        >
          ← Previous
        </button>
        <span className="font-mono text-[11px] text-slate-400">
          page {cursorStack.length + 1}
        </span>
        <button
          type="button"
          onClick={nextPage}
          disabled={!hasNext}
          className="rounded-md border border-slate-200 px-3 py-1 text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-400"
        >
          Next →
        </button>
      </div>
    </Panel>
  );
}

function SortPill({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-md px-2 py-0.5 ${
        active
          ? 'bg-brand-600 text-white'
          : 'bg-slate-100 text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700'
      }`}
    >
      {children}
    </button>
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
      scope="col"
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
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
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      {children}
    </td>
  );
}

function LastPriceCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-slate-300 dark:text-slate-700">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-slate-300 dark:text-slate-700">—</span>;
  const fixed =
    n >= 1000 ? n.toFixed(2) : n >= 1 ? n.toFixed(4) : n >= 0.0001 ? n.toFixed(6) : n.toExponential(3);
  return (
    <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
      {fixed}
    </span>
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
