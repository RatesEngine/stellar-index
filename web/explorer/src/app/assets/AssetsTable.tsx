'use client';

import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, Search } from 'lucide-react';

import { useAssets, type AssetClassFilter, type Coin } from '@/api/hooks';
import { formatCompact } from '@/lib/format';
import {
  Badge,
  Button,
  Callout,
  EmptyState,
  TBody,
  TR,
  Table,
  TableWrap,
  Td,
  Th,
  THead,
} from '@/components/ui';

/**
 * /assets directory table — the CMC/CoinGecko-style global asset
 * listing, redesigned per the assets-redesign spec.
 *
 * Sourced from `/v1/assets?asset_class=…` (R-018 assets-unification
 * endgame). Each row:
 *
 *   - For catalogue assets (USDC the currency, GBP, BTC, …):
 *     `asset_id` is the slug; clicking lands on
 *     `/assets/{slug}` (GlobalAssetView).
 *   - For classic_assets non-catalogue (USDC-GA5Z..., AQUA-G..., …):
 *     `asset_id` is the full classic id; clicking lands on
 *     `/assets/{slug}` (handler dispatches to AssetDetail via
 *     ticker-or-canonical-id LookupBySlug).
 *
 * Columns are deliberately data-dense and right-aligned for
 * numerics. Issuer is intentionally NOT a column — issuer detail
 * is surfaced inline on the `/assets/{slug}` detail page.
 */

// MARKET_CAP_VOLUME_THRESHOLD_USD — below this 24h USD volume, the
// market-cap column shows "—" because the price feed underlying it
// is too thin for the cap to be a confident number.
const MARKET_CAP_VOLUME_THRESHOLD_USD = 1_000;

// ASSET_CLASS_OPTIONS — surface labels per the redesign spec.
// "blockchain" is the explorer's name for the catalogue's "crypto"
// class (CMC's "Cryptocurrencies" tab); the server normalises
// blockchain→crypto in `normaliseAssetClass`.
const ASSET_CLASS_OPTIONS: { value: AssetClassFilter; label: string }[] = [
  { value: 'all', label: 'All Assets' },
  { value: 'fiat', label: 'Fiat Currency' },
  { value: 'blockchain', label: 'Crypto' },
  { value: 'stablecoin', label: 'Stablecoin' },
];

function parseAssetClass(raw: string | null): AssetClassFilter {
  switch (raw) {
    case 'fiat':
    case 'blockchain':
    case 'stablecoin':
      return raw;
    default:
      return 'all';
  }
}

