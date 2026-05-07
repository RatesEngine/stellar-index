'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, Search, X } from 'lucide-react';

import { useCoins, type Coin } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * /assets directory table — the canonical Stellar asset list.
 *
 * Cursor-paginated against `/v1/coins`. Server emits keyset
 * cursors; we round-trip the active cursor through the URL
 * (`?cursor=…`) so deep links + back-button navigation work
 * without local state drift.
 *
 * Columns are deliberately data-dense and right-aligned for
 * numerics — etherscan / oklink style. Fields the API doesn't
 * yet expose render as `—` rather than placeholder values.
 */
// MARKET_CAP_VOLUME_THRESHOLD_USD — below this 24h USD volume, the
// market-cap column shows "—" because the price feed underlying it
// is too thin for the cap to be a confident number. Per user spec:
// "we probably just wont show a market cap for low volume assets
// because we wont have the data confidence in doing so".
const MARKET_CAP_VOLUME_THRESHOLD_USD = 1_000;

// NETWORK_OPTIONS — Stellar is the only ingested network today.
// Future networks plug in here once their indexer wires up; the
// `?network=` param is honoured but server-side filtering on the
// /v1/coins endpoint is a follow-up (until then the chip is
// effectively a UX promise + sets the URL state).
const NETWORK_OPTIONS = ['all', 'stellar'] as const;
type NetworkOption = typeof NETWORK_OPTIONS[number];

