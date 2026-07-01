'use client';

import { useMemo } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useMarkets, type Market } from '@/api/hooks';
import { formatCompact, formatRelative } from '@/lib/format';

/**
 * MarketsTabPanel — backs the "Markets" tab on /assets/[slug].
 *
 * Pulls `/v1/markets?asset=<slug>` and renders the result. The
 * server expands catalogue slugs (e.g. "btc", "usdc", "xlm") to
 * every asset_id form the catalogue knows: Stellar networks[]
 * .asset_id entries plus the global `crypto:<TICKER>` /
 * `fiat:<TICKER>` form, then unions trade rows where any of those
 * appear on either side of a pair. Net result: a single panel
 * surfaces both Stellar SDEX markets (USDC-GA5Z..., native, …)
 * AND CEX markets (crypto:BTC/crypto:USDT, crypto:XLM/fiat:USD,
 * etc.) under the same slug — the cross-chain summary view per
 * the assets-redesign spec.
 *
 * For non-catalogue slugs (long-tail classic_assets), the server
 * treats `?asset=` as a canonical asset_id and returns the pre-
 * existing per-asset_id stream.
 */
export function MarketsTabPanel({ assetID }: { assetID: string }) {
  const markets = useMarkets(100, 'volume_24h_usd_desc', { asset: assetID });

  // Sort client-side by trade_count_24h desc as a secondary order
  // — the API returns the fanned-out merge already sorted by
  // trade_count, but a defensive re-sort keeps the column order
  // predictable when the API path is unfilteredly volume-desc.
  const matched = useMemo(() => {
    if (!markets.data) return [];
    return [...markets.data.markets].sort(
      (a, b) => b.trade_count_24h - a.trade_count_24h,
    );
  }, [markets.data]);

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
        bodyClassName="text-sm text-ink-muted"
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
        bodyClassName="text-sm text-ink-muted"
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
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>Side</Th>
              <Th>Pair</Th>
              <Th align="right">24h trades</Th>
              <Th align="right">Last trade</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
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
    <tr className="hover:bg-surface-muted">
      <Td>
        <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
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
        <span className="font-mono tabular-nums text-xs text-ink-muted">
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