export function AssetsTable({
  verifiedSlugs = [],
}: {
  /**
   * Slugs from `/v1/assets/verified` (fetched server-side and
   * passed in). Used to decorate matching rows with a green-check
   * verified badge. Empty array is the safe default.
   */
  verifiedSlugs?: string[];
} = {}) {
  const router = useRouter();
  const params = useSearchParams();
  const verifiedSlugSet = new Set(verifiedSlugs.map((s) => s.toLowerCase()));
  const cursor = params.get('cursor') ?? '';
  const limitParam = params.get('limit');
  const queryParam = params.get('q') ?? '';
  const assetClass = parseAssetClass(params.get('asset_class'));

  const limit = parseLimit(limitParam);

  const { data, isLoading, isError, error } = useAssets(
    assetClass,
    limit,
    cursor,
    queryParam || undefined,
    { sparkline7d: true },
  );

  // Local input state, debounced into the URL so the server-side
  // ?q= filter doesn't refire on every keystroke.
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

  const assets = data?.assets ?? [];

  function setQuery(
    updates: Partial<{
      cursor: string;
      limit: string;
      q: string;
      asset_class: string;
    }>,
  ) {
    const next = new URLSearchParams(params.toString());
    for (const [k, v] of Object.entries(updates)) {
      if (v === '' || v === undefined) next.delete(k);
      else next.set(k, v);
    }
    router.push(`/assets?${next.toString()}`);
  }

  if (isError) {
    return (
      <Callout tone="bad" title="Failed to load assets">
        {error instanceof Error ? error.message : 'unknown error'}
      </Callout>
    );
  }

  return (
    <div className="space-y-5">
      <FilterBar
        q={q}
        onQChange={setQ}
        limit={limit}
        onLimitChange={(v) => setQuery({ limit: String(v), cursor: '' })}
        assetClass={assetClass}
        onAssetClassChange={(v) =>
          setQuery({
            asset_class: v === 'all' ? '' : v,
            // Reset cursor when class changes — different phase,
            // different stream.
            cursor: '',
          })
        }
      />

      {!isLoading && assets.length === 0 ? (
        <EmptyState
          icon={<Search className="h-5 w-5" />}
          title="No assets match this filter"
          description="Try a different asset code, slug, issuer, or class."
        />
      ) : (
        <TableWrap>
          <Table>
            <THead>
              <tr>
                <Th>#</Th>
                <Th>Asset</Th>
                <Th>Class</Th>
                <Th align="right">Price</Th>
                <Th align="right">1h %</Th>
                <Th align="right">24h %</Th>
                <Th align="right">7d %</Th>
                <Th align="right">Market cap</Th>
                <Th align="right">Volume 24h</Th>
                <Th align="right">Circulating</Th>
                <Th align="right">7d chart</Th>
              </tr>
            </THead>
            <TBody>
              {isLoading && (
                <tr>
                  <td
                    colSpan={11}
                    className="py-12 text-center text-sm text-ink-muted"
                  >
                    Loading…
                  </td>
                </tr>
              )}
              {!isLoading &&
                assets.map((coin, idx) => (
                  <AssetRow
                    key={coin.asset_id}
                    coin={coin}
                    rank={idx + 1}
                    verified={verifiedSlugSet.has(coin.slug.toLowerCase())}
                  />
                ))}
            </TBody>
          </Table>
        </TableWrap>
      )}

      <Pagination
        cursor={cursor}
        nextCursor={data?.next_cursor ?? ''}
        onPrev={() => router.back()}
        onNext={() =>
          data?.next_cursor && setQuery({ cursor: data.next_cursor })
        }
      />

      <p className="text-xs text-ink-muted">
        Live data from{' '}
        <code className="rounded bg-surface-subtle px-1 font-mono text-[11px]">
          /v1/assets?asset_class={assetClass}
        </code>
        . Catalogue rows surface first (market-cap desc — fiats top
        the chart), then long-tail Stellar-classic rows by 24h
        volume. Per-asset issuer + on-chain pool detail lives on{' '}
        <code className="rounded bg-surface-subtle px-1 font-mono text-[11px]">
          /assets/&#123;slug&#125;
        </code>
        .
      </p>
    </div>
  );
}

