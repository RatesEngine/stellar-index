'use client';

import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useMarkets } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * Live-data markets table backed by `/v1/markets`.
 *
 * Sorts client-side by `trade_count_24h` desc — most active first.
 * Per ADR-0015 the API returns "active markets" only (pairs that
 * traded in the last 14 days). Cursor pagination is plumbed through
 * the hook but the v0 page only shows the first 100; "Load more"
 * lands once we add virtual scrolling.
 */
export function MarketsTable() {
  // Server-side ordering by volume_24h_usd desc (with NULLS LAST)
  // gives us the high-activity pairs in the first page directly,
  // no client sort needed for relevance.
  const { data, isLoading, isError, error } = useMarkets(
    100,
    'volume_24h_usd_desc',
  );

  const sorted = data?.markets ?? [];

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
      title={`${sorted.length} active markets`}
      hint="Pairs that traded in the last 14 days, ordered by 24h USD volume"
      source={asExample('/v1/markets', { limit: 500 })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>Base</Th>
              <Th>Quote</Th>
              <Th align="right">24h volume</Th>
              <Th align="right">24h trades</Th>
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
function AssetLabel({ canonical }: { canonical: string }) {
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
