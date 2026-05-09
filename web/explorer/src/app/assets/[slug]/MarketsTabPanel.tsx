'use client';

import { useMemo } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useMarkets, type Market } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * MarketsTabPanel — backs the "Markets" tab on /assets/[slug].
 *
 * Pulls `/v1/markets` (recently-active pairs in the last 14d) and
 * filters to markets where `base == asset_id` or `quote == asset_id`.
 * The asset_id comes from the parent server component's
 * `/v1/coins/{slug}` lookup.
 */
export function MarketsTabPanel({ assetID }: { assetID: string }) {
  // Server-side filter via `?asset=<assetID>` — the API restricts
  // the listing to pairs where this asset appears on either side,
  // so we get back exactly the rows we'd want to render. The
  // client-side filter below is a defensive guard: older API
  // versions silently ignore unknown query params, so on a
  // pre-`?asset=` deployment the response would be the global
  // top-100 instead of this asset's markets — without the guard
  // every asset detail page would render the same global list.
  // Once every region is on a release that includes the filter,
  // the client-side filter can be dropped.
  const markets = useMarkets(100, 'volume_24h_usd_desc', { asset: assetID });

  const matched = useMemo(() => {
    if (!markets.data) return [];
    return markets.data.markets
      .filter((m) => m.base === assetID || m.quote === assetID)
      .sort((a, b) => b.trade_count_24h - a.trade_count_24h);
  }, [markets.data, assetID]);

  if (markets.isError) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 100 })}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load markets.
      </Panel>
    );
  }
  if (markets.isLoading) {
    return (
      <Panel
        title="Markets"
        source={asExample('/v1/markets', { limit: 100 })}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }
  if (matched.length === 0) {
    return (
      <Panel
        title="Markets"
        hint="No active markets in the last 14 days"
        source={asExample('/v1/markets', { limit: 100 })}
        bodyClassName="text-sm text-slate-500"
      >
        No (base, quote) pair involving this asset has traded in the
        recency window.
      </Panel>
    );
  }

  return (
    <Panel
      title={`${matched.length} active market${matched.length === 1 ? '' : 's'}`}
      hint="Pairs involving this coin that traded in the last 14 days"
      source={asExample('/v1/markets', { limit: 100 })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>Side</Th>
              <Th>Pair</Th>
              <Th align="right">24h trades</Th>
              <Th align="right">Last trade</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {matched.map((m) => (
              <Row key={`${m.base}|${m.quote}`} m={m} assetID={assetID} />
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

function Row({ m, assetID }: { m: Market; assetID: string }) {
  const isBase = m.base === assetID;
  const counterparty = isBase ? m.quote : m.base;
  return (
    <tr className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
      <Td>
        <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-400">
          {isBase ? 'base' : 'quote'}
        </span>
      </Td>
      <Td>
        <span className="font-medium">vs </span>
        <span className="font-mono text-xs">{shortAsset(counterparty)}</span>
      </Td>
      <Td align="right">
        <span className="font-mono tabular-nums">
          {formatCompact(m.trade_count_24h)}
        </span>
      </Td>
      <Td align="right">
        <span className="font-mono tabular-nums text-xs text-slate-500">
          {formatRelative(m.last_trade_at)}
        </span>
      </Td>
    </tr>
  );
}

function shortAsset(canonical: string): string {
  if (canonical.startsWith('fiat:')) return canonical;
  if (/^\d+$/.test(canonical)) return 'XLM';
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  const code = canonical.slice(0, dashIx);
  const issuer = canonical.slice(dashIx + 1);
  return `${code} (${issuer.slice(0, 6)}…${issuer.slice(-4)})`;
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

function formatRelative(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 0) return 'now';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86_400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86_400)}d ago`;
}