function FilterBar({
  q,
  onQChange,
  limit,
  onLimitChange,
  assetClass,
  onAssetClassChange,
}: {
  q: string;
  onQChange: (v: string) => void;
  limit: number;
  onLimitChange: (v: number) => void;
  assetClass: AssetClassFilter;
  onAssetClassChange: (v: AssetClassFilter) => void;
}) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="text-ink-muted">Asset type:</span>
        {ASSET_CLASS_OPTIONS.map((opt) => (
          <button
            key={opt.value}
            type="button"
            onClick={() => onAssetClassChange(opt.value)}
            aria-pressed={assetClass === opt.value}
            className={`rounded-full px-3 py-1 text-xs font-medium tracking-wide ${
              assetClass === opt.value
                ? 'bg-brand-600 text-white'
                : 'bg-surface-subtle text-ink-body hover:bg-line'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-ink-faint" />
          <input
            type="search"
            aria-label="Search assets by code, slug, or name"
            value={q}
            onChange={(e) => onQChange(e.target.value)}
            placeholder="Search by code, slug, or name…"
            className="w-72 rounded-md border border-line bg-surface py-1.5 pl-8 pr-3 text-sm placeholder:text-ink-faint focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
          />
        </div>
        <label className="flex items-center gap-2 text-xs text-ink-muted">
          <span>Per page</span>
          <select
            value={limit}
            onChange={(e) => onLimitChange(parseInt(e.target.value, 10))}
            className="rounded-md border border-line bg-surface px-2 py-1 text-xs focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500"
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

function AssetRow({
  coin,
  rank,
  verified,
}: {
  coin: Coin;
  rank: number;
  verified: boolean;
}) {
  const price = parseDec(coin.price_usd);
  const marketCapRaw = parseDec(coin.market_cap_usd);
  const volume = parseDec(coin.volume_24h_usd);
  const supply = parseDec(coin.circulating_supply);
  // Suppress market cap when 24h volume is below the confidence
  // threshold — without enough recent trade volume the price
  // underlying the cap is too thin to publish a believable number.
  // Catalogue fiat rows are EXEMPT: their market_cap is computed
  // from a static M2 × current FX rate; trade volume is meaningless
  // for fiat-as-money-supply.
  const marketCap =
    coin.class === 'fiat'
      ? marketCapRaw
      : marketCapRaw != null &&
          volume != null &&
          volume >= MARKET_CAP_VOLUME_THRESHOLD_USD
        ? marketCapRaw
        : null;
  return (
    <TR>
      <Td>
        <span className="text-ink-faint tnum">{rank}</span>
      </Td>
      <Td>
        <Link
          href={`/assets/${coin.slug}`}
          className="group flex items-baseline gap-2"
        >
          <span className="font-medium text-ink group-hover:text-brand-600">
            {coin.code}
          </span>
          {verified && (
            <span
              title="Verified currency — in the catalogue at /v1/assets/verified"
              className="inline-flex items-center"
              aria-label="Verified currency"
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
          <span className="text-[11px] text-ink-muted">
            {coin.name ?? coin.slug}
          </span>
        </Link>
      </Td>
      <Td>
        <ClassBadge cls={coin.class} />
      </Td>
      <Td align="right">
        {price != null ? (
          <span className="font-mono tabular-nums text-ink">
            ${formatPriceSmart(price)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        <ChangePct raw={coin.change_1h_pct} />
      </Td>
      <Td align="right">
        <ChangePct raw={coin.change_24h_pct} />
      </Td>
      <Td align="right">
        <ChangePct raw={coin.change_7d_pct} />
      </Td>
      <Td align="right">
        {marketCap != null ? (
          <span className="font-mono tabular-nums text-ink-body">
            ${formatCompact(marketCap)}
          </span>
        ) : (
          <Dash title="Awaiting circulating supply via SEP-1 / on-chain observer" />
        )}
      </Td>
      <Td align="right">
        {volume != null ? (
          <span className="font-mono tabular-nums text-ink-body">
            ${formatCompact(volume)}
          </span>
        ) : (
          <Dash />
        )}
      </Td>
      <Td align="right">
        {supply != null ? (
          <span className="font-mono tabular-nums text-ink-body">
            {formatCompact(supply)}
          </span>
        ) : (
          <Dash title="Awaiting issuer SEP-1 fixed_number / on-chain mint observer" />
        )}
      </Td>
      <Td align="right">
        <RowSparkline points={coin.price_history_7d} />
      </Td>
    </TR>
  );
}

function ClassBadge({ cls }: { cls?: string }) {
  if (!cls) {
    return <span className="text-xs text-ink-faint">—</span>;
  }
  const tone: 'warn' | 'ok' | 'brand' =
    cls === 'fiat' ? 'warn' : cls === 'stablecoin' ? 'ok' : 'brand';
  const label =
    cls === 'fiat' ? 'Fiat' : cls === 'stablecoin' ? 'Stablecoin' : 'Crypto';
  return <Badge tone={tone}>{label}</Badge>;
}

function RowSparkline({ points }: { points?: { t: string; p?: string | null }[] }) {
  const values = (points ?? [])
    .map((pt) => (pt.p ? Number(pt.p) : null))
    .filter((v): v is number => v != null && Number.isFinite(v));
  if (values.length < 2) {
    return <span className="font-mono text-[10px] text-ink-faint">—</span>;
  }
  const W = 80;
  const H = 24;
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
  return (
    <svg
      width={W}
      height={H}
      viewBox={`0 0 ${W} ${H}`}
      className={`inline-block ${positive ? 'text-up' : 'text-down'}`}
    >
      <path d={path} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
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
      <Button
        variant="secondary"
        size="sm"
        disabled={!hasPrev}
        onClick={onPrev}
      >
        <ChevronLeft className="h-3.5 w-3.5" />
        Previous
      </Button>
      <span className="text-xs text-ink-faint">
        {hasPrev || hasNext ? 'Cursor-paginated' : ' '}
      </span>
      <Button
        variant="secondary"
        size="sm"
        disabled={!hasNext}
        onClick={onNext}
      >
        Next
        <ChevronRight className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}

function Dash({ title }: { title?: string }) {
  return (
    <span
      className="text-ink-faint"
      title={title ?? 'No data yet'}
    >
      —
    </span>
  );
}

function ChangePct({ raw }: { raw: string | null | undefined }) {
  if (raw == null)
    return <Dash title="Not enough trade history to compute this window" />;
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

function parseDec(s: string | null | undefined): number | null {
  if (!s) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

function formatPriceSmart(n: number): string {
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  if (n > 0) return n.toExponential(3);
  return '0';
}

function parseLimit(raw: string | null): number {
  const valid = [50, 100, 200, 500];
  if (!raw) return 100;
  const n = parseInt(raw, 10);
  return valid.includes(n) ? n : 100;
}
