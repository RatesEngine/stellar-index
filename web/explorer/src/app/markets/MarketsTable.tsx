'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { AssetLabel } from '@/components/AssetLabel';
import { SourceSparkline } from '@/components/SourceSparkline';
import { useMarkets } from '@/api/hooks';
import { formatCompact } from '@/lib/format';
import { Input, TBody, TR, Table, Td, Th, THead } from '@/components/ui';

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
        source={asExample('/v1/markets', { limit: 100, order_by: orderBy, include: 'sparkline' })}
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
        source={asExample('/v1/markets', { limit: 100, order_by: orderBy, include: 'sparkline' })}
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }
  if (data.markets.length === 0) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 100, order_by: orderBy, include: 'sparkline' })}
        bodyClassName="text-sm text-ink-muted"
      >
        No active markets in the last 14 days.
      </Panel>
    );
  }

  return (
    <Panel
      title={`${data.markets.length} active markets`}
      hint="Pairs that traded in the last 14 days, ordered by 24h USD volume"
      source={asExample('/v1/markets', { limit: 100, order_by: orderBy, include: 'sparkline' })}
      bodyClassName="-mx-4"
    >
      <div className="px-4 pb-3 pt-1">
        <div className="flex flex-wrap items-center gap-3 text-xs">
          <Input
            type="search"
            aria-label="Filter markets by base or quote asset"
            placeholder="Filter by base or quote asset…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-72 font-mono text-[11px]"
          />
          <span className="font-mono text-[11px] text-ink-muted">
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
        <Table>
          <THead>
            <tr>
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
          </THead>
          <TBody>
            {sorted.map((m, i) => {
              const slug = `${m.base}~${m.quote}`;
              return (
              <TR key={`${m.base}|${m.quote}`}>
                <Td>
                  <Link
                    href={`/markets/${encodeURIComponent(slug)}`}
                    className="text-ink-faint hover:text-brand-600"
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
                    <span className="text-ink-faint">—</span>
                  )}
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-ink-body">
                    {formatCompact(m.trade_count_24h)}
                  </span>
                </Td>
                <Td>
                  <SourceSparkline buckets={m.volume_history_24h} />
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-xs text-ink-muted">
                    {formatRelative(m.last_trade_at)}
                  </span>
                </Td>
              </TR>
              );
            })}
          </TBody>
        </Table>
      </div>
    </Panel>
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

function LastPriceCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-ink-faint">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-ink-faint">—</span>;
  // Pair prices are quote-per-base — they span >9 orders of
  // magnitude across the 5K active pairs (sub-satoshi memecoins
  // through XLM-USD), so digits adapt to keep precision visible.
  const fixed =
    n >= 1000 ? n.toFixed(2) : n >= 1 ? n.toFixed(4) : n >= 0.0001 ? n.toFixed(6) : n.toExponential(3);
  return (
    <span className="font-mono tabular-nums text-ink-body">
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
