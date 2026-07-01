'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { AssetLabel } from '@/components/AssetLabel';
import { apiGet, asExample } from '@/api/client';
import { formatCompact, formatRelative } from '@/lib/format';
import {
  Button,
  Container,
  PageHeader,
  TBody,
  TR,
  Table,
  Td,
  Th,
  THead,
} from '@/components/ui';

import { DexProtocolsTable } from './DexProtocolsTable';

interface Pool {
  source: string;
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
}

type Order = 'volume_24h_usd_desc' | 'pair';

const PAGE_LIMIT = 100;

// Hard-coded to mirror internal/sources/external/registry.go's
// Subclass=DEX entries. Frontend doesn't have an /v1/sources?role=
// filter yet, so the pill row is static — keeps the chips visible
// before the first /v1/pools response lands.
const ALL_DEXES = ['aquarius', 'comet', 'phoenix', 'sdex', 'soroswap'];

// Source-name annotations that appear next to the source chip.
// Comet's only mainnet deployment is Blend's backstop pool —
// every Comet trade on Stellar is part of a liquidation auction,
// not retail price discovery. Surface that context inline so the
// row isn't read as a normal AMM venue. See
// docs/operations/wasm-audits/comet.md.
const SOURCE_NOTE: Record<string, string> = {
  comet: 'Blend backstop',
};

// Source name → category styling. Anything outside this list still
// renders, just without a coloured chip — keeps the table working
// when new sources land before this map gets updated.
const SOURCE_TONE: Record<string, string> = {
  soroswap: 'bg-up-subtle text-up-strong',
  phoenix: 'bg-warn-50 text-warn-700',
  aquarius: 'bg-brand-100 text-brand-800',
  sdex: 'bg-line text-ink',
  comet: 'bg-violet-100 text-violet-800',
  binance: 'bg-yellow-100 text-yellow-800',
  coinbase: 'bg-brand-100 text-brand-800',
  kraken: 'bg-purple-100 text-purple-800',
  bitstamp: 'bg-teal-100 text-teal-800',
};

/**
 * DexesView — the all-pools explorer table. Same UX as /assets
 * (sortable header, paginated, drillable) but listing every
 * (source, base, quote) tuple in the trades store. Source filter
 * chips at the top let visitors scope the table to one venue.
 */