export function AssetsTable() {
  const router = useRouter();
  const params = useSearchParams();
  const cursor = params.get('cursor') ?? '';
  const limitParam = params.get('limit');
  const issuerFilter = params.get('issuer') ?? undefined;
  const queryParam = params.get('q') ?? '';
  const orderParam = params.get('order') === 'volume_24h_usd_desc'
    ? 'volume_24h_usd_desc'
    : 'observation_count_desc';
  const networkParam = (params.get('network') as NetworkOption | null) ?? 'all';

  const limit = parseLimit(limitParam);

  const { data, isLoading, isError, error } = useCoins(
    limit,
    issuerFilter,
    cursor,
    queryParam || undefined,
    orderParam,
  );

  // Local input state, debounced into the URL so the server-side
  // ?q= filter doesn't refire on every keystroke. 250ms is the
  // standard "feels live, doesn't thrash" window — see Algolia's
  // search-as-you-type guidance.
  const [q, setQ] = useState(queryParam);
  useEffect(() => {
    const trimmed = q.trim();
    if (trimmed === queryParam) return;
    const t = setTimeout(() => {
      setQuery({ q: trimmed, cursor: '' });
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);

  const coins = data?.coins ?? [];

  function setQuery(updates: Partial<{ cursor: string; limit: string; issuer: string; q: string; order: string; network: string }>) {
    const next = new URLSearchParams(params.toString());
    for (const [k, v] of Object.entries(updates)) {
      if (v === '' || v === undefined) next.delete(k);
      else next.set(k, v);
    }
    router.push(`/assets?${next.toString()}`);
  }

  if (isError) {
    return (
      <div className="rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900/40 dark:bg-red-950/40 dark:text-red-200">
        Failed to load assets: {error instanceof Error ? error.message : 'unknown error'}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <FilterBar
        q={q}
        onQChange={setQ}
        issuerFilter={issuerFilter}
        onIssuerClear={() => setQuery({ issuer: '', cursor: '' })}
        limit={limit}
        onLimitChange={(v) => setQuery({ limit: String(v), cursor: '' })}
        network={networkParam}
        onNetworkChange={(v) =>
          setQuery({ network: v === 'all' ? '' : v, cursor: '' })
        }
      />

      <div className="overflow-x-auto rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
        <table className="min-w-full text-sm">
          <thead>
            <tr className="border-b border-slate-200 bg-slate-50 text-left text-[11px] uppercase tracking-wider text-slate-500 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-500">
              <Th>#</Th>
              <Th>Asset</Th>
              <Th>Issuer</Th>
              <Th align="right">Price</Th>
              <Th align="right">24h %</Th>
              <Th align="right">Market cap</Th>
              <Th align="right">
                <SortHeader
                  active={orderParam === 'volume_24h_usd_desc'}
                  label="Volume 24h"
                  onClick={() =>
                    setQuery({
                      order:
                        orderParam === 'volume_24h_usd_desc'
                          ? ''
                          : 'volume_24h_usd_desc',
                      cursor: '',
                    })
                  }
                />
              </Th>
              <Th align="right">Circulating</Th>
              <Th align="right">First seen</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {isLoading && (
              <tr>
                <td
                  colSpan={9}
                  className="py-12 text-center text-sm text-slate-500"
                >
                  Loading…
                </td>
              </tr>
            )}
            {!isLoading && coins.length === 0 && (
              <tr>
                <td
                  colSpan={9}
                  className="py-12 text-center text-sm text-slate-500"
                >
                  No assets match this filter.
                </td>
              </tr>
            )}
            {!isLoading &&
              coins.map((coin, idx) => (
                <AssetRow
                  key={coin.asset_id}
                  coin={coin}
                  rank={idx + 1}
                />
              ))}
          </tbody>
        </table>
      </div>

      <Pagination
        cursor={cursor}
        nextCursor={data?.next_cursor ?? ''}
        onPrev={() => router.back()}
        onNext={() =>
          data?.next_cursor && setQuery({ cursor: data.next_cursor })
        }
      />

      <p className="text-xs text-slate-500 dark:text-slate-400">
        Live data from{' '}
        <code className="rounded bg-slate-100 px-1 font-mono text-[11px] dark:bg-slate-800">
          /v1/coins
        </code>
        . Price + 24h change + market cap + volume populate from the
        latest 1-min VWAP, with USD triangulated via XLM when no
        direct fiat:USD pair exists.
      </p>
    </div>
  );
}

function FilterBar({
  q,
  onQChange,
  issuerFilter,
  onIssuerClear,
  limit,
  onLimitChange,
  network,
  onNetworkChange,
}: {
  q: string;
  onQChange: (v: string) => void;
  issuerFilter?: string;
  onIssuerClear: () => void;
  limit: number;
  onLimitChange: (v: number) => void;
  network: NetworkOption;
  onNetworkChange: (v: NetworkOption) => void;
}) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="text-slate-500">Network:</span>
        {NETWORK_OPTIONS.map((n) => (
          <button
            key={n}
            type="button"
            onClick={() => onNetworkChange(n)}
            className={`rounded-full px-2.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${
              network === n
                ? 'bg-brand-600 text-white'
                : 'bg-slate-100 text-slate-600 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-400 dark:hover:bg-slate-700'
            }`}
          >
            {n === 'all' ? 'All networks' : n}
          </button>
        ))}
        <span className="text-slate-400">·</span>
        <span className="text-slate-400">
          Stellar is the only network ingested today; more land as we wire them.
        </span>
      </div>

    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <input
            type="search"
            value={q}
            onChange={(e) => onQChange(e.target.value)}
            placeholder="Search by code, slug, or issuer…"
            className="w-72 rounded-md border border-slate-200 bg-white py-1.5 pl-8 pr-3 text-sm placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:placeholder:text-slate-500"
          />
        </div>
        {issuerFilter && (
          <span className="inline-flex items-center gap-1 rounded-md bg-slate-100 py-1 pl-2.5 pr-1 text-xs text-slate-700 dark:bg-slate-800 dark:text-slate-300">
            issuer: <code className="font-mono">{issuerFilter.slice(0, 8)}…{issuerFilter.slice(-4)}</code>
            <button
              type="button"
              onClick={onIssuerClear}
              className="ml-1 rounded p-0.5 text-slate-500 hover:bg-slate-200 hover:text-slate-700 dark:hover:bg-slate-700"
              aria-label="Clear issuer filter"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        )}
      </div>
      <label className="flex items-center gap-2 text-xs text-slate-500">
        <span>Per page</span>
        <select
          value={limit}
          onChange={(e) => onLimitChange(parseInt(e.target.value, 10))}
          className="rounded-md border border-slate-200 bg-white px-2 py-1 text-xs focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900"
        >
          <option value={50}>50</option>
          <option value={100}>100</option>
          <option value={200}>200</option>
          <option value={500}>500</option>
        </select>
      </label>
    </div>
    </div>
  );
}

function AssetRow({ coin, rank }: { coin: Coin; rank: number }) {
  const price = parseDec(coin.price_usd);
  const marketCapRaw = parseDec(coin.market_cap_usd);
  const volume = parseDec(coin.volume_24h_usd);
  const supply = parseDec(coin.circulating_supply);
  // Suppress market cap when 24h volume is below the confidence
  // threshold — without enough recent trade volume the price
  // underlying the cap is too thin to publish a believable number.
  // Applies uniformly regardless of how supply was sourced.
  const marketCap =
    marketCapRaw != null && volume != null && volume >= MARKET_CAP_VOLUME_THRESHOLD_USD
      ? marketCapRaw
      : null;
  return (
    <tr className="hover:bg-slate-50 dark:hover:bg-slate-800/40">
      <Td>
        <span className="text-slate-400">{rank}</span>
      </Td>
      <Td>
        <Link
          href={`/assets/${coin.slug}`}
          className="group flex items-baseline gap-2"
        >
          <span className="font-medium text-ink group-hover:text-brand-600 dark:text-slate-100">
            {coin.code}
          </span>
          <span className="text-[11px] text-slate-500">{coin.slug}</span>
        </Link>
      </Td>
      <Td>
        {coin.issuer ? (
          <Link
            href={`/issuers/${coin.issuer}`}
            className="font-mono text-[11px] text-slate-500 hover:text-brand-600 dark:text-slate-400"
            title={coin.issuer}
          >
            {coin.issuer.slice(0, 8)}…{coin.issuer.slice(-4)}
          </Link>
        ) : (
          <span className="text-xs text-slate-400">native</span>
        )}
      </Td>
      <Td align="right">
        {price != null ? (
          <span className="font-mono tabular-nums text-ink dark:text-slate-100">
            ${formatPriceSmart(price)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        <ChangePct raw={coin.change_24h_pct} />
      </Td>
      <Td align="right">
        {marketCap != null ? (
          <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
            ${formatCompact(marketCap)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        {volume != null ? (
          <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
            ${formatCompact(volume)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        {supply != null ? (
          <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
            {formatCompact(supply)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        <span className="font-mono text-[11px] text-slate-500">
          #{coin.first_seen_ledger.toLocaleString()}
        </span>
      </Td>
    </tr>
  );
}

function Pagination({
  cursor,
  nextCursor,
  onPrev,
  onNext,
}: {
  cursor: string;
  nextCursor: string;
  onPrev: () => void;
  onNext: () => void;
}) {
  const hasPrev = cursor !== '';
  const hasNext = nextCursor !== '';
  return (
    <div className="flex items-center justify-between gap-2 px-1">
      <button
        type="button"
        disabled={!hasPrev}
        onClick={onPrev}
        className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:opacity-40 disabled:hover:border-slate-200 disabled:hover:text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        Previous
      </button>
      <span className="text-xs text-slate-400">
        {hasPrev || hasNext ? 'Cursor-paginated' : ' '}
      </span>
      <button
        type="button"
        disabled={!hasNext}
        onClick={onNext}
        className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:opacity-40 disabled:hover:border-slate-200 disabled:hover:text-slate-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
      >
        Next
        <ChevronRight className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

// SortHeader is a clickable Th-content that renders a small
// ↓ marker when its column is the active sort. There's only one
// sortable column today (Volume 24h) — when more land, this can
// stay as the same shape, just toggling the matching `order` URL
// param. The default sort (observation_count_desc) is rendered
// without a marker because it's not a "user picked this" choice.
function SortHeader({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`inline-flex items-center gap-1 hover:text-brand-600 ${
        active ? 'text-brand-600' : ''
      }`}
      title={
        active
          ? 'Sorted by 24h USD volume (desc). Click to reset.'
          : 'Sort by 24h USD volume (desc).'
      }
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
      className={`px-4 py-2.5 font-medium ${align === 'right' ? 'text-right' : 'text-left'}`}
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

function Dash() {
  return <span className="text-slate-300 dark:text-slate-700">—</span>;
}

function ChangePct({ raw }: { raw: string | null | undefined }) {
  if (raw == null) return <Dash />;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <Dash />;
  const tone =
    n > 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : n < 0
        ? 'text-rose-600 dark:text-rose-400'
        : 'text-slate-500 dark:text-slate-500';
  const sign = n > 0 ? '+' : '';
  return (
    <span className={`font-mono tabular-nums ${tone}`}>
      {sign}
      {n.toFixed(2)}%
    </span>
  );
}

function parseDec(s: string | null | undefined): number | null {
  if (!s) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

function formatPriceSmart(n: number): string {
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  // sub-millicent — show in scientific so a 1e-9 doesn't dominate
  if (n > 0) return n.toExponential(3);
  return '0';
}

function parseLimit(raw: string | null): number {
  const valid = [50, 100, 200, 500];
  if (!raw) return 100;
  const n = parseInt(raw, 10);
  return valid.includes(n) ? n : 100;
}
