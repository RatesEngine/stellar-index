'use client';

import Link from 'next/link';

import { useCoins, useVerifiedSlugs, type Coin } from '@/api/hooks';
import {
  EmptyState,
  Skeleton,
  Table,
  TableWrap,
  TBody,
  Td,
  Th,
  THead,
  TR,
} from '@/components/ui';
import { formatCompact } from '@/lib/format';

/**
 * HomeTopAssets — the activity-ranked top 10 from /v1/coins.
 *
 * The endpoint orders by observation_count desc (a proxy for
 * activity), so the first page roughly = "most active assets
 * across all of Stellar." Volume is the trailing-24h USD figure
 * computed from prices_1m. Fields the API doesn't yet expose
 * (price_usd / market_cap_usd) render as `—`.
 *
 * Server-rendered list this isn't — we want this to refresh on
 * the same TanStack cadence as the rest of the home page.
 */
export function HomeTopAssets() {
  const { data, isLoading, isError } = useCoins(
    10,
    undefined,
    undefined,
    undefined,
    undefined,
    { sparkline: true },
  );
  const { data: verifiedSlugs } = useVerifiedSlugs();

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">
            Top assets by activity
          </h2>
          <p className="text-sm text-ink-body">
            Ranked by total observation count across every venue we
            ingest from. 24h volume sums every (base, quote) pair the
            asset participates in.
          </p>
        </div>
        <Link
          href="/assets"
          className="text-sm text-brand-600 hover:underline"
        >
          See all →
        </Link>
      </div>
      {isError ? (
        <EmptyState
          title="Couldn't load top assets."
          description="The assets feed is unavailable right now."
          action={
            <Link href="/assets" className="text-brand-600 hover:underline">
              Browse all assets →
            </Link>
          }
        />
      ) : (
        <TableWrap>
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <Th>#</Th>
                <Th>Asset</Th>
                <Th align="right">Price</Th>
                <Th align="right">24h %</Th>
                <Th align="right">24h volume</Th>
                <Th align="right">24h chart</Th>
                <Th align="right">Observations</Th>
              </TR>
            </THead>
            <TBody>
              {isLoading && !data &&
                Array.from({ length: 8 }).map((_, i) => (
                  <TR key={`sk-${i}`} className="hover:bg-transparent">
                    <Td colSpan={7}>
                      <Skeleton className="h-5 w-full" />
                    </Td>
                  </TR>
                ))}
              {data?.coins.map((coin, idx) => (
                <Row
                  key={coin.asset_id}
                  coin={coin}
                  rank={idx + 1}
                  verified={verifiedSlugs?.has(coin.slug.toLowerCase()) ?? false}
                />
              ))}
            </TBody>
          </Table>
        </TableWrap>
      )}
    </section>
  );
}

function Row({
  coin,
  rank,
  verified,
}: {
  coin: Coin;
  rank: number;
  verified: boolean;
}) {
  const price = parseDec(coin.price_usd);
  const volume = parseDec(coin.volume_24h_usd);
  return (
    <TR>
      <Td className="text-ink-faint">{rank}</Td>
      <Td>
        <Link
          href={`/assets/${coin.slug}`}
          className="group flex items-center gap-2"
        >
          <span aria-hidden className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-subtle font-mono text-xs">
            {iconForCode(coin.code)}
          </span>
          <span className="font-medium text-ink group-hover:text-brand-600">
            {coin.code}
          </span>
          {verified && (
            <span
              title="Verified currency"
              aria-label="Verified currency"
              className="inline-flex items-center"
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                viewBox="0 0 20 20"
                fill="currentColor"
                className="h-3.5 w-3.5 text-up"
                aria-hidden="true"
              >
                <path
                  fillRule="evenodd"
                  d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
                  clipRule="evenodd"
                />
              </svg>
            </span>
          )}
          <span className="text-[11px] text-ink-muted">{coin.slug}</span>
        </Link>
      </Td>
      <Td align="right">
        {price != null ? (
          <span className="font-mono text-ink">${formatPrice(price)}</span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        <ChangePct raw={coin.change_24h_pct} />
      </Td>
      <Td align="right">
        {volume != null ? (
          <span className="font-mono text-ink-body">${formatCompact(volume)}</span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        <RowSparkline points={coin.price_history_24h} />
      </Td>
      <Td align="right">
        <span className="font-mono text-ink-body">
          {formatCompact(coin.observation_count)}
        </span>
      </Td>
    </TR>
  );
}

function parseDec(s: string | null | undefined): number | null {
  if (!s) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

function formatPrice(n: number): string {
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  if (n > 0) return n.toExponential(3);
  return '0';
}

function Dash() {
  return <span className="text-ink-faint">—</span>;
}

function RowSparkline({
  points,
}: {
  points?: { t: string; p?: string | null }[];
}) {
  const values = (points ?? [])
    .map((pt) => (pt.p ? Number(pt.p) : null))
    .filter((v): v is number => v != null && Number.isFinite(v));
  if (values.length < 2) {
    return <span className="font-mono text-[10px] text-ink-faint">—</span>;
  }
  const W = 80;
  const H = 22;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const stepX = W / (values.length - 1);
  const path = values
    .map((v, i) => {
      const x = i * stepX;
      const y = H - ((v - min) / range) * H;
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  const positive = values[values.length - 1] >= values[0];
  // Use the up/down semantic tokens via currentColor so the sparkline
  // tracks the palette (tailwind.config.ts) rather than a frozen hex.
  return (
    <svg
      width={W}
      height={H}
      className={`inline-block ${positive ? 'text-up' : 'text-down'}`}
      viewBox={`0 0 ${W} ${H}`}
      role="img"
      aria-label="24h price chart"
    >
      <path d={path} fill="none" stroke="currentColor" strokeWidth={1.25} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function ChangePct({ raw }: { raw: string | null | undefined }) {
  if (raw == null) return <Dash />;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <Dash />;
  const tone =
    n > 0
      ? 'text-up'
      : n < 0
        ? 'text-down'
        : 'text-ink-muted';
  const sign = n > 0 ? '+' : '';
  return (
    <span className={`font-mono tabular-nums ${tone}`}>
      {sign}
      {n.toFixed(2)}%
    </span>
  );
}

// iconForCode returns a single-glyph stand-in for the asset's
// row icon. Mirrors the unified currencies listing's iconFor so
// home + listing render the same visual treatment for the same
// codes. Real SVG icons land in a follow-up.
function iconForCode(code: string): string {
  const c = code.toUpperCase();
  const map: Record<string, string> = {
    XLM: '✦',
    BTC: '₿',
    ETH: 'Ξ',
    USDC: '$',
    USDT: '$',
    EURC: '€',
    EUROC: '€',
    DAI: '◈',
    LTC: 'Ł',
    DOGE: 'Ð',
    AQUA: '🌊',
    yXLM: '✦',
    BLND: '🟧',
  };
  return map[c] ?? c.slice(0, 1);
}