export function DexesView() {
  const [order, setOrder] = useState<Order>('volume_24h_usd_desc');
  const [cursor, setCursor] = useState<string>('');
  const [cursorStack, setCursorStack] = useState<string[]>([]);
  // Source filter is server-side. Empty string = all DEXes.
  const [sourceFilter, setSourceFilter] = useState<string>('');

  const q = useQuery<{ pools: Pool[]; nextCursor?: string }>({
    queryKey: ['/v1/pools', order, cursor, sourceFilter],
    queryFn: async () => {
      const env = await apiGet<{
        data: Pool[];
        pagination?: { next?: string };
      }>('/v1/pools', {
        order_by: order,
        limit: PAGE_LIMIT,
        ...(cursor ? { cursor } : {}),
        ...(sourceFilter ? { source: sourceFilter } : {}),
      });
      return {
        pools: env.data ?? [],
        nextCursor: env.pagination?.next,
      };
    },
  });

  const pools = q.data?.pools ?? [];

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
  function changeSource(next: string) {
    setSourceFilter(next);
    setCursor('');
    setCursorStack([]);
  }

  const hasNext = !!q.data?.nextCursor;
  const hasPrev = cursorStack.length > 0;

  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <PageHeader
        eyebrow="On-chain venues"
        title="DEXes"
        description={
          <>
            Every Stellar DEX we ingest — Soroswap, Phoenix, Aquarius, Comet,
            and the Stellar-native order book SDEX. The first table summarises
            each protocol; the second lists every (DEX, base, quote) pool
            we&apos;ve observed in the last 14 days. CEX trading pairs (Binance,
            Coinbase, Kraken, Bitstamp) live at{' '}
            <Link href="/exchanges" className="text-brand-600 hover:underline">
              /exchanges
            </Link>
            ; &ldquo;pool&rdquo; is AMM/DEX terminology.
          </>
        }
      />

      <DexProtocolsTable />

      <Panel
        title={`${pools.length} pools on this page${sourceFilter ? ` (${sourceFilter} only)` : ''}`}
        hint="Source: /v1/pools"
        source={asExample('/v1/pools', {
          limit: PAGE_LIMIT,
          order_by: order,
          ...(sourceFilter ? { source: sourceFilter } : {}),
        })}
        bodyClassName="-mx-4"
      >
        <div className="space-y-3 px-4 pb-3 pt-1">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-ink-muted">Venue:</span>
            <SourceChip
              active={sourceFilter === ''}
              onClick={() => changeSource('')}
              label="All DEXes"
            />
            {ALL_DEXES.map((s) => (
              <SourceChip
                key={s}
                active={sourceFilter === s}
                onClick={() => changeSource(s)}
                label={s}
              />
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="text-ink-muted">Sort:</span>
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
              Source / pair (A→Z)
            </SortPill>
          </div>
        </div>

        <div className="overflow-x-auto">
          <Table>
            <THead>
              <tr>
                <Th>#</Th>
                <Th>Venue</Th>
                <Th>Base</Th>
                <Th>Quote</Th>
                <Th align="right">Last price</Th>
                <Th align="right">24h volume</Th>
                <Th align="right">24h trades</Th>
                <Th align="right">Last trade</Th>
              </tr>
            </THead>
            <TBody>
              {q.isLoading && !q.data && (
                <tr>
                  <td colSpan={8} className="px-4 py-8 text-center text-sm text-ink-muted">
                    Loading pools…
                  </td>
                </tr>
              )}
              {/* Distinguish "API failed" from "API returned 0 rows".
                  Pre-2026-05-13 a 503 from /v1/pools (the trades-
                  hypertable scan timed out) silently rendered as
                  "No pools matched." — QA finding F-02 in
                  docs/review-2026-05-13-live-site-qa.md. */}
              {!q.isLoading && q.isError && (
                <tr>
                  <td colSpan={8} className="px-4 py-8 text-center text-sm">
                    <div className="text-bad-700">
                      Couldn&apos;t load pools right now.
                    </div>
                    <div className="mt-1 text-xs text-ink-muted">
                      The pools query is timing out (likely a hot
                      trades-hypertable scan). Retry or check{' '}
                      <a
                        href="/status"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="underline-offset-2 hover:underline"
                      >
                        the status page
                      </a>
                      .
                    </div>
                    <button
                      type="button"
                      onClick={() => q.refetch()}
                      className="mt-2 rounded-md border border-bad-500/40 px-3 py-1 text-xs text-bad-700 hover:bg-bad-50"
                    >
                      Retry
                    </button>
                  </td>
                </tr>
              )}
              {!q.isLoading && !q.isError && pools.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-8 text-center text-sm text-ink-muted">
                    No pools matched.
                  </td>
                </tr>
              )}
              {pools.map((p, i) => {
                const slug = `${p.base}~${p.quote}`;
                const offset = cursorStack.length * PAGE_LIMIT + i + 1;
                const vol = p.volume_24h_usd ? Number(p.volume_24h_usd) : null;
                const tone = SOURCE_TONE[p.source] ?? 'bg-surface-subtle text-ink-body';
                return (
                  <TR key={`${p.source}|${p.base}|${p.quote}`}>
                    <Td>
                      <span className="font-mono text-[11px] text-ink-faint">
                        {offset}
                      </span>
                    </Td>
                    <Td>
                      <Link
                        href={`/dexes/${p.source}`}
                        className={`inline-block rounded-sm px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider hover:underline ${tone}`}
                      >
                        {p.source}
                      </Link>
                      {SOURCE_NOTE[p.source] && (
                        <div className="mt-0.5 text-[9px] uppercase tracking-wide text-ink-muted">
                          {SOURCE_NOTE[p.source]}
                        </div>
                      )}
                    </Td>
                    <Td>
                      <Link
                        href={`/markets/${encodeURIComponent(slug)}`}
                        className="hover:text-brand-600"
                      >
                        <AssetLabel canonical={p.base} />
                      </Link>
                    </Td>
                    <Td>
                      <Link
                        href={`/markets/${encodeURIComponent(slug)}`}
                        className="hover:text-brand-600"
                      >
                        <AssetLabel canonical={p.quote} />
                      </Link>
                    </Td>
                    <Td align="right">
                      <LastPriceCell raw={p.last_price} />
                    </Td>
                    <Td align="right">
                      {vol != null && Number.isFinite(vol) && vol > 0 ? (
                        <span className="font-mono tabular-nums">
                          ${formatCompact(vol)}
                        </span>
                      ) : (
                        <span className="text-ink-faint">—</span>
                      )}
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-ink-body">
                        {p.trade_count_24h > 0
                          ? formatCompact(p.trade_count_24h)
                          : '0'}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums text-xs text-ink-muted">
                        {formatRelative(p.last_trade_at)}
                      </span>
                    </Td>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        </div>

        <div className="flex items-center justify-between border-t border-line px-4 py-3 text-xs">
          <Button
            variant="secondary"
            size="sm"
            onClick={prevPage}
            disabled={!hasPrev}
          >
            ← Previous
          </Button>
          <span className="font-mono text-[11px] text-ink-faint">
            page {cursorStack.length + 1}
          </span>
          <Button
            variant="secondary"
            size="sm"
            onClick={nextPage}
            disabled={!hasNext}
          >
            Next →
          </Button>
        </div>
      </Panel>

      <p className="text-xs text-ink-muted">
        Drill into a single DEX&apos;s pools at{' '}
        <Link href="/dexes/sdex" className="text-brand-600 hover:underline">
          /dexes/sdex
        </Link>
        ,{' '}
        <Link href="/dexes/soroswap" className="text-brand-600 hover:underline">
          /dexes/soroswap
        </Link>
        ,{' '}
        <Link href="/dexes/phoenix" className="text-brand-600 hover:underline">
          /dexes/phoenix
        </Link>
        ,{' '}
        <Link href="/dexes/aquarius" className="text-brand-600 hover:underline">
          /dexes/aquarius
        </Link>
        ,{' '}
        <Link href="/dexes/comet" className="text-brand-600 hover:underline">
          /dexes/comet
        </Link>
        .
      </p>
    </Container>
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
          : 'bg-surface-subtle text-ink-body hover:bg-line'
      }`}
    >
      {children}
    </button>
  );
}

function SourceChip({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`rounded-full px-2 py-0.5 font-mono text-[10px] uppercase tracking-wider ${
        active
          ? 'bg-brand-600 text-white'
          : 'bg-surface-subtle text-ink-body hover:bg-line'
      }`}
    >
      {label}
    </button>
  );
}

function LastPriceCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-ink-faint">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-ink-faint">—</span>;
  const fixed =
    n >= 1000 ? n.toFixed(2) : n >= 1 ? n.toFixed(4) : n >= 0.0001 ? n.toFixed(6) : n.toExponential(3);
  return (
    <span className="font-mono tabular-nums text-ink-body">
      {fixed}
    </span>
  );
}
