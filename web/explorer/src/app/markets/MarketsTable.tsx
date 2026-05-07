'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { SourceSparkline } from '@/components/SourceSparkline';
import { useMarkets } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * Live-data markets table backed by `/v1/markets`.
 *
 * Default sort is `volume_24h_usd_desc` so the high-activity pairs
 * land in the first page. Click the 24h volume header to toggle
 * back to the alphabetical-by-pair sort (the API's `pair` order_by);
 * URL ?order= preserves the choice across navigation.
 *
 * Per ADR-0015 the API returns "active markets" only (pairs that
 * traded in the last 14 days). Cursor pagination is plumbed through
 * the hook but the v0 page only shows the first 100; "Load more"
 * lands once we add virtual scrolling.
 */
export function MarketsTable() {
  const router = useRouter();
  const params = useSearchParams();
  const orderParam = params.get('order') ?? '';
  // Default to volume desc (high-activity first); ?order=pair flips
  // to the API's alphabetical-by-pair order. Anything else falls
  // back to the default.
  const orderBy: 'volume_24h_usd_desc' | 'pair' =
    orderParam === 'pair' ? 'pair' : 'volume_24h_usd_desc';

  const { data, isLoading, isError, error } = useMarkets(100, orderBy, { sparkline: true });
  const [filter, setFilter] = useState('');

  const sorted = useMemo(() => {
    const rows = data?.markets ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return rows;
    return rows.filter((m) => `${m.base ?? ''} ${m.quote ?? ''}`.toLowerCase().includes(q));
  }, [data, filter]);

  function setOrder(next: 'volume_24h_usd_desc' | 'pair') {
    const sp = new URLSearchParams(params.toString());
    if (next === 'volume_24h_usd_desc') sp.delete('order');
    else sp.set('order', next);
    router.replace(`/markets${sp.toString() ? `?${sp.toString()}` : ''}`);
  }

  if (isError) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 500 })}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load markets:{' '}
        {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 500 })}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }
  if (data.markets.length === 0) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 500 })}
        bodyClassName="text-sm text-slate-500"
      >
        No active markets in the last 14 days.
      </Panel>
    );
  }

  return (
    <Panel
      title={`${data.markets.length} active markets`}
      hint="Pairs that traded in the last 14 days, ordered by 24h USD volume"
      source={asExample('/v1/markets', { limit: 500 })}
      bodyClassName="-mx-4"
    >
      <div className="px-4 pb-3 pt-1">
        <div className="flex flex-wrap items-center gap-3 text-xs">
          <input
            type="search"
            placeholder="Filter by base or quote asset…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-72 rounded-md border border-slate-200 bg-white px-2.5 py-1 font-mono text-[11px] placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900"
          />
          <span className="font-mono text-[11px] text-slate-500">
            {sorted.length} of {data.markets.length} rows
            {filter && (
              <button
                type="button"
                onClick={() => setFilter('')}
                className="ml-2 text-brand-600 hover:underline"
              >
                clear
              </button>
            )}
          </span>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>
                <SortHeader
                  active={orderBy === 'pair'}
                  label="Base"
                  hint={
                    orderBy === 'pair'
                      ? 'Sorted alphabetically. Click to sort by 24h volume.'
                      : 'Sort alphabetically by base asset.'
                  }
                  onClick={() =>
                    setOrder(
                      orderBy === 'pair' ? 'volume_24h_usd_desc' : 'pair',
                    )
                  }
                />
              </Th>
              <Th>Quote</Th>
              <Th align="right">Last price</Th>
              <Th align="right">
                <SortHeader
                  active={orderBy === 'volume_24h_usd_desc'}
                  label="24h volume"
                  hint={
                    orderBy === 'volume_24h_usd_desc'
                      ? 'Sorted by 24h USD volume (desc). Click to sort alphabetically.'
                      : 'Sort by 24h USD volume (desc).'
                  }
                  onClick={() => setOrder('volume_24h_usd_desc')}
                />
              </Th>
              <Th align="right">24h trades</Th>
              <Th>24h chart</Th>
              <Th align="right">Last trade</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {sorted.map((m, i) => {
              const slug = `${m.base}~${m.quote}`;
              return (
              <tr
                key={`${m.base}|${m.quote}`}
                className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
              >
                <Td>
                  <Link
                    href={`/markets/${encodeURIComponent(slug)}`}
                    className="text-slate-400 hover:text-brand-600"
                  >
                    {i + 1}
                  </Link>
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
                  {m.volume_24h_usd ? (
                    <span className="font-mono tabular-nums">
                      ${formatCompact(Number(m.volume_24h_usd))}
                    </span>
                  ) : (
                    <span className="text-slate-300 dark:text-slate-700">—</span>
                  )}
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
                    {formatCompact(m.trade_count_24h)}
                  </span>
                </Td>
                <Td>
                  <SourceSparkline buckets={m.volume_history_24h} />
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
    </Panel>
  );
}

/**
 * AssetLabel renders a canonical asset string compactly. The v1 API
 * returns assets as `<code>-<G-issuer>` for classic, `<num>` for
 * native (XLM is `0`), and `fiat:USD` style for off-chain. We split
 * on the first dash to get code + issuer, and render code prominent
 * with a truncated issuer beneath.
 */
function AssetLabel({ canonical }: { canonical: string | undefined | null }) {
  if (!canonical) return <span className="text-xs text-slate-400">—</span>;
  if (canonical.startsWith('fiat:')) {
    return <span className="font-medium">{canonical.replace('fiat:', '')}</span>;
  }
  // Native: numeric strings ("0", "1", …) — for now show as raw
  if (/^\d+$/.test(canonical)) {
    return <span className="font-medium">XLM-native</span>;
  }
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return <span className="font-mono text-xs">{canonical}</span>;
  const code = canonical.slice(0, dashIx);
  const issuer = canonical.slice(dashIx + 1);
  return (
    <div>
      <div className="font-medium">{code}</div>
      <div className="font-mono text-[10px] text-slate-500">
        {issuer.length > 12 ? `${issuer.slice(0, 8)}…${issuer.slice(-4)}` : issuer}
      </div>
    </div>
  );
}

// SortHeader is a clickable th-content with a small marker that
// animates on/off when the column is the active sort. Two columns
// here are sortable (Base alphabetically and 24h volume desc); the
// rest are fixed because the API doesn't support sorting on them.
function SortHeader({
  active,
  label,
  hint,
  onClick,
}: {
  active: boolean;
  label: string;
  hint: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={hint}
      className={`inline-flex items-center gap-1 hover:text-brand-600 ${
        active ? 'text-brand-600' : ''
      }`}
    >
      {label}
      <span aria-hidden className="text-[10px]">
        {active ? '↓' : '↕'}
      </span>
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

function LastPriceCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-slate-300 dark:text-slate-700">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-slate-300 dark:text-slate-700">—</span>;
  // Pair prices are quote-per-base — they span >9 orders of
  // magnitude across the 5K active pairs (sub-satoshi memecoins
  // through XLM-USD), so digits adapt to keep precision visible.
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
  if (ms < 0) return 'now';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86_400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86_400)}d ago`;
}
